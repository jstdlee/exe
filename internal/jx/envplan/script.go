package envplan

import (
	"fmt"
	"strings"
)

// rootMarker ends the root part of the script (a quoted here-document).
const rootMarker = "EXE_ROOT"

// Script renders the plan as a POSIX sh bootstrap for distro (Debian or
// Alpine). It runs as the VM's user: the root part goes through sudo (or
// doas on Alpine), the user part runs in ~/work.
func Script(p Plan, distro string) (string, error) {
	if distro != Debian && distro != Alpine {
		return "", fmt.Errorf("unknown distro %q (use %s or %s)", distro, Debian, Alpine)
	}
	var root, user []Step
	for _, s := range p.Steps {
		if s.Distro != "" && s.Distro != distro {
			continue
		}
		if strings.Contains(s.Run, rootMarker) {
			return "", fmt.Errorf("step %q contains the reserved word %s", s.Name, rootMarker)
		}
		if s.Root {
			root = append(root, s)
		} else {
			user = append(user, s)
		}
	}

	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	fmt.Fprintf(&b, "# exe env bootstrap for %s", distro)
	if len(p.Sources) > 0 {
		fmt.Fprintf(&b, ", from: %s", strings.Join(p.Sources, ", "))
	}
	b.WriteString(`
# Run it as the VM's user: root steps go through sudo (or doas).
set -eu
mkdir -p "$HOME/work"
cd "$HOME/work"
if [ "$(id -u)" -eq 0 ]; then asroot() { sh "$@"; }
elif command -v sudo >/dev/null 2>&1; then asroot() { sudo -n sh "$@"; }
else asroot() { doas sh "$@"; }
fi

root_script=$(mktemp)
trap 'rm -f "$root_script"' EXIT
cat >"$root_script" <<'` + rootMarker + `'
set -eu
case "$(uname -m)" in
x86_64 | amd64) goarch=amd64 nodearch=x64 ;;
aarch64 | arm64) goarch=arm64 nodearch=arm64 ;;
*) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac
export goarch nodearch
`)
	if distro == Debian {
		b.WriteString(`export DEBIAN_FRONTEND=noninteractive
pkg_install() {
	apt-get install -y -q --no-install-recommends "$@" && return 0
	for p in "$@"; do
		apt-get install -y -q --no-install-recommends "$p" || echo "warning: package $p not installed" >&2
	done
}
echo "==> packages"
apt-get update -q
`)
	} else {
		b.WriteString(`pkg_install() {
	apk add --no-cache "$@" && return 0
	for p in "$@"; do
		apk add --no-cache "$p" || echo "warning: package $p not installed" >&2
	done
}
echo "==> packages"
apk update -q
`)
	}
	if pkgs := p.Packages[distro]; len(pkgs) > 0 {
		b.WriteString("pkg_install " + strings.Join(pkgs, " ") + "\n")
	}
	for _, s := range root {
		writeStep(&b, s)
	}
	b.WriteString(rootMarker + "\n")
	b.WriteString(`asroot "$root_script" </dev/null

export PATH="$HOME/work/.venv/bin:$HOME/.cargo/bin:$HOME/.local/bin:/usr/local/go/bin:/usr/local/bin:$PATH"
export COREPACK_ENABLE_DOWNLOAD_PROMPT=0
`)
	for _, s := range user {
		writeStep(&b, s)
	}
	b.WriteString("echo \"==> done\"\n")
	return b.String(), nil
}

func writeStep(b *strings.Builder, s Step) {
	fmt.Fprintf(b, "echo %s\n", shellWords([]string{"==> " + s.Name}))
	b.WriteString(strings.TrimRight(s.Run, "\n") + "\n")
}
