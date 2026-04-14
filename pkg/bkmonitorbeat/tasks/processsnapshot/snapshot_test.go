package processsnapshot

import (
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
)

func TestSharedProcStates(t *testing.T) {
	sharedProcStateSnapshot.mut.Lock()
	sharedProcStateSnapshot.snapshot = procStateSnapshot{}
	sharedProcStateSnapshot.mut.Unlock()

	UpdateSharedProcStateSnapshot([]define.ProcStat{
		{Pid: 1, Status: "running"},
		{Pid: 2, Status: "zombie"},
		{Pid: 3, Status: "zombie"},
	})

	snapshot, ok := SharedProcStates(time.Minute)
	if !ok {
		t.Fatal("expected shared snapshot to be available")
	}
	if len(snapshot) != 3 {
		t.Fatalf("expected 3 process states, got %d", len(snapshot))
	}
	if snapshot[2] != "zombie" {
		t.Fatalf("expected pid 2 state zombie, got %s", snapshot[2])
	}

	sharedProcStateSnapshot.mut.Lock()
	sharedProcStateSnapshot.snapshot.updatedAt = time.Now().Add(-2 * time.Minute)
	sharedProcStateSnapshot.mut.Unlock()

	if _, ok := SharedProcStates(time.Minute); ok {
		t.Fatal("expected stale shared snapshot to be rejected")
	}
}
