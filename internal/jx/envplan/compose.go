package envplan

import (
	"path"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

func isCompose(p string) bool {
	switch p {
	case "compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml":
		return true
	}
	return false
}

type composeFile struct {
	Services map[string]composeService `yaml:"services"`
}

type composeService struct {
	Image       string      `yaml:"image"`
	Build       yaml.Node   `yaml:"build"`
	Ports       []yaml.Node `yaml:"ports"`
	Expose      []yaml.Node `yaml:"expose"`
	Environment yaml.Node   `yaml:"environment"`
}

func (b *builder) parseCompose(p string, data []byte) {
	b.source(p)
	var f composeFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		b.note("%s: not valid YAML (%v)", p, err)
		return
	}
	for _, name := range sortedKeys(f.Services) {
		b.composeService(p, name, f.Services[name], true)
	}
}

// composeService maps one compose (or CI job) service. Ports count only for
// compose: a CI service's ports are for its own job.
func (b *builder) composeService(src, name string, svc composeService, ports bool) {
	if ports {
		for _, n := range append(svc.Ports, svc.Expose...) {
			if port := containerPort(n); port > 0 {
				b.ports[port] = true
			}
		}
	}
	if svc.Image == "" {
		if !svc.Build.IsZero() {
			b.note("%s: service %q builds from a Dockerfile; its packages come from the project's own manifests", src, name)
		}
		return
	}
	mapped, envKeys := b.image(src, name, svc.Image), envNames(svc.Environment)
	if mapped && len(envKeys) > 0 && isServer(svc.Image) {
		b.note("%s: service %q sets %s; configure the installed server to match", src, name, strings.Join(envKeys, ", "))
	}
}

// containerPort reads a compose port entry: "8080:80", "127.0.0.1:8080:80/tcp",
// "3000", "9000-9001:9000-9001" (first port of the range), or the long form
// {target: 80, published: 8080}. The guest runs the service natively, so the
// container port is the one it listens on.
func containerPort(n yaml.Node) int {
	s := n.Value
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == "target" {
				s = n.Content[i+1].Value
			}
		}
	}
	s, _, _ = strings.Cut(s, "/")
	if i := strings.LastIndex(s, ":"); i >= 0 {
		s = s[i+1:]
	}
	s, _, _ = strings.Cut(s, "-")
	port, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || port <= 0 || port > 65535 {
		return 0
	}
	return port
}

// envNames lists the variable names of a compose environment block (list
// or map form); values are never read.
func envNames(n yaml.Node) []string {
	var out []string
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i < len(n.Content); i += 2 {
			out = append(out, n.Content[i].Value)
		}
	case yaml.SequenceNode:
		for _, c := range n.Content {
			k, _, _ := strings.Cut(c.Value, "=")
			out = append(out, k)
		}
	}
	return out
}

// imageName splits "docker.io/library/postgres:16-alpine" into
// ("postgres", "16-alpine").
func imageName(image string) (name, tag string) {
	image, _, _ = strings.Cut(image, "@")
	name = path.Base(image)
	name, tag, _ = strings.Cut(name, ":")
	return strings.ToLower(name), tag
}

// service is a server package the guest runs natively in place of a
// container. Debian starts packaged servers itself; Alpine's OpenRC needs
// them enabled.
type service struct {
	name  string // OpenRC service
	setup bool   // the init script has a one-time "setup" action
}

func (s service) alpineStep() Step {
	run := "rc-update add " + s.name + " default >/dev/null 2>&1 || true\n"
	if s.setup {
		run += "rc-service " + s.name + " setup >/dev/null 2>&1 || true\n"
	}
	run += "rc-service " + s.name + " start || echo \"warning: " + s.name + " did not start\" >&2"
	return Step{Name: "start " + s.name, Root: true, Distro: Alpine, Run: run}
}

type serverImage struct {
	prefixes []string
	deb, apk []string
	svc      service
}

// serverImages map container images of common backing services to distro
// packages. A prefix match covers variants (postgis, redis-stack).
var serverImages = []serverImage{
	{[]string{"postgres", "postgis"}, []string{"postgresql"}, []string{"postgresql", "postgresql-contrib"}, service{"postgresql", true}},
	{[]string{"redis", "valkey"}, []string{"redis-server"}, []string{"redis"}, service{name: "redis"}},
	{[]string{"mysql", "mariadb"}, []string{"mariadb-server"}, []string{"mariadb", "mariadb-client"}, service{"mariadb", true}},
	{[]string{"memcached"}, []string{"memcached"}, []string{"memcached"}, service{name: "memcached"}},
	{[]string{"nginx"}, []string{"nginx"}, []string{"nginx"}, service{name: "nginx"}},
	{[]string{"rabbitmq"}, []string{"rabbitmq-server"}, []string{"rabbitmq-server"}, service{name: "rabbitmq-server"}},
}

func serverFor(name string) (serverImage, bool) {
	for _, s := range serverImages {
		for _, p := range s.prefixes {
			if strings.HasPrefix(name, p) {
				return s, true
			}
		}
	}
	return serverImage{}, false
}

func isServer(image string) bool {
	name, _ := imageName(image)
	_, ok := serverFor(name)
	return ok
}

// image maps a container image to packages or a runtime request; it
// reports whether it knew the image.
func (b *builder) image(src, svcName, image string) bool {
	name, tag := imageName(image)
	ver := leadingVersion(tag)
	if s, ok := serverFor(name); ok {
		b.addDeb(s.deb...)
		b.addApk(s.apk...)
		b.services[s.svc.name] = s.svc
		return true
	}
	switch name {
	case "python", "pypy":
		b.pythonWanted = true
		b.python.set(ver, false, prioImage, src)
	case "node":
		b.nodeWanted = true
		b.node.set(major(ver), false, prioImage, src)
	case "golang", "go":
		b.goWanted = true
		b.golang.set(ver, false, prioImage, src)
	case "rust":
		b.rustWanted = true
	case "openjdk", "eclipse-temurin", "amazoncorretto", "maven", "gradle":
		b.javaWanted = true
		b.java.set(major(ver), false, prioImage, src)
	case "ruby":
		b.rubyWanted = true
	case "alpine", "debian", "ubuntu", "busybox", "scratch":
	default:
		b.note("%s: service %q uses image %s, which has no package mapping; install it by hand", src, svcName, image)
		return false
	}
	return true
}
