package worker_test

import (
	"fmt"
	"os"
	"testing"

	"go.uber.org/goleak"
)

// TestMain fails the package if any test leaves a goroutine running: a
// server, monitor, heartbeat loop or fetch that outlives its test (#157).
// User code that hangs on purpose is released first.
func TestMain(m *testing.M) {
	code := m.Run()
	close(unhang)
	if err := goleak.Find(); code == 0 && err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "goleak:", err)
		code = 1
	}
	os.Exit(code)
}
