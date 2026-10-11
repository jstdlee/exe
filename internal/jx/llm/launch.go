package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Agent is a launchable agent CLI and what it can run on.
type Agent struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Kinds are the provider APIs it can use, preferred first.
	Kinds []string `json:"kinds"`
	// OwnThinking: a thinking level applies on the agent's own sign-in
	// too (it has a flag or variable for it).
	OwnThinking bool `json:"own_thinking"`
}

// Agents are the CLIs the launch dialog knows, by id (the Tools ids).
var Agents = map[string]Agent{
	"claude":   {ID: "claude", Name: "Claude Code", Kinds: []string{Anthropic}, OwnThinking: true},
	"codex":    {ID: "codex", Name: "Codex", Kinds: []string{OpenAI}, OwnThinking: true},
	"opencode": {ID: "opencode", Name: "OpenCode", Kinds: []string{OpenAI, Anthropic}},
	"pi":       {ID: "pi", Name: "Pi", Kinds: []string{OpenAI, Anthropic}, OwnThinking: true},
	"omp":      {ID: "omp", Name: "Oh My Pi", Kinds: []string{OpenAI, Anthropic}, OwnThinking: true},
	"grok":     {ID: "grok", Name: "Grok Build", Kinds: []string{OpenAI, Anthropic}, OwnThinking: true},
}

// AgentList is Agents sorted by id, for the API.
func AgentList() []Agent {
	out := make([]Agent, 0, len(Agents))
	for _, a := range Agents {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Levels are the thinking levels, lowest first; "" is the default.
var Levels = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}

func validLevel(l string) bool {
	if l == "" {
		return true
	}
	for _, v := range Levels {
		if v == l {
			return true
		}
	}
	return false
}

// API picks the provider API agent a uses on p, "" when they share none.
func (a Agent) API(p Provider) string {
	for _, k := range a.Kinds {
		if p.Speaks(k) {
			return k
		}
	}
	return ""
}

// Spec is one launch. Provider nil runs the agent on its own sign-in; its
// BaseURL is the one the guest uses (after a forward, if any). Model holds
// the chosen model's limits when the list gave them.
type Spec struct {
	Agent    string
	Provider *Provider
	Model    Model
	Thinking string
}

// KeyVar is the variable the guest's agent reads the provider key from.
const KeyVar = "EXE_LLM_API_KEY"

// Script is the guest sh script a terminal sources (with `.`) to start the
// agent: it exports the key, writes the agent's provider entry into its own
// config, and execs the CLI. It removes itself first: path is where the
// daemon put it, as a shell word ("$HOME/..."), so the key does not stay
// on the guest's disk.
func Script(s Spec, path string) (string, error) {
	a, ok := Agents[s.Agent]
	if !ok {
		return "", fmt.Errorf("unknown agent %q", s.Agent)
	}
	if !validLevel(s.Thinking) {
		return "", fmt.Errorf("unknown thinking level %q", s.Thinking)
	}
	var b strings.Builder
	b.WriteString("rm -f " + path + "\n")
	if s.Provider == nil {
		b.WriteString(ownRun(a, s.Thinking))
		return b.String(), nil
	}
	p := *s.Provider
	api := a.API(p)
	if api == "" {
		return "", fmt.Errorf("%s cannot use %s: it needs an %s-compatible API", a.Name, p.Name, kindName(a.Kinds[0]))
	}
	m := s.Model
	if m.ID == "" {
		m.ID = p.Model
	}
	if m.ID == "" {
		return "", fmt.Errorf("pick a model of %s for %s", p.Name, a.Name)
	}
	if strings.ContainsAny(m.ID, "\n\r\x00") {
		return "", errors.New("a model id is one line")
	}
	key := p.APIKey
	if key == "" {
		key = "exe-none" // some CLIs refuse an empty key; a keyless endpoint ignores it
	}
	b.WriteString("export " + KeyVar + "=" + shq(key) + "\n")
	run, err := providerRun(a, p, api, m, s.Thinking)
	if err != nil {
		return "", err
	}
	b.WriteString(run)
	return b.String(), nil
}

func kindName(k string) string {
	if k == Anthropic {
		return "Anthropic"
	}
	return "OpenAI"
}

// ---- own sign-in ---------------------------------------------------------

func ownRun(a Agent, level string) string {
	switch a.ID {
	case "claude":
		if t := claudeTokens(level); t != "" {
			return "export MAX_THINKING_TOKENS=" + t + "\nexec claude\n"
		}
		return "exec claude\n"
	case "codex":
		if level != "" {
			return "exec codex -c " + shq("model_reasoning_effort="+tomlStr(codexEffort(level))) + "\n"
		}
		return "exec codex\n"
	case "pi", "omp":
		if level != "" {
			return "exec " + a.ID + " --thinking " + shq(level) + "\n"
		}
	case "grok":
		if level != "" {
			return "exec grok --reasoning-effort " + shq(effortNone(level)) + "\n"
		}
	}
	return "exec " + a.ID + "\n"
}

// claudeTokens is Claude Code's thinking budget for a level.
func claudeTokens(level string) string {
	return map[string]string{"off": "0", "minimal": "2048", "low": "4096", "medium": "10000",
		"high": "31999", "xhigh": "48000", "max": "63999"}[level]
}

func codexEffort(level string) string { return effortNone(level) }

// effortNone names "off" the way OpenAI's reasoning_effort does.
func effortNone(level string) string {
	if level == "off" {
		return "none"
	}
	return level
}

// ---- on a provider -------------------------------------------------------

const (
	beginMark = "# >>> exe provider (written by exe at each launch)"
	endMark   = "# <<< exe provider"
)

func providerRun(a Agent, p Provider, api string, m Model, level string) (string, error) {
	var b strings.Builder
	switch a.ID {
	case "claude":
		b.WriteString("export ANTHROPIC_BASE_URL=" + shq(p.AnthropicRoot()) + "\n")
		b.WriteString("export ANTHROPIC_AUTH_TOKEN=\"$" + KeyVar + "\"\n")
		b.WriteString("unset ANTHROPIC_API_KEY CLAUDE_CODE_OAUTH_TOKEN\n")
		for _, v := range []string{"ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL",
			"ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_SMALL_FAST_MODEL", "CLAUDE_CODE_SUBAGENT_MODEL"} {
			b.WriteString("export " + v + "=" + shq(m.ID) + "\n")
		}
		if t := claudeTokens(level); t != "" {
			b.WriteString("export MAX_THINKING_TOKENS=" + t + "\n")
		}
		b.WriteString("exec claude\n")

	case "codex":
		args := []string{
			"model_providers.exe.name=" + tomlStr(p.Name),
			"model_providers.exe.base_url=" + tomlStr(p.OpenAIBase()),
			"model_providers.exe.env_key=" + tomlStr(KeyVar),
			"model_providers.exe.wire_api=" + tomlStr("responses"),
			"model_provider=" + tomlStr("exe"),
		}
		if level != "" {
			args = append(args, "model_reasoning_effort="+tomlStr(codexEffort(level)))
		}
		b.WriteString("exec codex")
		for _, x := range args {
			b.WriteString(" -c " + shq(x))
		}
		b.WriteString(" -m " + shq(m.ID) + "\n")

	case "opencode":
		npm, base, opts := "@ai-sdk/openai-compatible", p.OpenAIBase(), map[string]any{}
		if api == Anthropic {
			npm, base = "@ai-sdk/anthropic", p.AnthropicRoot()+"/v1"
			if t := claudeTokens(level); t != "" && level != "off" {
				n := 0
				fmt.Sscan(t, &n)
				opts["thinking"] = map[string]any{"type": "enabled", "budgetTokens": n}
			}
		} else if level != "" {
			opts["reasoningEffort"] = effortNone(level)
		}
		model := map[string]any{"name": m.ID}
		if len(opts) > 0 {
			model["options"] = opts
		}
		if lim := limits(m); lim != nil {
			model["limit"] = map[string]int{"context": lim[0], "output": lim[1]}
		}
		cfg := map[string]any{
			"$schema": "https://opencode.ai/config.json",
			"provider": map[string]any{"exe": map[string]any{
				"npm": npm, "name": p.Name,
				"options": map[string]any{"baseURL": base, "apiKey": "{env:" + KeyVar + "}"},
				"models":  map[string]any{m.ID: model},
			}},
			"model": "exe/" + m.ID,
		}
		b.WriteString("export OPENCODE_CONFIG_CONTENT=" + shq(jsonText(cfg)) + "\n")
		b.WriteString("exec opencode -m " + shq("exe/"+m.ID) + "\n")

	case "pi", "omp":
		entry := piEntry(p, api, m, level)
		if a.ID == "pi" {
			// pi's models.json is JSON: node (pi runs on it) sets one key
			b.WriteString(`f="$HOME/.pi/agent/models.json"; mkdir -p "$(dirname "$f")"` + "\n")
			b.WriteString("EXE_PI_ENTRY=" + shq(entry) + " node -e " + shq(piMerge) + ` "$f" || exit 1` + "\n")
		} else {
			// omp's models.yml is YAML, which takes the entry as a JSON flow
			// mapping: one marked line under providers:
			b.WriteString(`f="$HOME/.omp/agent/models.yml"; mkdir -p "$(dirname "$f")"; touch "$f"` + "\n")
			// the values ride the environment: awk -v would rewrite backslashes
			b.WriteString("EXE_E=" + shq("exe: "+entry) + " EXE_B=" + shq(beginMark) + " EXE_Z=" + shq(endMark) + " awk " + shq(ompAwk) +
				` "$f" > "$f.exe" && mv -f "$f.exe" "$f" || exit 1` + "\n")
		}
		b.WriteString("exec " + a.ID + " --provider exe --model " + shq(m.ID))
		if level != "" {
			b.WriteString(" --thinking " + shq(level))
		}
		b.WriteString("\n")

	case "grok":
		backend, base := "chat_completions", p.OpenAIBase()
		if api == Anthropic {
			backend, base = "messages", p.AnthropicRoot()+"/v1"
		}
		lines := []string{beginMark, "[model.exe]",
			"model = " + tomlStr(m.ID),
			"base_url = " + tomlStr(base),
			"name = " + tomlStr(p.Name+": "+m.ID),
			"env_key = " + tomlStr(KeyVar),
			"api_backend = " + tomlStr(backend)}
		if lim := limits(m); lim != nil {
			lines = append(lines, fmt.Sprintf("context_window = %d", lim[0]))
		}
		lines = append(lines, endMark)
		b.WriteString(`f="$HOME/.grok/config.toml"; mkdir -p "$(dirname "$f")"; touch "$f"` + "\n")
		b.WriteString("{ sed " + shq("/^"+sedRe(beginMark)+"$/,/^"+sedRe(endMark)+"$/d") + ` "$f"; printf '%s\n' ` + shq(strings.Join(lines, "\n")) +
			`; } > "$f.exe" && mv -f "$f.exe" "$f" || exit 1` + "\n")
		b.WriteString("exec grok -m exe")
		if level != "" {
			b.WriteString(" --reasoning-effort " + shq(effortNone(level)))
		}
		b.WriteString("\n")
	default:
		return "", fmt.Errorf("%s has no provider launch", a.Name)
	}
	return b.String(), nil
}

// limits is [context, output] for a model whose list gave them; the output
// is capped, because some lists give the API's ceiling rather than a
// useful reply size, and a CLI that asks for all of it gets refused.
func limits(m Model) []int {
	if m.Context <= 0 {
		return nil
	}
	out := m.MaxOut
	if out <= 0 || out > 32768 {
		out = 32768
	}
	if out > m.Context {
		out = m.Context
	}
	return []int{m.Context, out}
}

// piEntry is the provider entry pi and omp read: the key comes from the
// variable named in apiKey.
func piEntry(p Provider, api string, m Model, level string) string {
	kind, base := "openai-completions", p.OpenAIBase()
	if api == Anthropic {
		kind, base = "anthropic-messages", p.AnthropicRoot()
	}
	model := map[string]any{"id": m.ID, "name": m.ID, "reasoning": level != "" && level != "off"}
	ctx, out := 128000, 32768
	if lim := limits(m); lim != nil {
		ctx, out = lim[0], lim[1]
	}
	model["contextWindow"], model["maxTokens"] = ctx, out
	return jsonText(map[string]any{"baseUrl": base, "api": kind, "apiKey": KeyVar, "models": []any{model}})
}

// piMerge sets providers.exe in pi's models.json and keeps the rest. A
// file it cannot parse is left alone: it is the person's own.
const piMerge = `const fs = require("fs"), f = process.argv[1];
let d = {};
if (fs.existsSync(f)) {
  const t = fs.readFileSync(f, "utf8");
  if (t.trim()) { try { d = JSON.parse(t); } catch (e) { console.error("exe: " + f + " is not valid JSON; fix it or move it away"); process.exit(1); } }
}
d.providers = d.providers || {};
d.providers.exe = JSON.parse(process.env.EXE_PI_ENTRY);
fs.writeFileSync(f + ".exe", JSON.stringify(d, null, 2) + "\n");
fs.renameSync(f + ".exe", f);`

// ompAwk puts the marked entry line e first under the top-level
// providers: key of omp's models.yml (adding the key when missing) at the
// indentation the file's other providers use, dropping an older marked
// block.
const ompAwk = `BEGIN { e = ENVIRON["EXE_E"]; b = ENVIRON["EXE_B"]; z = ENVIRON["EXE_Z"] }
index($0, b) { skip = 1; next }
index($0, z) { skip = 0; next }
skip { next }
{ l[++n] = $0 }
END {
  for (i = 1; i <= n; i++) if (l[i] ~ /^providers:[ \t]*(\{[ \t]*\}|null|~)?[ \t]*$/) { p = i; break }
  ind = "  "
  if (p) for (i = p + 1; i <= n; i++) if (l[i] !~ /^[ \t]*(#.*)?$/) { if (match(l[i], /^ +/)) ind = substr(l[i], 1, RLENGTH); break }
  for (i = 1; i <= n; i++) {
    if (i == p) { print "providers:"; print ind b; print ind e; print ind z } else print l[i]
  }
  if (!p) { print "providers:"; print ind b; print ind e; print ind z }
}`

// tomlStr is a TOML basic string: JSON's string escapes are TOML's.
func tomlStr(s string) string { return jsonText(s) }

// jsonText is v as compact JSON, with & < > left as they are.
func jsonText(v any) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
	return strings.TrimSuffix(b.String(), "\n")
}

// sedRe escapes s for a basic regular expression.
func sedRe(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`\.*[]^$/`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
