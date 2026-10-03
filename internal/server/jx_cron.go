package server

// jx Feature B, part 2: scheduled jobs (internal/jx/cron). The scheduler
// loop starts lazily from this file's route registration and lives as long
// as the process. A run holds lease "cron" on its VM, starts the VM if
// needed (jxEnsureVMUp, memory guard included), then posts to the Board
// or runs a shell command over SSH.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"exe/internal/jx/cron"
)

func init() {
	jxFeatureRegs = append(jxFeatureRegs, func(s *Server, mux *http.ServeMux) {
		mux.HandleFunc("GET /v1/jx/cron", s.handleJXCronList)
		mux.HandleFunc("POST /v1/jx/cron", s.handleJXCronCreate)
		mux.HandleFunc("GET /v1/jx/cron/{id}", s.handleJXCronGet)
		mux.HandleFunc("PUT /v1/jx/cron/{id}", s.handleJXCronUpdate)
		mux.HandleFunc("DELETE /v1/jx/cron/{id}", s.handleJXCronDelete)
		mux.HandleFunc("POST /v1/jx/cron/{id}/run", s.handleJXCronRun)
		mux.HandleFunc("GET /v1/jx/cron/{id}/runs", s.handleJXCronRuns)
		s.jxCronStart()
	})
}

type jxCronRuntime struct {
	once     sync.Once
	sched    *cron.Scheduler
	err      error
	loopOnce sync.Once
}

var jxCronRTs sync.Map // *Server -> *jxCronRuntime

func (s *Server) jxCronRT() *jxCronRuntime {
	v, _ := jxCronRTs.LoadOrStore(s, &jxCronRuntime{})
	rt := v.(*jxCronRuntime)
	rt.once.Do(func() {
		store, err := cron.Open(s.jxDir())
		if err != nil {
			rt.err = fmt.Errorf("cron jobs: %w", err)
			log.Printf("jx: %v", rt.err)
			return
		}
		rt.sched = cron.NewScheduler(store, s.jxCronExec)
	})
	return rt
}

func (s *Server) jxCron() (*cron.Scheduler, error) {
	rt := s.jxCronRT()
	return rt.sched, rt.err
}

// jxCronStart starts the scheduler loop once per Server; it runs until the
// process exits.
func (s *Server) jxCronStart() {
	if s.StateDir == "" || !jxStartLoops {
		return
	}
	rt := s.jxCronRT()
	if rt.sched == nil {
		return
	}
	rt.loopOnce.Do(func() { go rt.sched.Loop(context.Background()) })
}

// jxCronExec runs one job; ctx carries the job's timeout.
func (s *Server) jxCronExec(ctx context.Context, job cron.Job) cron.Result {
	if job.Kind == cron.KindBoard && jxBoardSubmit == nil {
		return cron.Result{Err: errors.New("board not available")}
	}
	vm := job.Target
	if vm != "" && vm != "host" {
		defer s.JXHold(vm, "cron")()
		if jxEnsureVMUp == nil {
			return cron.Result{Err: errors.New("cannot start VMs (jxEnsureVMUp missing)")}
		}
		if err := jxEnsureVMUp(s, ctx, vm); err != nil {
			return cron.Result{Err: err}
		}
	}
	switch job.Kind {
	case cron.KindBoard:
		threadID, turnID, err := jxBoardSubmit(s, ctx, BoardSubmitRequest{
			ThreadID: job.ThreadID, Target: job.Target, Agent: job.Agent,
			Prompt: job.Prompt, Title: job.Name, Origin: "cron:" + job.ID,
		})
		if err != nil {
			return cron.Result{ThreadID: threadID, Err: err}
		}
		return cron.Result{ThreadID: threadID, Output: fmt.Sprintf("posted turn %s to thread %s", turnID, threadID)}
	case cron.KindShell:
		info, err := s.runningVM(ctx, vm)
		if err != nil {
			return cron.Result{Err: err}
		}
		out, code, err := s.vmTarget(info).Run(ctx, job.Command, cron.MaxOutput)
		res := cron.Result{Output: out}
		if code >= 0 {
			res.ExitCode = &code
		}
		switch {
		case err != nil:
			res.Err = err
		case code != 0:
			res.Err = fmt.Errorf("exit status %d", code)
		}
		return res
	}
	return cron.Result{Err: fmt.Errorf("unknown job kind %q", job.Kind)}
}

// jxCronJobView is a Job as the API shows it.
type jxCronJobView struct {
	cron.Job
	NextRun    *time.Time `json:"next_run"`
	LastRun    *time.Time `json:"last_run"`
	LastStatus string     `json:"last_status,omitempty"`
	Running    bool       `json:"running"`
}

func jxCronView(sc *cron.Scheduler, j cron.Job) jxCronJobView {
	v := jxCronJobView{Job: j, Running: sc.Running(j.ID)}
	if n, ok := sc.Next(j); ok {
		v.NextRun = &n
	}
	if r, ok := sc.Store().LastRun(j.ID); ok {
		at := r.StartedAt
		v.LastRun, v.LastStatus = &at, r.Status
	}
	return v
}

func (s *Server) jxCronOr500(w http.ResponseWriter) *cron.Scheduler {
	sc, err := s.jxCron()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return nil
	}
	return sc
}

func (s *Server) handleJXCronList(w http.ResponseWriter, r *http.Request) {
	sc := s.jxCronOr500(w)
	if sc == nil {
		return
	}
	out := []jxCronJobView{}
	for _, j := range sc.Store().Jobs() {
		out = append(out, jxCronView(sc, j))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleJXCronGet(w http.ResponseWriter, r *http.Request) {
	sc := s.jxCronOr500(w)
	if sc == nil {
		return
	}
	j, ok := sc.Store().Job(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, cron.ErrNotFound)
		return
	}
	writeJSON(w, http.StatusOK, jxCronView(sc, j))
}

// POST /v1/jx/cron: a new job; "enabled" defaults to true.
func (s *Server) handleJXCronCreate(w http.ResponseWriter, r *http.Request) {
	sc := s.jxCronOr500(w)
	if sc == nil {
		return
	}
	j := cron.Job{Enabled: true}
	if err := json.NewDecoder(r.Body).Decode(&j); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	j.ID, j.CreatedAt = "", time.Time{}
	s.jxCronSave(w, sc, j, http.StatusCreated)
}

// PUT /v1/jx/cron/{id}: the fields given replace the job's.
func (s *Server) handleJXCronUpdate(w http.ResponseWriter, r *http.Request) {
	sc := s.jxCronOr500(w)
	if sc == nil {
		return
	}
	old, ok := sc.Store().Job(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, cron.ErrNotFound)
		return
	}
	j := old
	if err := json.NewDecoder(r.Body).Decode(&j); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	j.ID, j.CreatedAt = old.ID, old.CreatedAt
	s.jxCronSave(w, sc, j, http.StatusOK)
}

func (s *Server) jxCronSave(w http.ResponseWriter, sc *cron.Scheduler, j cron.Job, code int) {
	if err := j.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	j, err := sc.Store().Put(j)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	sc.Changed(j.ID)
	writeJSON(w, code, jxCronView(sc, j))
}

func (s *Server) handleJXCronDelete(w http.ResponseWriter, r *http.Request) {
	sc := s.jxCronOr500(w)
	if sc == nil {
		return
	}
	j, err := sc.Store().Delete(r.PathValue("id"))
	if err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, cron.ErrNotFound) {
			code = http.StatusNotFound
		}
		writeErr(w, code, err)
		return
	}
	sc.Changed(j.ID)
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) handleJXCronRun(w http.ResponseWriter, r *http.Request) {
	sc := s.jxCronOr500(w)
	if sc == nil {
		return
	}
	runID, err := sc.RunNow(r.PathValue("id"))
	switch {
	case errors.Is(err, cron.ErrNotFound):
		writeErr(w, http.StatusNotFound, err)
	case errors.Is(err, cron.ErrRunning):
		writeErr(w, http.StatusConflict, err)
	case err != nil:
		writeErr(w, http.StatusInternalServerError, err)
	default:
		writeJSON(w, http.StatusAccepted, map[string]string{"run_id": runID})
	}
}

func (s *Server) handleJXCronRuns(w http.ResponseWriter, r *http.Request) {
	sc := s.jxCronOr500(w)
	if sc == nil {
		return
	}
	id := r.PathValue("id")
	if _, ok := sc.Store().Job(id); !ok {
		writeErr(w, http.StatusNotFound, cron.ErrNotFound)
		return
	}
	runs := sc.Store().Runs(id)
	if runs == nil {
		runs = []cron.Run{}
	}
	writeJSON(w, http.StatusOK, runs)
}
