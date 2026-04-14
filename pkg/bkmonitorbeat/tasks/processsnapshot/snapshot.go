package processsnapshot

import (
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
)

type procStateSnapshot struct {
	updatedAt time.Time
	states    map[int32]string
}

var sharedProcStateSnapshot struct {
	mut      sync.RWMutex
	snapshot procStateSnapshot
}

func UpdateSharedProcStateSnapshot(stats []define.ProcStat) {
	states := make(map[int32]string, len(stats))
	for _, stat := range stats {
		if stat.Status == "" {
			continue
		}
		states[stat.Pid] = stat.Status
	}

	sharedProcStateSnapshot.mut.Lock()
	sharedProcStateSnapshot.snapshot = procStateSnapshot{
		updatedAt: time.Now(),
		states:    states,
	}
	sharedProcStateSnapshot.mut.Unlock()
}

func SharedProcStates(maxAge time.Duration) (map[int32]string, bool) {
	sharedProcStateSnapshot.mut.RLock()
	defer sharedProcStateSnapshot.mut.RUnlock()

	snapshot := sharedProcStateSnapshot.snapshot
	if snapshot.updatedAt.IsZero() || len(snapshot.states) == 0 {
		return nil, false
	}
	if maxAge > 0 && time.Since(snapshot.updatedAt) > maxAge {
		return nil, false
	}

	states := make(map[int32]string, len(snapshot.states))
	for pid, state := range snapshot.states {
		states[pid] = state
	}

	return states, true
}
