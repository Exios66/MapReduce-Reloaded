package coordinator

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain fails the package if any test leaves a goroutine running: a
// server, monitor, heartbeat loop or fetch that outlives its test (#157).
func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }
