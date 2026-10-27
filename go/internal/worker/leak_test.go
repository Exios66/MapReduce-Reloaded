package worker_test

import (
	"fmt"
	"os"
	"testing"
	"time"

	"go.uber.org/goleak"
)

// TestMain fails the package if any test leaves a goroutine running: a
// server, monitor, heartbeat loop or fetch that outlives its test (#157).
// User code that hangs on purpose is released first, and its workers are
// given about 10 s to unwind.
func TestMain(m *testing.M) {
	code := m.Run()
	close(unhang)
	err := goleak.Find() // retries for about a second itself
	for deadline := time.Now().Add(10 * time.Second); err != nil && time.Now().Before(deadline); {
		err = goleak.Find()
	}
	if code == 0 && err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "goleak:", err)
		code = 1
	}
	os.Exit(code)
}
