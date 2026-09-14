// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pipeline

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
)

type retryTestHandler struct {
	attempts     int
	succeedAfter int
	results      []int
	err          error
}

func (h *retryTestHandler) SetManager(manager BulkManager) {}

func (h *retryTestHandler) Handle(context.Context, define.Payload, chan<- error) (interface{}, time.Time, bool) {
	return nil, time.Time{}, false
}

func (h *retryTestHandler) Flush(context.Context, []interface{}) (int, error) {
	h.attempts++
	count := h.results[h.attempts-1]
	if h.attempts < h.succeedAfter {
		err := h.err
		if err == nil {
			err = errors.New("partial bulk failure")
		}
		return count, err
	}
	return count, nil
}

func (h *retryTestHandler) Close() error { return nil }

type nonRetryableTestError struct{}

func (e *nonRetryableTestError) Error() string   { return "permanent bulk failure" }
func (e *nonRetryableTestError) Retryable() bool { return false }

type zeroTimeTestHandler struct{}

func (h *zeroTimeTestHandler) SetManager(manager BulkManager) {}

func (h *zeroTimeTestHandler) Handle(context.Context, define.Payload, chan<- error) (interface{}, time.Time, bool) {
	return struct{}{}, time.Time{}, true
}

func (h *zeroTimeTestHandler) Flush(_ context.Context, results []interface{}) (int, error) {
	return len(results), nil
}

func (h *zeroTimeTestHandler) Close() error { return nil }

type recordingObserver struct {
	mu     sync.Mutex
	values []float64
}

func (o *recordingObserver) Observe(value float64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.values = append(o.values, value)
}

func (o *recordingObserver) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.values)
}

func TestFlushWithRetriesIsFiniteAndDoesNotSumPartialSuccesses(t *testing.T) {
	handler := &retryTestHandler{succeedAfter: 3, results: []int{1, 1, 2}}
	adapter := &BulkBackendAdapter{
		BaseBackend:   define.NewBaseBackend("retry-test"),
		context:       context.Background(),
		handler:       handler,
		flushRetries:  2,
		flushInterval: time.Nanosecond,
	}

	count := adapter.flushWithRetries([]interface{}{1, 2})
	require.Equal(t, 3, handler.attempts)
	require.Equal(t, 2, count)
}

func TestFlushWithRetriesKeepsKnownPartialCountWhenExhausted(t *testing.T) {
	handler := &retryTestHandler{succeedAfter: 10, results: []int{1, 0, 0}}
	adapter := &BulkBackendAdapter{
		BaseBackend:   define.NewBaseBackend("retry-test"),
		context:       context.Background(),
		handler:       handler,
		flushRetries:  2,
		flushInterval: time.Nanosecond,
	}

	count := adapter.flushWithRetries([]interface{}{1, 2})
	require.Equal(t, 3, handler.attempts)
	require.Equal(t, 1, count)
}

func TestFlushWithRetriesStopsOnNonRetryableError(t *testing.T) {
	handler := &retryTestHandler{
		succeedAfter: 10,
		results:      []int{0},
		err:          &nonRetryableTestError{},
	}
	adapter := &BulkBackendAdapter{
		BaseBackend:   define.NewBaseBackend("retry-test"),
		context:       context.Background(),
		handler:       handler,
		flushRetries:  3,
		flushInterval: time.Second,
	}

	count := adapter.flushWithRetries([]interface{}{1, 2})
	require.Equal(t, 1, handler.attempts)
	require.Zero(t, count)
}

func TestBulkBackendAdapterSkipsRecvDeltaForZeroEventTime(t *testing.T) {
	ctx := context.Background()
	ctx = config.PipelineConfigIntoContext(ctx, &config.PipelineConfig{DataID: 1})
	ctx = config.ShipperConfigIntoContext(ctx, &config.MetaClusterInfo{
		ClusterType: "kafka",
		StorageConfig: map[string]interface{}{
			"topic": "zero-time-test",
		},
		ClusterConfig: map[string]interface{}{
			"domain_name": "localhost",
			"port":        9092,
		},
	})

	backend := NewBulkBackendAdapter(ctx, "zero-time-test", &zeroTimeTestHandler{}, 1, time.Hour, 1)
	recvDelta := new(recordingObserver)
	processElapsed := new(recordingObserver)
	backend.ProcessorTimeObserver = &ProcessorTimeObserver{
		frontendRecvDelta: recvDelta,
		processElapsed:    processElapsed,
	}

	payload := define.NewJSONPayload(1)
	payload.SetTime(time.Now())
	backend.Push(payload, nil)
	require.NoError(t, backend.Close())
	require.Zero(t, recvDelta.count())
	require.Equal(t, 1, processElapsed.count())
}
