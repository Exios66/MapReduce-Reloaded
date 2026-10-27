package worker

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// The worker's disk is managed in two ways. Map output, kept in one
// directory per job under the work directory, is deleted once the
// coordinator says the job has ended, as the paper deletes intermediate
// files when a job is done. And a worker whose disk is nearly full stops
// taking tasks until space comes back. The check is made before each task,
// not during one, so -min-free-mib must leave room for one task's output; a
// task that still runs out of space fails and is retried (§3.3).

// jobDir matches the directories prune may delete: the coordinator's job
// IDs, so a -work-dir that holds anything else keeps it.
var jobDir = regexp.MustCompile(`^job-[0-9]+$`)

// diskPoll is how often a worker waiting for disk space checks again.
var diskPoll = time.Second

// prune deletes the map output of every job under the work directory that
// is not in active, as of a heartbeat sent at sent. A directory written
// since sent is kept: it may belong to a job submitted after the
// coordinator answered. Only one prune runs at a time, outside the
// heartbeat loop, so a large delete cannot delay a heartbeat.
func (w *Worker) prune(active []string, sent time.Time) {
	if w.KeepEndedJobs || !w.pruning.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer w.pruning.Store(false)
		keep := make(map[string]bool, len(active))
		for _, id := range active {
			keep[id] = true
		}
		entries, err := os.ReadDir(w.workDir)
		if err != nil {
			w.logger.Warn("listing map output", "err", err)
			return
		}
		for _, e := range entries {
			if !e.IsDir() || keep[e.Name()] || !jobDir.MatchString(e.Name()) {
				continue
			}
			info, err := e.Info()
			if err != nil || !info.ModTime().Before(sent) {
				continue
			}
			dir := filepath.Join(w.workDir, e.Name())
			size := dirSize(dir)
			if err := os.RemoveAll(dir); err != nil {
				w.logger.Warn("deleting map output", "job", e.Name(), "err", err)
				continue
			}
			w.stats.prunedJobs.Add(1)
			w.stats.prunedBytes.Add(size)
			w.logger.Info("deleted the map output of an ended job", "job", e.Name(), "bytes", size)
		}
	}()
}

// dirSize returns the bytes in the files under dir, as far as it can read.
func dirSize(dir string) uint64 {
	var n uint64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				n += uint64(max(0, info.Size()))
			}
		}
		return nil
	})
	return n
}

// waitForDisk returns once the work directory's file system has at least
// MinFreeDisk bytes free, or ctx is done. Heartbeats go on while it waits,
// so the coordinator does not declare the worker dead. If free space
// cannot be read, it does not wait.
func (w *Worker) waitForDisk(ctx context.Context) error {
	waiting := false
	for {
		free, err := w.freeBytes(w.workDir)
		if err != nil {
			return nil
		}
		w.stats.diskFree.Store(free)
		if free >= w.MinFreeDisk {
			if waiting {
				w.logger.Info("disk space is back; taking tasks again", "free_bytes", free)
			}
			return nil
		}
		if !waiting {
			w.logger.Warn("disk nearly full; not taking tasks", "free_bytes", free, "min_free_bytes", w.MinFreeDisk)
			w.stats.diskWaits.Add(1)
			waiting = true
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(diskPoll):
		}
	}
}
