package worker_test

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

func TestSeriesSamplePreservesWorkerNoDataTriggerAndRecovery(t *testing.T) {
	redistest.Server(t)
	address := startG3ARedis(t)
	off, offBackend := openG3AStateStore(t, address, "sample-off")
	on, onBackend := openG3AStateStore(t, address, "sample-on")
	t.Cleanup(func() { _ = offBackend.Close(); _ = onBackend.Close() })
	worker.CheckSeriesSampleNoDataWorkerRegression(t, off, on)
}
