package statusweb

import (
	"time"

	"github.com/exios66/mapreduce-reloaded/go/internal/coordinator"
)

// The shape of /status.json. The Python coordinator serves the same keys;
// the tests of both list them.
type jsonStatus struct {
	At      float64      `json:"at"`
	Jobs    []jsonJob    `json:"jobs"`
	Workers []jsonWorker `json:"workers"`
	Lost    []jsonLost   `json:"lost"`
}

type jsonJob struct {
	ID              string           `json:"id"`
	State           string           `json:"state"`
	Error           string           `json:"error"`
	MapCompleted    uint32           `json:"map_completed"`
	MapTotal        uint32           `json:"map_total"`
	ReduceCompleted uint32           `json:"reduce_completed"`
	ReduceTotal     uint32           `json:"reduce_total"`
	BackupAttempts  uint32           `json:"backup_attempts"`
	BackupWins      uint32           `json:"backup_wins"`
	MapHostLocal    uint32           `json:"map_host_local"`
	MapZoneLocal    uint32           `json:"map_zone_local"`
	Counters        map[string]int64 `json:"counters"`
	SubmittedAt     *float64         `json:"submitted_at"`
	FinishedAt      *float64         `json:"finished_at"`
	DurationSeconds *float64         `json:"duration_seconds"`
}

type jsonWorker struct {
	ID       string        `json:"id"`
	Host     string        `json:"host"`
	Zone     string        `json:"zone"`
	Slots    uint32        `json:"slots"`
	LastSeen *float64      `json:"last_seen"`
	Running  []jsonRunning `json:"running"`
}

type jsonRunning struct {
	Task     string  `json:"task"`
	Progress float32 `json:"progress"`
}

type jsonLost struct {
	ID    string   `json:"id"`
	At    float64  `json:"at"`
	Tasks []string `json:"tasks"`
}

// unix is t as Unix seconds, or nil if t was never set.
func unix(t time.Time) *float64 {
	if t.IsZero() {
		return nil
	}
	s := float64(t.UnixNano()) / 1e9
	return &s
}

// toJSON is st in the shape of /status.json.
func toJSON(st coordinator.Status) jsonStatus {
	out := jsonStatus{At: *unix(st.At), Jobs: []jsonJob{}, Workers: []jsonWorker{}, Lost: []jsonLost{}}
	for _, j := range st.Jobs {
		jj := jsonJob{
			ID: j.GetJobId(), State: stateName(j.GetState()), Error: j.GetError(),
			MapCompleted: j.GetMapTasksCompleted(), MapTotal: j.GetMapTasksTotal(),
			ReduceCompleted: j.GetReduceTasksCompleted(), ReduceTotal: j.GetReduceTasksTotal(),
			BackupAttempts: j.GetBackupAttempts(), BackupWins: j.GetBackupWins(),
			MapHostLocal: j.GetMapTasksHostLocal(), MapZoneLocal: j.GetMapTasksZoneLocal(),
			Counters: map[string]int64{},
		}
		for k, v := range j.GetCounters().GetValues() {
			jj.Counters[k] = v
		}
		if j.GetSubmittedAt() != nil {
			jj.SubmittedAt = unix(j.GetSubmittedAt().AsTime())
		}
		if j.GetFinishedAt() != nil {
			jj.FinishedAt = unix(j.GetFinishedAt().AsTime())
		}
		if d, ok := duration(j, st.At); ok {
			s := d.Seconds()
			jj.DurationSeconds = &s
		}
		out.Jobs = append(out.Jobs, jj)
	}
	for _, w := range st.Workers {
		jw := jsonWorker{
			ID: w.ID, Host: w.Info.GetHost(), Zone: w.Info.GetZone(), Slots: w.Info.GetSlots(),
			LastSeen: unix(w.LastSeen), Running: []jsonRunning{},
		}
		for _, t := range w.Running {
			jw.Running = append(jw.Running, jsonRunning{Task: t.String(), Progress: w.Progress[t]})
		}
		out.Workers = append(out.Workers, jw)
	}
	for _, l := range st.Lost {
		jl := jsonLost{ID: l.ID, At: *unix(l.At), Tasks: []string{}}
		for _, t := range l.Tasks {
			jl.Tasks = append(jl.Tasks, t.String())
		}
		out.Lost = append(out.Lost, jl)
	}
	return out
}
