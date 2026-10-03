package lease

import (
	"testing"
	"time"
)

func TestIdleCountsFromLastRelease(t *testing.T) {
	now := time.Unix(1000, 0)
	tb := New()
	tb.SetClock(func() time.Time { return now })
	if _, idle := tb.Get("a").Idle(now); idle {
		t.Fatal("unseen VM must not be idle")
	}
	r1 := tb.Acquire("a", "terminal")
	r2 := tb.Acquire("a", "board")
	now = now.Add(time.Hour)
	r1()
	if _, idle := tb.Get("a").Idle(now); idle {
		t.Fatal("VM with a holder is not idle")
	}
	now = now.Add(time.Minute)
	r2()
	r2() // idempotent
	now = now.Add(5 * time.Minute)
	d, idle := tb.Get("a").Idle(now)
	if !idle || d != 5*time.Minute {
		t.Fatalf("idle=%v d=%v, want 5m", idle, d)
	}
	tb.Touch("a")
	if d, _ := tb.Get("a").Idle(now); d != 0 {
		t.Fatalf("touch: d=%v", d)
	}
}
