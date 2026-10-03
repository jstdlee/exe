// Package cron holds the jx scheduled jobs: a small cron-expression parser,
// the job and run store, and the scheduler loop. It knows nothing about VMs
// or the Board: the server hands it an ExecFunc that runs one job.
package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule computes run times.
type Schedule interface {
	// Next returns the first run time strictly after t, in t's location,
	// or the zero time when the schedule never fires again.
	Next(t time.Time) time.Time
}

// MinEvery is the shortest @every interval.
const MinEvery = time.Minute

var shortcuts = map[string]string{
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
	"@monthly":  "0 0 1 * *",
	"@weekly":   "0 0 * * 0",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@hourly":   "0 * * * *",
}

// Parse reads a schedule: five fields (minute hour day-of-month month
// day-of-week) with `*`, lists, ranges, steps and month/day names, or one
// of @every <duration>, @hourly, @daily, @weekly, @monthly, @yearly.
//
// Day of month and day of week combine as in Vixie cron: when either field
// starts with `*` both must match, otherwise either one may.
//
// Times are wall-clock times in the location Next is given. A time the
// clocks skip (spring forward) runs when the gap ends; a time the clocks
// repeat (fall back) runs once, at its first occurrence.
func Parse(s string) (Schedule, error) {
	s = strings.TrimSpace(s)
	if rest, ok := strings.CutPrefix(s, "@every"); ok {
		rest = strings.TrimSpace(rest)
		d, err := time.ParseDuration(rest)
		if err != nil {
			return nil, fmt.Errorf("@every: %v", err)
		}
		if d < MinEvery {
			return nil, fmt.Errorf("@every: interval must be at least %s", MinEvery)
		}
		return every(d), nil
	}
	if strings.HasPrefix(s, "@") {
		exp, ok := shortcuts[strings.ToLower(s)]
		if !ok {
			return nil, fmt.Errorf("unknown schedule %q", s)
		}
		s = exp
	}
	f := strings.Fields(s)
	if len(f) != 5 {
		return nil, fmt.Errorf("schedule %q: want 5 fields (minute hour day month weekday), got %d", s, len(f))
	}
	var sp spec
	var err error
	if sp.minute, _, err = parseField(f[0], minuteField); err != nil {
		return nil, err
	}
	if sp.hour, _, err = parseField(f[1], hourField); err != nil {
		return nil, err
	}
	if sp.dom, sp.domStar, err = parseField(f[2], domField); err != nil {
		return nil, err
	}
	if sp.month, _, err = parseField(f[3], monthField); err != nil {
		return nil, err
	}
	if sp.dow, sp.dowStar, err = parseField(f[4], dowField); err != nil {
		return nil, err
	}
	if sp.dow&(1<<7) != 0 { // 7 is Sunday too
		sp.dow = sp.dow&^(1<<7) | 1
	}
	return &sp, nil
}

type every time.Duration

func (e every) Next(t time.Time) time.Time { return t.Add(time.Duration(e)).Truncate(time.Second) }

type field struct {
	name     string
	min, max int
	names    []string // names[i] is value min+i
}

var (
	minuteField = field{name: "minute", min: 0, max: 59}
	hourField   = field{name: "hour", min: 0, max: 23}
	domField    = field{name: "day of month", min: 1, max: 31}
	monthField  = field{name: "month", min: 1, max: 12,
		names: []string{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"}}
	dowField = field{name: "day of week", min: 0, max: 7,
		names: []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}}
)

func parseField(s string, f field) (bits uint64, star bool, err error) {
	for _, part := range strings.Split(s, ",") {
		rng, stepStr, hasStep := strings.Cut(part, "/")
		step := 1
		if hasStep {
			if step, err = strconv.Atoi(stepStr); err != nil || step <= 0 {
				return 0, false, fmt.Errorf("%s: bad step %q", f.name, stepStr)
			}
		}
		var lo, hi int
		switch {
		case rng == "*":
			lo, hi = f.min, f.max
		case strings.Contains(rng, "-"):
			a, b, _ := strings.Cut(rng, "-")
			if lo, err = f.value(a); err != nil {
				return 0, false, err
			}
			if hi, err = f.value(b); err != nil {
				return 0, false, err
			}
			if lo > hi {
				return 0, false, fmt.Errorf("%s: range %q runs backwards", f.name, rng)
			}
		default:
			if lo, err = f.value(rng); err != nil {
				return 0, false, err
			}
			hi = lo
			if hasStep { // "5/15" = from 5 to the end, every 15
				hi = f.max
			}
		}
		for v := lo; v <= hi; v += step {
			bits |= 1 << uint(v)
		}
	}
	return bits, strings.HasPrefix(s, "*"), nil
}

func (f field) value(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("%s: empty value", f.name)
	}
	for i, n := range f.names {
		if strings.EqualFold(s, n) {
			return f.min + i, nil
		}
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%s: bad value %q", f.name, s)
	}
	if v < f.min || v > f.max {
		return 0, fmt.Errorf("%s: %d out of range %d-%d", f.name, v, f.min, f.max)
	}
	return v, nil
}

type spec struct {
	minute, hour, dom, month, dow uint64
	domStar, dowStar              bool
}

// searchYears bounds the search; a schedule like "0 0 30 2 *" never fires.
const searchYears = 5

func (sp *spec) Next(t time.Time) time.Time {
	loc := t.Location()
	// c walks wall-clock minutes; UTC keeps its arithmetic free of DST.
	c := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, time.UTC).Add(time.Minute)
	end := c.AddDate(searchYears, 0, 0)
	for c.Before(end) {
		switch {
		case sp.month&(1<<uint(c.Month())) == 0:
			c = time.Date(c.Year(), c.Month()+1, 1, 0, 0, 0, 0, time.UTC)
		case !sp.dayMatches(c):
			c = time.Date(c.Year(), c.Month(), c.Day()+1, 0, 0, 0, 0, time.UTC)
		case sp.hour&(1<<uint(c.Hour())) == 0:
			c = c.Truncate(time.Hour).Add(time.Hour)
		case sp.minute&(1<<uint(c.Minute())) == 0:
			c = c.Add(time.Minute)
		default:
			if r := resolve(c, loc); r.After(t) {
				return r
			}
			c = c.Add(time.Minute)
		}
	}
	return time.Time{}
}

func (sp *spec) dayMatches(c time.Time) bool {
	dom := sp.dom&(1<<uint(c.Day())) != 0
	dow := sp.dow&(1<<uint(c.Weekday())) != 0
	if sp.domStar || sp.dowStar {
		return dom && dow
	}
	return dom || dow
}

// resolve turns wall-clock time c (fields read in UTC) into an instant in
// loc. A skipped time maps to the end of the gap; a repeated time to its
// first occurrence.
func resolve(c time.Time, loc *time.Location) time.Time {
	r := time.Date(c.Year(), c.Month(), c.Day(), c.Hour(), c.Minute(), 0, 0, loc)
	rw := time.Date(r.Year(), r.Month(), r.Day(), r.Hour(), r.Minute(), 0, 0, time.UTC)
	if !rw.Equal(c) {
		// c does not exist in loc. Go normalized it into one of the two
		// zones around the gap; the gap ends at that zone's boundary.
		start, end := r.ZoneBounds()
		if rw.Before(c) && !end.IsZero() {
			return end
		}
		if !start.IsZero() {
			return start
		}
		return r
	}
	start, _ := r.ZoneBounds()
	if start.IsZero() {
		return r
	}
	_, off := r.Zone()
	_, prevOff := start.Add(-time.Second).Zone()
	if prevOff > off { // clocks went back at start: c may have happened before
		if e := r.Add(-time.Duration(prevOff-off) * time.Second); e.Before(start) {
			return e
		}
	}
	return r
}
