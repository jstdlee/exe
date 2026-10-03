package cron

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"
)

// Result is what running a job produced.
type Result struct {
	Output   string
	ExitCode *int
	ThreadID string
	Err      error
}

// ExecFunc runs one job; ctx carries the job's timeout.
type ExecFunc func(ctx context.Context, job Job) Result

// ErrRunning is returned by RunNow while the job's previous run goes on.
var ErrRunning = errors.New("the job is still running")

// Scheduler fires due jobs. Missed runs (the daemon was down) are not
// caught up: after a start, each job waits for its next time.
type Scheduler struct {
	store *Store
	exec  ExecFunc
	now   func() time.Time
	logf  func(format string, args ...any)

	mu      sync.Mutex
	next    map[string]time.Time // job id -> next run; absent = not computed
	running map[string]bool
	kick    chan struct{}
	wg      sync.WaitGroup
}

// NewScheduler returns a scheduler over store that runs jobs with exec.
func NewScheduler(store *Store, exec ExecFunc) *Scheduler {
	return &Scheduler{
		store: store, exec: exec, now: time.Now, logf: log.Printf,
		next: map[string]time.Time{}, running: map[string]bool{}, kick: make(chan struct{}, 1),
	}
}

// SetClock replaces time.Now (tests).
func (s *Scheduler) SetClock(now func() time.Time) { s.mu.Lock(); s.now = now; s.mu.Unlock() }

// SetLogf replaces log.Printf.
func (s *Scheduler) SetLogf(f func(format string, args ...any)) { s.logf = f }

// Store is the scheduler's job store.
func (s *Scheduler) Store() *Store { return s.store }

func (s *Scheduler) clock() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now()
}

// Tick fires every enabled job whose time has come at now, and computes
// the next time of jobs it has not seen yet (without firing them).
func (s *Scheduler) Tick(now time.Time) {
	for _, j := range s.store.Jobs() {
		if !j.Enabled {
			s.mu.Lock()
			delete(s.next, j.ID)
			s.mu.Unlock()
			continue
		}
		s.mu.Lock()
		n, seen := s.next[j.ID]
		s.mu.Unlock()
		if !seen {
			s.setNext(j, now)
			continue
		}
		if n.IsZero() || now.Before(n) {
			continue
		}
		s.setNext(j, now)
		s.fire(j, now, "schedule")
	}
}

func (s *Scheduler) setNext(j Job, after time.Time) {
	n, err := j.NextAfter(after)
	if err != nil {
		s.logf("cron %s: %v", j.ID, err)
	}
	s.mu.Lock()
	s.next[j.ID] = n
	s.mu.Unlock()
}

// Next returns a job's next run time, computing it if needed.
func (s *Scheduler) Next(j Job) (time.Time, bool) {
	if !j.Enabled {
		return time.Time{}, false
	}
	s.mu.Lock()
	n, ok := s.next[j.ID]
	s.mu.Unlock()
	if !ok {
		s.setNext(j, s.clock())
		s.mu.Lock()
		n = s.next[j.ID]
		s.mu.Unlock()
	}
	return n, !n.IsZero()
}

// Changed tells the scheduler a job was added, edited or deleted: its next
// time is recomputed from now.
func (s *Scheduler) Changed(id string) {
	s.mu.Lock()
	delete(s.next, id)
	s.mu.Unlock()
	if j, ok := s.store.Job(id); ok && j.Enabled {
		s.setNext(j, s.clock())
	}
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// RunNow starts a run of job id at once and returns the run id.
func (s *Scheduler) RunNow(id string) (string, error) {
	j, ok := s.store.Job(id)
	if !ok {
		return "", ErrNotFound
	}
	runID, err := s.fire(j, s.clock(), "manual")
	return runID, err
}

// fire starts a run of j in its own goroutine. A job never overlaps itself:
// a scheduled time that comes while the last run goes on is recorded as
// skipped; a manual run is refused with ErrRunning.
func (s *Scheduler) fire(j Job, now time.Time, trigger string) (string, error) {
	run := Run{ID: NewID(), Trigger: trigger, StartedAt: now.UTC(), Status: StatusRunning}
	s.mu.Lock()
	busy := s.running[j.ID]
	if !busy {
		s.running[j.ID] = true
	}
	s.mu.Unlock()
	if busy {
		if trigger == "manual" {
			return "", ErrRunning
		}
		end := now.UTC()
		run.Status, run.EndedAt, run.Error = StatusSkipped, &end, "skipped: still running"
		if err := s.store.SaveRun(j.ID, run); err != nil {
			s.logf("cron %s: %v", j.ID, err)
		}
		s.logf("cron %s (%s): skipped, the last run is still going", j.ID, j.Name)
		return run.ID, nil
	}
	if err := s.store.SaveRun(j.ID, run); err != nil {
		s.logf("cron %s: %v", j.ID, err)
	}
	s.logf("cron %s (%s): %s run %s", j.ID, j.Name, trigger, run.ID)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			s.mu.Lock()
			delete(s.running, j.ID)
			s.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), j.Timeout())
		res := s.exec(ctx, j)
		if res.Err == nil && ctx.Err() == context.DeadlineExceeded {
			res.Err = errors.New("timed out")
		}
		cancel()
		end := s.clock().UTC()
		run.EndedAt = &end
		run.Output, run.ExitCode, run.ThreadID = res.Output, res.ExitCode, res.ThreadID
		if len(run.Output) > MaxOutput {
			run.Output = run.Output[len(run.Output)-MaxOutput:]
		}
		run.Status = StatusOK
		if res.Err != nil {
			run.Status, run.Error = StatusError, res.Err.Error()
			s.logf("cron %s (%s): run %s failed: %v", j.ID, j.Name, run.ID, res.Err)
		}
		if err := s.store.SaveRun(j.ID, run); err != nil {
			s.logf("cron %s: %v", j.ID, err)
		}
	}()
	return run.ID, nil
}

// Running reports whether a run of job id goes on.
func (s *Scheduler) Running(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running[id]
}

// Wait blocks until every started run has ended (tests).
func (s *Scheduler) Wait() { s.wg.Wait() }

// Loop ticks until ctx ends: at each job's next time, at least once a
// minute (so a changed wall clock is noticed), and on Changed.
func (s *Scheduler) Loop(ctx context.Context) {
	for {
		now := s.clock()
		s.Tick(now)
		wait := time.Minute
		s.mu.Lock()
		for _, n := range s.next {
			if !n.IsZero() {
				if d := n.Sub(now); d < wait {
					wait = d
				}
			}
		}
		s.mu.Unlock()
		if wait < 50*time.Millisecond {
			wait = 50 * time.Millisecond
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		case <-s.kick:
			t.Stop()
		}
	}
}
