package server

import (
	"os"
	"testing"
)

// Tests build many short-lived Servers; the idle and cron loops would
// outlive them and tick against freed fakes. Tests drive Tick directly.
func TestMain(m *testing.M) {
	jxStartLoops = false
	os.Exit(m.Run())
}
