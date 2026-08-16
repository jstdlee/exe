package agentenv

import (
	"fmt"
	"strings"
)

// BootstrapScript is the guest-side shell that init writes and runs.
func BootstrapScript(f Flavor, m Manifest) string {
	var b strings.Builder
	b.WriteString("#!/bin/bash\nset -euo pipefail\n")
	b.WriteString("mkdir -p \"$HOME/work\" \"$HOME/.exe-env\"\n")
	fmt.Fprintf(&b, "cat > \"$HOME/.exe-env/FLAVOR\" <<'EOF'\n%s\nEOF\n", f.Name)
	fmt.Fprintf(&b, "cat > \"$HOME/BOOTSTRAP.md\" <<'EOF'\n# Environment bootstrap\n\n%s\n\n## Manifest\n\n%s\n\n## Init prompt\n\n%s\nEOF\n",
		f.Name, m.Prompt, strings.TrimSpace(f.InitPrompt))
	if len(m.Packages) > 0 {
		fmt.Fprintf(&b, "sudo DEBIAN_FRONTEND=noninteractive apt-get update -qq\n")
		fmt.Fprintf(&b, "sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -qq %s || true\n",
			strings.Join(m.Packages, " "))
	}
	if len(m.Python) > 0 {
		fmt.Fprintf(&b, "python3 -m pip install --user %s || true\n", strings.Join(m.Python, " "))
	}
	b.WriteString("echo bootstrap-ok\n")
	return b.String()
}
