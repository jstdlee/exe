package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"exe/internal/jx/cron"
	"exe/internal/vmm"
)

// jxCronTestJob is a GET /v1/jx/cron entry.
type jxCronTestJob struct {
	cron.Job
	NextRun    *time.Time `json:"next_run"`
	LastRun    *time.Time `json:"last_run"`
	LastStatus string     `json:"last_status"`
}

func TestJXCronAPI(t *testing.T) {
	s, ts := newJXTestServer(t, newJXFakeVMs(vmm.Info{Name: "vm1", State: "stopped", MemoryMB: 1024}))

	for _, bad := range []string{
		`{"schedule": "61 * * * *", "kind": "shell", "target": "vm1", "command": "date"}`,
		`{"schedule": "@daily", "kind": "shell", "target": "vm1"}`,
		`{"schedule": "@daily", "kind": "board", "target": "vm1", "agent": "gpt", "prompt": "hi"}`,
		`{"schedule": "@daily", "tz": "Nowhere/Land", "kind": "shell", "target": "vm1", "command": "date"}`,
		`not json`,
	} {
		if c := jxDo(t, ts, "POST", "/v1/jx/cron", bad, nil); c != 400 {
			t.Errorf("POST %s: %d", bad, c)
		}
	}

	var job jxCronTestJob
	c := jxDo(t, ts, "POST", "/v1/jx/cron", `{"name": "nightly", "schedule": "30 2 * * *", "tz": "UTC", "kind": "shell", "target": "vm1", "command": "date"}`, &job)
	if c != 201 || job.ID == "" || !job.Enabled || job.NextRun == nil || job.NextRun.UTC().Hour() != 2 || job.NextRun.UTC().Minute() != 30 {
		t.Fatalf("create: %d %+v", c, job)
	}
	var list []jxCronTestJob
	if jxDo(t, ts, "GET", "/v1/jx/cron", nil, &list); len(list) != 1 || list[0].ID != job.ID {
		t.Fatalf("list %+v", list)
	}
	if c := jxDo(t, ts, "PUT", "/v1/jx/cron/"+job.ID, `{"enabled": false, "id": "evil"}`, &job); c != 200 || job.Enabled || job.NextRun != nil || job.Command != "date" || job.ID != list[0].ID {
		t.Fatalf("disable: %d %+v", c, job)
	}
	if c := jxDo(t, ts, "PUT", "/v1/jx/cron/nope", `{"enabled": true}`, nil); c != 404 {
		t.Fatalf("PUT unknown: %d", c)
	}

	// A board job with no Board in this build records the error.
	saved := jxBoardSubmit
	jxBoardSubmit = nil
	defer func() { jxBoardSubmit = saved }()
	var board jxCronTestJob
	jxDo(t, ts, "POST", "/v1/jx/cron", `{"schedule": "@hourly", "kind": "board", "target": "host", "agent": "claude", "prompt": "status?"}`, &board)
	if board.Name != "board on host" {
		t.Fatalf("default name %q", board.Name)
	}
	var started struct {
		RunID string `json:"run_id"`
	}
	if c := jxDo(t, ts, "POST", "/v1/jx/cron/"+board.ID+"/run", nil, &started); c != 202 || started.RunID == "" {
		t.Fatalf("run: %d %+v", c, started)
	}
	sc, _ := s.jxCron()
	sc.Wait()
	var runs []cron.Run
	jxDo(t, ts, "GET", "/v1/jx/cron/"+board.ID+"/runs", nil, &runs)
	if len(runs) != 1 || runs[0].ID != started.RunID || runs[0].Status != "error" || runs[0].Error != "board not available" {
		t.Fatalf("runs %+v", runs)
	}

	// With a Board: the run holds lease "cron" on its VM, brings it up
	// first, posts with origin cron:<id>, and never overlaps itself.
	release := make(chan struct{})
	var got BoardSubmitRequest
	jxBoardSubmit = func(s *Server, ctx context.Context, req BoardSubmitRequest) (string, string, error) {
		got = req
		<-release
		return "th1", "tu1", nil
	}
	savedUp := jxEnsureVMUp
	var heldDuringUp bool
	jxEnsureVMUp = func(s *Server, ctx context.Context, vm string) error {
		for _, h := range s.Leases().Get(vm).Holders {
			heldDuringUp = heldDuringUp || h.Reason == "cron"
		}
		return nil
	}
	defer func() { jxEnsureVMUp = savedUp }()
	var onVM jxCronTestJob
	jxDo(t, ts, "POST", "/v1/jx/cron", `{"name": "triage", "schedule": "@every 2h", "kind": "board", "target": "vm1", "agent": "codex", "prompt": "triage the inbox"}`, &onVM)
	if c := jxDo(t, ts, "POST", "/v1/jx/cron/"+onVM.ID+"/run", nil, &started); c != 202 {
		t.Fatalf("run: %d", c)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !sc.Running(onVM.ID) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if c := jxDo(t, ts, "POST", "/v1/jx/cron/"+onVM.ID+"/run", nil, nil); c != 409 {
		t.Fatalf("overlapping run: %d", c)
	}
	close(release)
	sc.Wait()
	if !heldDuringUp || got.Origin != "cron:"+onVM.ID || got.Agent != "codex" || got.Target != "vm1" || got.Prompt != "triage the inbox" {
		t.Fatalf("held=%v req=%+v", heldDuringUp, got)
	}
	if h := s.Leases().Get("vm1").Holders; len(h) != 0 {
		t.Fatalf("lease kept after the run: %+v", h)
	}
	jxDo(t, ts, "GET", "/v1/jx/cron/"+onVM.ID, nil, &onVM)
	if onVM.LastStatus != "ok" || onVM.LastRun == nil {
		t.Fatalf("job after run %+v", onVM)
	}
	jxDo(t, ts, "GET", "/v1/jx/cron/"+onVM.ID+"/runs", nil, &runs)
	if len(runs) != 1 || runs[0].ThreadID != "th1" {
		t.Fatalf("runs %+v", runs)
	}

	// A VM that cannot come up fails the run with that reason.
	jxEnsureVMUp = func(*Server, context.Context, string) error { return errors.New("no room") }
	jxDo(t, ts, "POST", "/v1/jx/cron/"+onVM.ID+"/run", nil, nil)
	sc.Wait()
	jxDo(t, ts, "GET", "/v1/jx/cron/"+onVM.ID+"/runs", nil, &runs)
	if runs[0].Status != "error" || runs[0].Error != "no room" {
		t.Fatalf("runs %+v", runs)
	}

	var deleted cron.Job
	if c := jxDo(t, ts, "DELETE", "/v1/jx/cron/"+job.ID, nil, &deleted); c != 200 || deleted.ID != job.ID {
		t.Fatalf("delete %d %+v", c, deleted)
	}
	for _, p := range []string{"/v1/jx/cron/" + job.ID, "/v1/jx/cron/" + job.ID + "/runs"} {
		if c := jxDo(t, ts, "GET", p, nil, nil); c != 404 {
			t.Errorf("GET %s after delete: %d", p, c)
		}
	}
	if c := jxDo(t, ts, "DELETE", "/v1/jx/cron/"+job.ID, nil, nil); c != 404 {
		t.Fatalf("delete twice: %d", c)
	}
	if c := jxDo(t, ts, "POST", "/v1/jx/cron/"+job.ID+"/run", nil, nil); c != 404 {
		t.Fatalf("run deleted: %d", c)
	}
}
