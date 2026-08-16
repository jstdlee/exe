package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadPreservesFirecrackerDefaultsForExistingConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("EXE_HOME", dir)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"listen":"9000"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if config.Listen != ":9000" {
		t.Fatalf("listen = %q", config.Listen)
	}
	if config.Firecracker.Binary != "firecracker" {
		t.Fatalf("binary = %q", config.Firecracker.Binary)
	}
	if config.Firecracker.NetworkHelper != "/usr/local/libexec/exe-net-helper" {
		t.Fatalf("network helper = %q", config.Firecracker.NetworkHelper)
	}
	if config.Firecracker.NetworkCIDR != "172.30.0.0/16" {
		t.Fatalf("network CIDR = %q", config.Firecracker.NetworkCIDR)
	}
	wantArch := "aarch64"
	if runtime.GOARCH == "amd64" {
		wantArch = "x86_64"
	}
	if !strings.Contains(config.Firecracker.KernelURL, "/"+wantArch+"/") {
		t.Fatalf("kernel URL %q does not select %s", config.Firecracker.KernelURL, wantArch)
	}
}

func TestIdleStopAndHostAgentNormalize(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("EXE_HOME", dir)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"idle_stop_minutes":-5,"host_agent":" Grok ","host_model":" grok-4.6 "}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IdleStopMinutes != 0 {
		t.Fatalf("idle = %d", cfg.IdleStopMinutes)
	}
	if cfg.HostAgent != "grok" || cfg.HostModel != "grok-4.6" {
		t.Fatalf("host %q %q", cfg.HostAgent, cfg.HostModel)
	}
}
