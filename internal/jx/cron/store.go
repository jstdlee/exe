package cron

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Job kinds.
const (
	KindBoard = "board" // post a prompt to the Board
	KindShell = "shell" // run a command in the VM over SSH
)

// Run states.
const (
	StatusRunning = "running"
	StatusOK      = "ok"
	StatusError   = "error"
	StatusSkipped = "skipped"
)

const (
	// KeepRuns is how many runs per job the store keeps.
	KeepRuns = 20
	// DefaultTimeout bounds a run whose job sets no timeout.
	DefaultTimeout = time.Hour
	// MaxOutput is the most shell output a run keeps.
	MaxOutput = 64 << 10
)

// Job is one scheduled job.
type Job struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Schedule       string    `json:"schedule"`
	TZ             string    `json:"tz,omitempty"`
	Enabled        bool      `json:"enabled"`
	Kind           string    `json:"kind"`
	Target         string    `json:"target"`
	Agent          string    `json:"agent,omitempty"`
	ThreadID       string    `json:"thread_id,omitempty"`
	Prompt         string    `json:"prompt,omitempty"`
	Command        string    `json:"command,omitempty"`
	TimeoutMinutes int       `json:"timeout_minutes,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// Run is one execution of a job.
type Run struct {
	ID        string     `json:"id"`
	Trigger   string     `json:"trigger"` // "schedule" | "manual"
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	Status    string     `json:"status"` // running | ok | error | skipped
	Output    string     `json:"output,omitempty"`
	ExitCode  *int       `json:"exit_code,omitempty"`
	Error     string     `json:"error,omitempty"`
	ThreadID  string     `json:"thread_id,omitempty"`
}

// Timeout is the job's run timeout.
func (j Job) Timeout() time.Duration {
	if j.TimeoutMinutes > 0 {
		return time.Duration(j.TimeoutMinutes) * time.Minute
	}
	return DefaultTimeout
}

// Location is the job's time zone (local time when TZ is empty).
func (j Job) Location() (*time.Location, error) {
	if j.TZ == "" {
		return time.Local, nil
	}
	loc, err := time.LoadLocation(j.TZ)
	if err != nil {
		return nil, fmt.Errorf("tz: %v", err)
	}
	return loc, nil
}

// NextAfter returns the job's first run time after t; zero if never.
func (j Job) NextAfter(t time.Time) (time.Time, error) {
	sched, err := Parse(j.Schedule)
	if err != nil {
		return time.Time{}, err
	}
	loc, err := j.Location()
	if err != nil {
		return time.Time{}, err
	}
	return sched.Next(t.In(loc)), nil
}

// Validate checks a job and fills defaults (name).
func (j *Job) Validate() error {
	if _, err := Parse(j.Schedule); err != nil {
		return err
	}
	if _, err := j.Location(); err != nil {
		return err
	}
	if j.TimeoutMinutes < 0 {
		return errors.New("timeout_minutes must not be negative")
	}
	switch j.Kind {
	case KindShell:
		if strings.TrimSpace(j.Command) == "" {
			return errors.New("a shell job needs a command")
		}
		if j.Target == "" || j.Target == "host" {
			return errors.New("a shell job needs a VM target")
		}
	case KindBoard:
		if strings.TrimSpace(j.Prompt) == "" {
			return errors.New("a board job needs a prompt")
		}
		if j.ThreadID == "" {
			if j.Target == "" {
				return errors.New("a board job needs a target (VM name or host) or a thread_id")
			}
			if j.Agent != "claude" && j.Agent != "codex" {
				return errors.New(`a board job needs agent "claude" or "codex" (or a thread_id)`)
			}
		}
	default:
		return fmt.Errorf(`kind must be "board" or "shell", not %q`, j.Kind)
	}
	if strings.TrimSpace(j.Name) == "" {
		j.Name = j.Kind + " on " + j.Target
	}
	return nil
}

// NewID returns a random job or run id.
func NewID() string {
	var b [6]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func validID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

// ErrNotFound is returned for an unknown job id.
var ErrNotFound = errors.New("no such cron job")

// Store keeps jobs in <dir>/cron.json and each job's runs in
// <dir>/cron-runs/<id>.json. Safe for concurrent use.
type Store struct {
	dir  string
	mu   sync.Mutex
	jobs []Job
	runs map[string][]Run // loaded lazily, oldest first
}

// Open loads the store; a missing file is an empty store.
func Open(dir string) (*Store, error) {
	st := &Store{dir: dir, runs: map[string][]Run{}}
	b, err := os.ReadFile(st.jobsPath())
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &st.jobs); err != nil {
			return nil, fmt.Errorf("%s: %v", st.jobsPath(), err)
		}
	}
	return st, nil
}

func (st *Store) jobsPath() string          { return filepath.Join(st.dir, "cron.json") }
func (st *Store) runsPath(id string) string { return filepath.Join(st.dir, "cron-runs", id+".json") }

// Jobs returns every job, oldest first.
func (st *Store) Jobs() []Job {
	st.mu.Lock()
	defer st.mu.Unlock()
	return append([]Job(nil), st.jobs...)
}

// Job returns one job.
func (st *Store) Job(id string) (Job, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, j := range st.jobs {
		if j.ID == id {
			return j, true
		}
	}
	return Job{}, false
}

// Put adds or replaces a job (by ID; a new job gets one).
func (st *Store) Put(j Job) (Job, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if j.ID == "" {
		j.ID = NewID()
	} else if !validID(j.ID) {
		return Job{}, fmt.Errorf("bad job id %q", j.ID)
	}
	if j.CreatedAt.IsZero() {
		j.CreatedAt = time.Now().UTC()
	}
	jobs := append([]Job(nil), st.jobs...)
	found := false
	for i := range jobs {
		if jobs[i].ID == j.ID {
			jobs[i], found = j, true
		}
	}
	if !found {
		jobs = append(jobs, j)
	}
	if err := writeJSONAtomic(st.jobsPath(), jobs); err != nil {
		return Job{}, err
	}
	st.jobs = jobs
	return j, nil
}

// Delete removes a job and its runs.
func (st *Store) Delete(id string) (Job, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	var gone Job
	jobs := make([]Job, 0, len(st.jobs))
	for _, j := range st.jobs {
		if j.ID == id {
			gone = j
			continue
		}
		jobs = append(jobs, j)
	}
	if gone.ID == "" {
		return Job{}, ErrNotFound
	}
	if err := writeJSONAtomic(st.jobsPath(), jobs); err != nil {
		return Job{}, err
	}
	st.jobs = jobs
	delete(st.runs, id)
	os.Remove(st.runsPath(id))
	return gone, nil
}

func (st *Store) has(id string) bool {
	for _, j := range st.jobs {
		if j.ID == id {
			return true
		}
	}
	return false
}

// loadRuns reads a job's runs once per process. Runs a previous daemon
// left "running" can never finish now: they are marked interrupted.
func (st *Store) loadRuns(id string) []Run {
	if r, ok := st.runs[id]; ok {
		return r
	}
	var runs []Run
	if validID(id) {
		if b, err := os.ReadFile(st.runsPath(id)); err == nil {
			json.Unmarshal(b, &runs)
		}
	}
	for i := range runs {
		if runs[i].Status == StatusRunning {
			runs[i].Status = StatusError
			runs[i].Error = "interrupted: the daemon stopped during the run"
		}
	}
	st.runs[id] = runs
	return runs
}

// Runs returns a job's runs, newest first.
func (st *Store) Runs(id string) []Run {
	st.mu.Lock()
	defer st.mu.Unlock()
	runs := st.loadRuns(id)
	out := make([]Run, len(runs))
	for i, r := range runs {
		out[len(runs)-1-i] = r
	}
	return out
}

// LastRun returns a job's newest run.
func (st *Store) LastRun(id string) (Run, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	runs := st.loadRuns(id)
	if len(runs) == 0 {
		return Run{}, false
	}
	return runs[len(runs)-1], true
}

// SaveRun adds a run or replaces the one with the same ID, keeping the
// newest KeepRuns.
func (st *Store) SaveRun(jobID string, run Run) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if !validID(jobID) {
		return fmt.Errorf("bad job id %q", jobID)
	}
	if !st.has(jobID) {
		return nil // deleted while it ran
	}
	runs := append([]Run(nil), st.loadRuns(jobID)...)
	found := false
	for i := range runs {
		if runs[i].ID == run.ID {
			runs[i], found = run, true
		}
	}
	if !found {
		runs = append(runs, run)
	}
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].StartedAt.Before(runs[j].StartedAt) })
	if len(runs) > KeepRuns {
		runs = runs[len(runs)-KeepRuns:]
	}
	st.runs[jobID] = runs
	return writeJSONAtomic(st.runsPath(jobID), runs)
}

// writeJSONAtomic writes v to path through a temp file and a rename, 0600
// in a 0700 directory.
func writeJSONAtomic(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(append(b, '\n')) // CreateTemp made it 0600
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}
