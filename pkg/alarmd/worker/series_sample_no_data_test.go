package worker_test

import (
	"os/exec"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

func TestSeriesSamplePreservesWorkerNoDataTriggerAndRecovery(t *testing.T) {
	if _, err := exec.LookPath("redis-server"); err != nil {
		t.Skip("redis-server is required for the NoData sampling regression")
	}
	address := startG3ARedis(t)
	off, offBackend := openG3AStateStore(t, address, "sample-off")
	on, onBackend := openG3AStateStore(t, address, "sample-on")
	t.Cleanup(func() { _ = offBackend.Close(); _ = onBackend.Close() })
	worker.CheckSeriesSampleNoDataWorkerRegression(t, off, on)
}
