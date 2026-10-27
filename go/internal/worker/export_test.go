package worker

import "time"

// SetFreeBytes replaces how w reads free disk space.
func SetFreeBytes(w *Worker, f func(dir string) (uint64, error)) { w.freeBytes = f }

// SetDiskPoll sets how often a waiting worker checks free space again, until
// the returned function restores it.
func SetDiskPoll(d time.Duration) (restore func()) {
	old := diskPoll
	diskPoll = d
	return func() { diskPoll = old }
}
