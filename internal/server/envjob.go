package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"exe/internal/agentenv"
	"exe/internal/sshexec"
	"exe/internal/vmm"
)

func (s *Server) registerEnvRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/env/init", s.handleEnvInit)
	mux.HandleFunc("GET /v1/env/flavors", s.handleEnvFlavors)
	mux.HandleFunc("POST /v1/vms/{name}/jobs", s.handleEnvJob)
	mux.HandleFunc("GET /v1/vms/{name}/jobs/{id}", s.handleEnvJobGet)
	mux.HandleFunc("GET /v1/vms/{name}/snaps", s.handleSnapList)
	mux.HandleFunc("POST /v1/vms/{name}/snaps", s.handleSnapCreate)
	mux.HandleFunc("POST /v1/vms/{name}/snaps/{id}/restore", s.handleSnapRestore)
	mux.HandleFunc("DELETE /v1/vms/{name}/snaps/{id}", s.handleSnapDelete)
	mux.HandleFunc("GET /dl/{token}", s.handleDelivery)
}

func (s *Server) handleEnvFlavors(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, []agentenv.Flavor{agentenv.Debian()})
}

type envInitReq struct {
	Name      string `json:"name"`
	Flavor    string `json:"flavor"`
	From      []struct {
		Name string `json:"name"`
		Text string `json:"text"`
	} `json:"from"`
	CPUs     int `json:"cpus"`
	MemoryMB int `json:"memory_mb"`
	DiskGB   int `json:"disk_gb"`
}

func (s *Server) handleEnvInit(w http.ResponseWriter, r *http.Request) {
	var req envInitReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := vmm.ValidateName(req.Name); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	fl, err := agentenv.LoadFlavor(req.Flavor)
	if err != nil {
		fl = agentenv.Debian()
	}
	var ms []agentenv.Manifest
	for _, f := range req.From {
		ms = append(ms, agentenv.ParseManifest(f.Name, f.Text))
	}
	plan := agentenv.MergeManifests(ms)
	spec := vmm.Spec{Name: req.Name, CPUs: req.CPUs, MemoryMB: req.MemoryMB, DiskGB: req.DiskGB}
	if spec.CPUs == 0 {
		spec.CPUs = fl.CPUs
	}
	if spec.MemoryMB == 0 {
		spec.MemoryMB = fl.MemoryMB
	}
	if spec.DiskGB == 0 {
		spec.DiskGB = fl.DiskGB
	}
	s.fillSpec(&spec)

	info, err := s.VMs.Get(r.Context(), req.Name)
	if err != nil {
		info, err = s.VMs.Create(r.Context(), spec)
		if err != nil {
			writeErr(w, errCode(err), err)
			return
		}
	} else if info.State != "running" {
		info, err = s.VMs.Start(r.Context(), req.Name)
		if err != nil {
			writeErr(w, errCode(err), err)
			return
		}
	}
	info, err = s.runningVM(r.Context(), req.Name)
	if err != nil {
		writeErr(w, errCode(err), err)
		return
	}
	tgt := s.vmTarget(info)
	script := agentenv.BootstrapScript(fl, plan)
	if err := tgt.WriteFile(r.Context(), "/home/"+s.Config().SSHUser+"/bootstrap.sh", []byte(script)); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	out, code, err := tgt.Run(r.Context(), "bash ~/bootstrap.sh", 200_000)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"vm": info, "flavor": fl.Name, "manifest": plan, "bootstrap_exit": code, "bootstrap_log": out,
	})
}

type envJobReq struct {
	Cmd     string `json:"cmd"`
	Script  string `json:"script"`
	Prompt  string `json:"prompt"`
	Session string `json:"session"`
	Files   []struct {
		Name    string `json:"name"`
		Content string `json:"content"` // base64
	} `json:"files"`
}

func (s *Server) handleEnvJob(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	info, err := s.runningVM(r.Context(), name)
	if err != nil {
		writeErr(w, errCode(err), err)
		return
	}
	var req envJobReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 12<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(req.Cmd+req.Script+req.Prompt) == "" && len(req.Files) == 0 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("cmd, script, prompt or files required"))
		return
	}
	sessID := req.Session
	if sessID == "" {
		sessID = agentenv.NewJobID()
	}
	sess, err := agentenv.LoadSession(s.StateDir, name, sessID)
	if err != nil {
		sess = &agentenv.Session{ID: sessID, VM: name}
	}
	jobID := agentenv.NewJobID()
	tgt := s.vmTarget(info)
	guestDir := "/tmp/exe-job-" + jobID
	if _, _, err := tgt.Run(r.Context(), "mkdir -p "+sshexec.Quote(guestDir+"/in")+" "+sshexec.Quote(guestDir+"/out"), 4096); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	for _, f := range req.Files {
		raw, err := base64.StdEncoding.DecodeString(f.Content)
		if err != nil {
			raw = []byte(f.Content)
		}
		base := filepath.Base(f.Name)
		if base == "" || base == "." || base == "/" {
			continue
		}
		if err := tgt.WriteFile(r.Context(), guestDir+"/in/"+base, raw); err != nil {
			writeErr(w, http.StatusBadGateway, err)
			return
		}
	}
	if req.Script != "" {
		if err := tgt.WriteFile(r.Context(), guestDir+"/run.sh", []byte(req.Script)); err != nil {
			writeErr(w, http.StatusBadGateway, err)
			return
		}
	}
	cmd := strings.TrimSpace(req.Cmd)
	if req.Script != "" {
		cmd = "chmod +x " + sshexec.Quote(guestDir+"/run.sh") + " && bash " + sshexec.Quote(guestDir+"/run.sh")
	}
	if cmd == "" {
		cmd = "true"
	}
	cmd = "cd " + sshexec.Quote(guestDir+"/in") + " && " + cmd
	s.touchVM(name)
	shellOut, exit, err := tgt.Run(r.Context(), cmd, 400_000)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	agentOut := ""
	if p := strings.TrimSpace(req.Prompt); p != "" {
		if err := tgt.WriteFile(r.Context(), guestDir+"/PROMPT", []byte(p)); err == nil {
			wrap := `p=$(cat ` + sshexec.Quote(guestDir+"/PROMPT") + `)
if command -v claude >/dev/null 2>&1; then claude -p "$p"
elif command -v opencode >/dev/null 2>&1; then opencode run "$p"
elif command -v pi >/dev/null 2>&1; then pi "$p"
elif command -v codex >/dev/null 2>&1; then codex exec "$p"
else echo "no named agent on PATH (install claude/opencode/pi/codex in this VM)"
fi`
			agentOut, _, _ = tgt.Run(r.Context(), wrap, 200_000)
		}
	}
	hostOut := filepath.Join(s.StateDir, "vms", name, "jobs", jobID+"-shell.txt")
	os.MkdirAll(filepath.Dir(hostOut), 0o755)
	os.WriteFile(hostOut, []byte(shellOut), 0o600)
	tok, err := agentenv.IssueToken(s.StateDir, hostOut, jobID+"-shell.txt", 24*time.Hour)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	listen := s.Config().Listen
	if strings.HasPrefix(listen, ":") {
		listen = "127.0.0.1" + listen
	}
	dl := "http://" + listen + "/dl/" + tok.ID
	turn := agentenv.Turn{
		JobID:       jobID,
		ShellOutput: shellOut,
		AgentOutput: agentOut,
		ExitCode:    exit,
		Downloads:   []string{dl},
		CreatedAt:   time.Now().UTC(),
	}
	if err := agentenv.AppendTurn(s.StateDir, sess, turn); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session": sess.ID, "job": jobID, "exit_code": exit,
		"shell_output": shellOut, "agent_output": agentOut,
		"downloads": []map[string]string{{"label": tok.Label, "url": dl, "token": tok.ID}},
	})
}

func (s *Server) handleEnvJobGet(w http.ResponseWriter, r *http.Request) {
	sess, err := agentenv.LoadSession(s.StateDir, r.PathValue("name"), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func (s *Server) handleSnapList(w http.ResponseWriter, r *http.Request) {
	list, err := agentenv.ListSnaps(s.StateDir, r.PathValue("name"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleSnapCreate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req struct {
		Label string `json:"label"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if _, err := os.Stat(agentenv.DiskPath(s.StateDir, name)); err != nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("no disk for vm %s", name))
		return
	}
	info, err := s.VMs.Get(r.Context(), name)
	if err != nil {
		writeErr(w, errCode(err), err)
		return
	}
	if info.State == "running" {
		if err := s.VMs.Stop(r.Context(), name); err != nil {
			writeErr(w, errCode(err), err)
			return
		}
	}
	snap, err := agentenv.CreateSnap(s.StateDir, name, req.Label)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, snap)
}

func (s *Server) handleSnapRestore(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, err := agentenv.SnapDisk(s.StateDir, name, r.PathValue("id")); err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	info, err := s.VMs.Get(r.Context(), name)
	if err != nil {
		writeErr(w, errCode(err), err)
		return
	}
	if info.State == "running" {
		if err := s.VMs.Stop(r.Context(), name); err != nil {
			writeErr(w, errCode(err), err)
			return
		}
	}
	if err := agentenv.RestoreSnap(s.StateDir, name, r.PathValue("id")); err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "restored", "id": r.PathValue("id")})
}

func (s *Server) handleSnapDelete(w http.ResponseWriter, r *http.Request) {
	if err := agentenv.DeleteSnap(s.StateDir, r.PathValue("name"), r.PathValue("id")); err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleDelivery(w http.ResponseWriter, r *http.Request) {
	tok, err := agentenv.RedeemToken(s.StateDir, r.PathValue("token"))
	if err != nil {
		writeErr(w, http.StatusGone, err)
		return
	}
	f, err := os.Open(tok.Path)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+tok.Label+`"`)
	io.Copy(w, f)
}
