package pipeline

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/eventbus"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

type maxConcurrencyPayload struct {
	receivedAt time.Time
}

func (p *maxConcurrencyPayload) To(v interface{}) error { return nil }
func (p *maxConcurrencyPayload) From(v interface{}) error { return nil }
func (p *maxConcurrencyPayload) SN() int { return 0 }
func (p *maxConcurrencyPayload) Type() string { return "test" }
func (p *maxConcurrencyPayload) Meta() define.PayloadMeta { return nil }
func (p *maxConcurrencyPayload) SetTime(t time.Time) { p.receivedAt = t }
func (p *maxConcurrencyPayload) GetTime() time.Time { return p.receivedAt }

type maxConcurrencyHandler struct {
	active      int64
	max         int64
	releaseOnce sync.Once
	releaseCh   chan struct{}
}

func (h *maxConcurrencyHandler) SetManager(manager BulkManager) {}

func (h *maxConcurrencyHandler) Handle(ctx context.Context, payload define.Payload, killChan chan<- error) (interface{}, time.Time, bool) {
	return payload, time.Now(), true
}

func (h *maxConcurrencyHandler) Flush(ctx context.Context, results []interface{}) (int, error) {
	active := atomic.AddInt64(&h.active, 1)
	for {
		max := atomic.LoadInt64(&h.max)
		if active <= max || atomic.CompareAndSwapInt64(&h.max, max, active) {
			break
		}
	}

	select {
	case <-h.releaseCh:
	case <-ctx.Done():
	}
	atomic.AddInt64(&h.active, -1)
	return len(results), nil
}

func (h *maxConcurrencyHandler) Close() error { return nil }

func TestBackendMaxConcurrencyLimitsConcurrentFlushes(t *testing.T) {
	// Given: pipeline backend config limits global flush concurrency to two.
	oldConcurrency := BulkDefaultConcurrency
	oldMaxConcurrency := BulkDefaultMaxConcurrency
	oldGlobalConcurrencySemaphore := BulkGlobalConcurrencySemaphore
	oldGlobalPushSemaphore := BulkGlobalPushSemaphore
	t.Cleanup(func() {
		BulkDefaultConcurrency = oldConcurrency
		BulkDefaultMaxConcurrency = oldMaxConcurrency
		BulkGlobalConcurrencySemaphore = oldGlobalConcurrencySemaphore
		BulkGlobalPushSemaphore = oldGlobalPushSemaphore
	})

	conf := define.NewViperConfiguration(viper.New())
	eventbus.Publish(eventbus.EvSysConfigPreParse, conf)
	conf.Set(ConfKeyPayloadFlushConcurrency, 100)
	conf.Set(ConfKeyPayloadFlushMaxConcurrency, 2)
	eventbus.Publish(eventbus.EvSysConfigPostParse, conf)
	BulkGlobalPushSemaphore = utils.NewWeightedSemaphore(10000)

	ctx := context.Background()
	ctx = config.PipelineConfigIntoContext(ctx, &config.PipelineConfig{DataID: 1})
	ctx = config.ShipperConfigIntoContext(ctx, &config.MetaClusterInfo{
		ClusterType: "kafka",
		StorageConfig: map[string]interface{}{
			"topic": "max-concurrency-test",
		},
		ClusterConfig: map[string]interface{}{
			"domain_name": "localhost",
			"port":        9092,
		},
	})
	handler := &maxConcurrencyHandler{releaseCh: make(chan struct{})}
	backend := NewBulkBackendAdapter(ctx, "max-concurrency-test", handler, 1, time.Hour, 1)
	t.Cleanup(func() {
		handler.releaseOnce.Do(func() { close(handler.releaseCh) })
		require.NoError(t, backend.Close())
	})

	// When: many payloads force many flush goroutines.
	for i := 0; i < 20; i++ {
		payload := &maxConcurrencyPayload{}
		payload.SetTime(time.Now())
		backend.Push(payload, nil)
	}

	require.Eventually(t, func() bool {
		return atomic.LoadInt64(&handler.max) > 1
	}, time.Second, 10*time.Millisecond)
	handler.releaseOnce.Do(func() { close(handler.releaseCh) })

	// Then: observed concurrent flushes never exceed configured global max concurrency.
	require.LessOrEqual(t, atomic.LoadInt64(&handler.max), int64(2), fmt.Sprintf("observed max flush concurrency: %d", handler.max))
}
