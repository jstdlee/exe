//go:build windows

package vmm

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/containers/gvisor-tap-vsock/pkg/types"
	"github.com/containers/gvisor-tap-vsock/pkg/virtualnetwork"
)

// guestNetwork is the VMs' network, running entirely inside the daemon: a
// gvisor netstack switch with DHCP, DNS and outbound NAT. QEMU processes
// stream ethernet frames to it over a loopback TCP socket (-netdev stream),
// so no TAP drivers or admin rights are involved. Guest IPs are only
// reachable through DialContext; Forward publishes single ports on 127.0.0.1
// for the host's browser.
type guestNetwork struct {
	subnet  *net.IPNet
	gateway net.IP
	vn      *virtualnetwork.VirtualNetwork
	ln      net.Listener // loopback endpoint QEMU connects to
	port    int

	mu       sync.Mutex
	forwards map[string]net.Listener // "guestIP:port" -> 127.0.0.1 listener
}

func newGuestNetwork(cidr string) (*guestNetwork, error) {
	ip, subnet, err := net.ParseCIDR(cidr)
	if err != nil || ip.To4() == nil {
		return nil, fmt.Errorf("invalid QEMU IPv4 network %q", cidr)
	}
	ones, bits := subnet.Mask.Size()
	if bits != 32 || ones < 16 || ones > 30 {
		return nil, fmt.Errorf("QEMU network %q must be an IPv4 subnet between /16 and /30", cidr)
	}
	subnet.IP = ip.To4().Mask(subnet.Mask)
	gateway := numberIPv4(ipv4Number(subnet.IP) + 1)

	// Every usable address gets a static DHCP lease for its derived MAC, so
	// a VM created later boots straight into the IP recorded in its vm.json.
	leases := make(map[string]string)
	base := ipv4Number(subnet.IP)
	size := uint32(1) << uint(32-ones)
	for offset := uint32(2); offset < size-1; offset++ {
		a := numberIPv4(base + offset)
		leases[a.String()] = macForIPv4(a)
	}
	vn, err := virtualnetwork.New(&types.Configuration{
		MTU:               1500,
		Subnet:            subnet.String(),
		GatewayIP:         gateway.String(),
		GatewayMacAddress: macForIPv4(gateway),
		DHCPStaticLeases:  leases,
		Forwards:          map[string]string{},
		NAT:               map[string]string{},
		Protocol:          types.QemuProtocol,
	})
	if err != nil {
		return nil, fmt.Errorf("create guest network: %w", err)
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("guest network endpoint: %w", err)
	}
	n := &guestNetwork{
		subnet:   subnet,
		gateway:  gateway,
		vn:       vn,
		ln:       ln,
		port:     ln.Addr().(*net.TCPAddr).Port,
		forwards: make(map[string]net.Listener),
	}
	go n.acceptLoop()
	return n, nil
}

// acceptLoop attaches each connecting QEMU to the switch; a connection lives
// as long as its VM.
func (n *guestNetwork) acceptLoop() {
	for {
		conn, err := n.ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			err := n.vn.AcceptQemu(context.Background(), conn)
			if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				log.Printf("guest network: VM stream ended: %v", err)
			}
		}()
	}
}

func (n *guestNetwork) Close() error {
	n.mu.Lock()
	for key, fwd := range n.forwards {
		fwd.Close()
		delete(n.forwards, key)
	}
	n.mu.Unlock()
	return n.ln.Close()
}

// Port is the loopback TCP port QEMU's -netdev stream connects to.
func (n *guestNetwork) Port() int { return n.port }

func (n *guestNetwork) GatewayIP() string { return n.gateway.String() }

func (n *guestNetwork) PrefixLen() int {
	ones, _ := n.subnet.Mask.Size()
	return ones
}

// MACForIP derives a VM's MAC from its IP; the DHCP static leases are built
// from the same mapping, which is what pins each VM to its recorded address.
func (n *guestNetwork) MACForIP(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed == nil || parsed.To4() == nil {
		return ""
	}
	return macForIPv4(parsed.To4())
}

// macForIPv4 maps an address to a locally administered unicast MAC
// (0x5a = local + unicast) using the low three IP bytes, unique within any
// subnet of /16 or smaller.
func macForIPv4(ip net.IP) string {
	v4 := ip.To4()
	return fmt.Sprintf("5a:94:ef:%02x:%02x:%02x", v4[1], v4[2], v4[3])
}

// Allocate picks the first usable address not in used.
func (n *guestNetwork) Allocate(used map[string]bool) (string, error) {
	base := ipv4Number(n.subnet.IP)
	ones, _ := n.subnet.Mask.Size()
	size := uint32(1) << uint(32-ones)
	for offset := uint32(2); offset < size-1; offset++ {
		candidate := numberIPv4(base + offset).String()
		if !used[candidate] {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("QEMU network %s has no free addresses", n.subnet.String())
}

func ipv4Number(ip net.IP) uint32 {
	return binary.BigEndian.Uint32(ip.To4())
}

func numberIPv4(v uint32) net.IP {
	ip := make(net.IP, net.IPv4len)
	binary.BigEndian.PutUint32(ip, v)
	return ip
}

// DialContext connects to guest-subnet addresses through the netstack and to
// everything else through the host network, so it is safe as a blanket
// dialer for the reverse proxy.
func (n *guestNetwork) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err == nil {
		if ip := net.ParseIP(host); ip != nil && n.subnet.Contains(ip) {
			return n.vn.DialContextTCP(ctx, addr)
		}
	}
	d := net.Dialer{Timeout: 10 * time.Second}
	return d.DialContext(ctx, network, addr)
}

// Forward publishes guestIP:port on a host-local address, creating the
// forward on first use; it lives until CloseForwards.
func (n *guestNetwork) Forward(guestIP string, port int) (string, error) {
	key := net.JoinHostPort(guestIP, strconv.Itoa(port))
	n.mu.Lock()
	defer n.mu.Unlock()
	if fwd := n.forwards[key]; fwd != nil {
		return fwd.Addr().String(), nil
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	n.forwards[key] = ln
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go n.relay(conn, key)
		}
	}()
	return ln.Addr().String(), nil
}

func (n *guestNetwork) relay(conn net.Conn, guestAddr string) {
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	guest, err := n.vn.DialContextTCP(ctx, guestAddr)
	cancel()
	if err != nil {
		return
	}
	defer guest.Close()
	done := make(chan struct{}, 2)
	go func() { io.Copy(guest, conn); done <- struct{}{} }()
	go func() { io.Copy(conn, guest); done <- struct{}{} }()
	<-done
}

// CloseForwards drops every host-local forward pointing at guestIP; called
// when its VM stops.
func (n *guestNetwork) CloseForwards(guestIP string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for key, fwd := range n.forwards {
		if host, _, err := net.SplitHostPort(key); err == nil && host == guestIP {
			fwd.Close()
			delete(n.forwards, key)
		}
	}
}
