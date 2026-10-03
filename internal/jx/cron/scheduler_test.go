package cron

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) set(t time.Time)     { c.mu.Lock(); c.t = t; c.mu.Unlock() }
func (c *fakeClock) add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

func newTestScheduler(t *testing.T, exec ExecFunc) (*Scheduler, *fakeClock) {
	t.Helper()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(st, exec)
	clk := &fakeClock{t: time.Date(2026, 10, 1, 10, 0, 30, 0, time.UTC)}
	s.SetClock(clk.now)
	s.SetLogf(t.Logf)
	return s, clk
}

func shellJob(sched string) Job {
	return Job{Name: "n", Schedule: sched, TZ: "UTC", Enabled: true, Kind: KindShell, Target: "vm1", Command: "true"}
}

func TestSchedulerFiresAtNextTime(t *testing.T) {
	var mu sync.Mutex
	var ran []string
	s, clk := newTestScheduler(t, func(ctx context.Context, j Job) Result {
		mu.Lock()
		ran = append(ran, j.ID)
		mu.Unlock()
		code := 0
		return Result{Output: "hi\n", ExitCode: &code}
	})
	j, err := s.Store().Put(shellJob("*/5 * * * *"))
	if err != nil {
		t.Fatal(err)
	}
	s.Changed(j.ID)
	if n, ok := s.Next(j); !ok || !n.Equal(time.Date(2026, 10, 1, 10, 5, 0, 0, time.UTC)) {
		t.Fatalf("next = %v %v", n, ok)
	}
	s.Tick(clk.now()) // not due
	clk.set(time.Date(2026, 10, 1, 10, 5, 0, 0, time.UTC))
	s.Tick(clk.now())
	s.Wait()
	s.Tick(clk.now()) // same minute: already fired, next is 10:10
	s.Wait()
	if len(ran) != 1 {
		t.Fatalf("ran %d times, want 1", len(ran))
	}
	runs := s.Store().Runs(j.ID)
	if len(runs) != 1 || runs[0].Status != StatusOK || runs[0].Output != "hi\n" || runs[0].EndedAt == nil || *runs[0].ExitCode != 0 {
		t.Fatalf("runs = %+v", runs)
	}
	// A daemon that was down misses runs; it does not catch up.
	clk.set(time.Date(2026, 10, 1, 12, 2, 0, 0, time.UTC))
	s.Tick(clk.now())
	s.Wait()
	if n, _ := s.Next(j); !n.Equal(time.Date(2026, 10, 1, 12, 5, 0, 0, time.UTC)) {
		t.Fatalf("next after gap = %v", n)
	}
	if len(ran) != 2 {
		t.Fatalf("ran %d times, want 2", len(ran))
	}
}

func TestSchedulerNoOverlap(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 4)
	s, clk := newTestScheduler(t, func(ctx context.Context, j Job) Result {
		started <- struct{}{}
		<-release
		return Result{}
	})
	j, _ := s.Store().Put(shellJob("* * * * *"))
	s.Changed(j.ID)
	clk.add(30 * time.Second)
	s.Tick(clk.now())
	<-started
	clk.add(time.Minute)
	s.Tick(clk.now()) // still running: skipped
	if _, err := s.RunNow(j.ID); !errors.Is(err, ErrRunning) {
		t.Fatalf("RunNow while running: %v", err)
	}
	close(release)
	s.Wait()
	runs := s.Store().Runs(j.ID)
	if len(runs) != 2 || runs[0].Status != StatusSkipped || runs[1].Status != StatusOK {
		t.Fatalf("runs = %+v", runs)
	}
	if !strings.Contains(runs[0].Error, "still running") {
		t.Fatalf("skip reason = %q", runs[0].Error)
	}
}

func TestSchedulerErrorsAndDisabled(t *testing.T) {
	s, clk := newTestScheduler(t, func(ctx context.Context, j Job) Result {
		code := 3
		return Result{ExitCode: &code, Err: errors.New("exit status 3")}
	})
	bad, _ := s.Store().Put(shellJob("@every 1m"))
	off := shellJob("@every 1m")
	off.Enabled = false
	off, _ = s.Store().Put(off)
	s.Changed(bad.ID)
	s.Changed(off.ID)
	if _, ok := s.Next(off); ok {
		t.Fatal("disabled job has a next run")
	}
	clk.add(time.Minute)
	s.Tick(clk.now())
	s.Wait()
	if r, ok := s.Store().LastRun(bad.ID); !ok || r.Status != StatusError || r.Error != "exit status 3" || *r.ExitCode != 3 {
		t.Fatalf("bad run = %+v", r)
	}
	if _, ok := s.Store().LastRun(off.ID); ok {
		t.Fatal("disabled job ran")
	}
}

func TestSchedulerTimeout(t *testing.T) {
	st, _ := Open(t.TempDir())
	s := NewScheduler(st, func(ctx context.Context, j Job) Result {
		ctx2, cancel := context.WithTimeout(ctx, 20*time.Millisecond) // stand-in for the job timeout
		defer cancel()
		<-ctx2.Done()
		return Result{Err: ctx2.Err()}
	})
	s.SetLogf(t.Logf)
	j, _ := st.Put(shellJob("@daily"))
	id, err := s.RunNow(j.ID)
	if err != nil || id == "" {
		t.Fatal(id, err)
	}
	s.Wait()
	r, _ := st.LastRun(j.ID)
	if r.Status != StatusError || r.Trigger != "manual" || r.ID != id {
		t.Fatalf("run = %+v", r)
	}
	if _, err := s.RunNow("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("RunNow unknown: %v", err)
	}
}

func TestStoreKeepsRunsAndPersists(t *testing.T) {
	dir := t.TempDir()
	st, _ := Open(dir)
	j, err := st.Put(shellJob("@hourly"))
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < KeepRuns+5; i++ {
		if err := st.SaveRun(j.ID, Run{ID: NewID(), StartedAt: base.Add(time.Duration(i) * time.Minute), Status: StatusOK}); err != nil {
			t.Fatal(err)
		}
	}
	running := Run{ID: "r-live", StartedAt: base.Add(time.Hour), Status: StatusRunning}
	st.SaveRun(j.ID, running)
	runs := st.Runs(j.ID)
	if len(runs) != KeepRuns || runs[0].ID != "r-live" {
		t.Fatalf("kept %d runs, newest %q", len(runs), runs[0].ID)
	}
	for _, name := range []string{"cron.json", filepath.Join("cron-runs", j.ID+".json")} {
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s mode %v", name, fi.Mode())
		}
	}
	// A fresh process sees the jobs; a run left "running" is interrupted.
	st2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := st2.Job(j.ID); !ok || got.Schedule != "@hourly" {
		t.Fatalf("reloaded job = %+v %v", got, ok)
	}
	if r, _ := st2.LastRun(j.ID); r.Status != StatusError || !strings.Contains(r.Error, "interrupted") {
		t.Fatalf("stale run = %+v", r)
	}
	if _, err := st2.Delete(j.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "cron-runs", j.ID+".json")); !os.IsNotExist(err) {
		t.Fatal("runs file survived delete")
	}
	if err := st2.SaveRun(j.ID, Run{ID: "late"}); err != nil || len(st2.Runs(j.ID)) != 0 {
		t.Fatal("a run of a deleted job was saved")
	}
}

func TestValidate(t *testing.T) {
	ok := []Job{
		shellJob("@daily"),
		{Schedule: "0 9 * * mon", Kind: KindBoard, Target: "host", Agent: "claude", Prompt: "p"},
		{Schedule: "@every 2h", Kind: KindBoard, ThreadID: "t1", Prompt: "p"},
	}
	for _, j := range ok {
		if err := j.Validate(); err != nil {
			t.Errorf("%+v: %v", j, err)
		}
		if j.Name == "" {
			t.Errorf("no default name for %+v", j)
		}
	}
	bad := []Job{
		{Schedule: "nope", Kind: KindShell, Target: "vm", Command: "x"},
		{Schedule: "@daily", TZ: "Mars/Olympus", Kind: KindShell, Target: "vm", Command: "x"},
		{Schedule: "@daily", Kind: KindShell, Target: "vm"},
		{Schedule: "@daily", Kind: KindShell, Target: "host", Command: "x"},
		{Schedule: "@daily", Kind: KindBoard, Target: "vm", Agent: "gpt", Prompt: "p"},
		{Schedule: "@daily", Kind: KindBoard, Target: "vm", Agent: "codex"},
		{Schedule: "@daily", Kind: "cron", Target: "vm"},
		{Schedule: "@daily", Kind: KindShell, Target: "vm", Command: "x", TimeoutMinutes: -1},
	}
	for _, j := range bad {
		if err := j.Validate(); err == nil {
			t.Errorf("%+v: want error", j)
		}
	}
}
