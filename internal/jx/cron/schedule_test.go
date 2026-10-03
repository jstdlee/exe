package cron

import (
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("no tzdata for %s: %v", name, err)
	}
	return loc
}

func TestParseErrors(t *testing.T) {
	for _, s := range []string{
		"", "* * * *", "* * * * * *", "60 * * * *", "* 24 * * *", "* * 0 * *",
		"* * 32 * *", "* * * 13 * ", "* * * * 8", "*/0 * * * *", "5-1 * * * *",
		"a * * * *", "* * * foo *", "1,,2 * * * *", "@every", "@every 10s",
		"@every banana", "@sometimes", "1- * * * *",
	} {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q): want error", s)
		}
	}
}

func TestNext(t *testing.T) {
	utc := time.UTC
	at := func(y int, mo time.Month, d, h, mi int) time.Time { return time.Date(y, mo, d, h, mi, 0, 0, utc) }
	// 2026-10-01 is a Thursday.
	from := at(2026, 10, 1, 10, 7)
	cases := []struct {
		spec string
		from time.Time
		want []time.Time // successive runs
	}{
		{"* * * * *", from, []time.Time{at(2026, 10, 1, 10, 8), at(2026, 10, 1, 10, 9)}},
		{"*/15 * * * *", from, []time.Time{at(2026, 10, 1, 10, 15), at(2026, 10, 1, 10, 30), at(2026, 10, 1, 10, 45)}},
		{"5/20 * * * *", from, []time.Time{at(2026, 10, 1, 10, 25), at(2026, 10, 1, 10, 45), at(2026, 10, 1, 11, 5)}},
		{"0,30 9-10 * * *", from, []time.Time{at(2026, 10, 1, 10, 30), at(2026, 10, 2, 9, 0), at(2026, 10, 2, 9, 30)}},
		{"0 9-17/4 * * *", from, []time.Time{at(2026, 10, 1, 13, 0), at(2026, 10, 1, 17, 0), at(2026, 10, 2, 9, 0)}},
		{"@hourly", from, []time.Time{at(2026, 10, 1, 11, 0), at(2026, 10, 1, 12, 0)}},
		{"@daily", from, []time.Time{at(2026, 10, 2, 0, 0), at(2026, 10, 3, 0, 0)}},
		{"@weekly", from, []time.Time{at(2026, 10, 4, 0, 0), at(2026, 10, 11, 0, 0)}},
		{"@monthly", from, []time.Time{at(2026, 11, 1, 0, 0), at(2026, 12, 1, 0, 0)}},
		{"0 0 1 jan *", from, []time.Time{at(2027, 1, 1, 0, 0)}},
		{"30 8 * * mon-fri", from, []time.Time{at(2026, 10, 2, 8, 30), at(2026, 10, 5, 8, 30)}},
		{"0 12 * * SAT,sun", from, []time.Time{at(2026, 10, 3, 12, 0), at(2026, 10, 4, 12, 0), at(2026, 10, 10, 12, 0)}},
		{"0 0 * * 7", from, []time.Time{at(2026, 10, 4, 0, 0)}}, // 7 = Sunday
		{"0 0 * * 5-7", from, []time.Time{at(2026, 10, 2, 0, 0), at(2026, 10, 3, 0, 0), at(2026, 10, 4, 0, 0), at(2026, 10, 9, 0, 0)}},
		{"0 0 29 feb *", from, []time.Time{at(2028, 2, 29, 0, 0)}},
		{"0 0 31 * *", from, []time.Time{at(2026, 10, 31, 0, 0), at(2026, 12, 31, 0, 0)}},
		{"59 23 31 12 *", at(2026, 12, 31, 23, 59), []time.Time{at(2027, 12, 31, 23, 59)}},
		// Vixie: both day fields restricted = either matches (13th OR Friday).
		{"0 0 13 * fri", from, []time.Time{at(2026, 10, 2, 0, 0), at(2026, 10, 9, 0, 0), at(2026, 10, 13, 0, 0), at(2026, 10, 16, 0, 0)}},
		// One day field starting with * = both must match: odd days that are Mondays.
		{"0 0 */2 * mon", from, []time.Time{at(2026, 10, 5, 0, 0), at(2026, 10, 19, 0, 0)}},
		{"0 0 * oct mon", from, []time.Time{at(2026, 10, 5, 0, 0), at(2026, 10, 12, 0, 0)}},
	}
	for _, c := range cases {
		sched, err := Parse(c.spec)
		if err != nil {
			t.Errorf("Parse(%q): %v", c.spec, err)
			continue
		}
		got := c.from
		for i, want := range c.want {
			got = sched.Next(got)
			if !got.Equal(want) {
				t.Errorf("%q run %d: got %v, want %v", c.spec, i+1, got, want)
				break
			}
		}
	}
}

func TestNextNever(t *testing.T) {
	sched, err := Parse("0 0 30 2 *")
	if err != nil {
		t.Fatal(err)
	}
	if n := sched.Next(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); !n.IsZero() {
		t.Fatalf("Feb 30 fired at %v", n)
	}
}

func TestEvery(t *testing.T) {
	sched, err := Parse("@every 90m")
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 10, 1, 10, 7, 30, 500, time.UTC)
	if got, want := sched.Next(from), time.Date(2026, 10, 1, 11, 37, 30, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestNextInLocation(t *testing.T) {
	tokyo := mustLoc(t, "Asia/Tokyo")
	sched, _ := Parse("0 9 * * *")
	got := sched.Next(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).In(tokyo)) // 09:00 JST already passed
	want := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)                      // 09:00 JST next day
	if !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestNextDST(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	utc := func(mo time.Month, d, h, mi int) time.Time { return time.Date(2026, mo, d, h, mi, 0, 0, time.UTC) }
	cases := []struct {
		name string
		spec string
		from time.Time
		want []time.Time
	}{
		// 2026-03-08: 02:00 EST jumps to 03:00 EDT (07:00 UTC).
		{"skipped time runs when the gap ends", "30 2 * * *", utc(3, 8, 5, 0), // 00:00 EST
			[]time.Time{utc(3, 8, 7, 0), utc(3, 9, 6, 30)}},
		{"gap runs collapse into one", "*/15 * * * *", utc(3, 8, 6, 50), // 01:50 EST
			[]time.Time{utc(3, 8, 7, 0), utc(3, 8, 7, 15), utc(3, 8, 7, 30)}},
		{"hourly across spring forward", "0 * * * *", utc(3, 8, 5, 30),
			[]time.Time{utc(3, 8, 6, 0), utc(3, 8, 7, 0), utc(3, 8, 8, 0)}},
		// 2026-11-01: 02:00 EDT falls back to 01:00 EST; 01:xx happens twice.
		{"repeated time runs once", "30 1 * * *", utc(11, 1, 4, 0), // 00:00 EDT
			[]time.Time{utc(11, 1, 5, 30), utc(11, 2, 6, 30)}},
		{"hourly across fall back", "0 * * * *", utc(11, 1, 4, 30),
			[]time.Time{utc(11, 1, 5, 0), utc(11, 1, 7, 0), utc(11, 1, 8, 0)}},
		{"from inside the repeated hour", "*/30 * * * *", utc(11, 1, 6, 10), // 01:10 EST (second pass)
			[]time.Time{utc(11, 1, 7, 0)}},
	}
	for _, c := range cases {
		sched, err := Parse(c.spec)
		if err != nil {
			t.Fatal(err)
		}
		got := c.from.In(ny)
		for i, want := range c.want {
			got = sched.Next(got)
			if !got.Equal(want) {
				t.Errorf("%s (%q) run %d: got %v (%v), want %v", c.name, c.spec, i+1, got.UTC(), got, want)
				break
			}
		}
	}
}
