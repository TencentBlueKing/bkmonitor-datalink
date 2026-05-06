// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tsdb

// 本文件测试 GetStorage miss 路径：ReloadStorageAfterMiss 的 singleflight、冷却、二次查找及 span 行为。
// 使用 package tsdb 以便重置包级 storageMap / ReloadStorageFromConsul 等状态。

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// resetStorageTestState 清空内存 map、reload 回调与节流状态，并 Forget singleflight key，避免用例间串扰。
func resetStorageTestState(t *testing.T) {
	t.Helper()
	storageLock.Lock()
	storageMap = make(map[string]*Storage)
	storageMapHash = ""
	storageLock.Unlock()
	lastMissReloadAttemptUnixNano.Store(0)
	ReloadStorageFromConsul = nil
	SetStorageMissReloadCooldown(defaultStorageMissReloadCooldown)
	storageMissReloadGroup.Forget(storageMissReloadSingleflightKey)
}

// installTestTracer 注册 TracerProvider + SpanRecorder，用于断言是否创建 get-tsdb-storage span。
func installTestTracer(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(prev)
	})
	return rec
}

// endedSpanNames 返回已结束 span 的名称列表。
func endedSpanNames(rec *tracetest.SpanRecorder) []string {
	spans := rec.Ended()
	names := make([]string, 0, len(spans))
	for _, s := range spans {
		names = append(names, s.Name())
	}
	return names
}

// TestGetStorage_hit_doesNotStartSpan：命中热路径不创建 get-tsdb-storage span。
func TestGetStorage_hit_doesNotStartSpan(t *testing.T) {
	resetStorageTestState(t)
	rec := installTestTracer(t)

	SetStorage("1", &Storage{Address: "addr-a"})
	st, err := GetStorage(context.Background(), "1")
	require.NoError(t, err)
	require.Equal(t, "addr-a", st.Address)

	assert.NotContains(t, endedSpanNames(rec), "get-tsdb-storage")
}

// TestGetStorage_miss_createsSpan：内存 miss 且注入 reload 空操作后仍不存在时，应产生 get-tsdb-storage span。
func TestGetStorage_miss_createsSpan(t *testing.T) {
	resetStorageTestState(t)
	rec := installTestTracer(t)

	ReloadStorageFromConsul = func() error { return nil }
	_, err := GetStorage(context.Background(), "missing")
	require.Error(t, err)

	assert.Contains(t, endedSpanNames(rec), "get-tsdb-storage")
}

// TestGetStorage_miss_reloadSecondHit：miss 触发 ReloadStorageFromConsul 写入 map 后，二次查找应命中。
func TestGetStorage_miss_reloadSecondHit(t *testing.T) {
	resetStorageTestState(t)
	rec := installTestTracer(t)

	ReloadStorageFromConsul = func() error {
		SetStorage("99", &Storage{Address: "from-reload"})
		return nil
	}
	st, err := GetStorage(context.Background(), "99")
	require.NoError(t, err)
	require.Equal(t, "from-reload", st.Address)

	assert.Contains(t, endedSpanNames(rec), "get-tsdb-storage")
}

// TestGetStorage_missReload_singleflight：并发多次 miss，ReloadStorageFromConsul 在同一刷新窗口内只执行一次。
func TestGetStorage_missReload_singleflight(t *testing.T) {
	resetStorageTestState(t)
	var calls atomic.Int32
	var wg sync.WaitGroup
	ReloadStorageFromConsul = func() error {
		calls.Add(1)
		time.Sleep(20 * time.Millisecond)
		return nil
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = GetStorage(context.Background(), "x")
		}()
	}
	wg.Wait()
	require.Equal(t, int32(1), calls.Load())
}

// TestGetStorage_missReload_cooldown：首次 miss 会触发一次 reload；冷却期内第二次 miss 不再调用 ReloadStorageFromConsul。
func TestGetStorage_missReload_cooldown(t *testing.T) {
	resetStorageTestState(t)
	SetStorageMissReloadCooldown(time.Hour)
	var calls atomic.Int32
	ReloadStorageFromConsul = func() error {
		calls.Add(1)
		return nil
	}

	ctx := context.Background()
	_, err := GetStorage(ctx, "nope")
	require.Error(t, err)
	require.Equal(t, int32(1), calls.Load())

	_, err = GetStorage(ctx, "still-no")
	require.Error(t, err)
	require.Equal(t, int32(1), calls.Load(), "within cooldown should not invoke reload again")
}

// TestGetStorage_missReload_returnsErrorStillFails：reload 回调返回错误时 GetStorage 仍为 ErrStorageNotFound，且不 panic。
func TestGetStorage_missReload_returnsErrorStillFails(t *testing.T) {
	resetStorageTestState(t)
	ReloadStorageFromConsul = func() error {
		return errors.New("consul down")
	}
	_, err := GetStorage(context.Background(), "id")
	require.Error(t, err)
}
