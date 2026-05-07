// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tsdb

// 本文件测试 GetStorage miss 路径和默认 miss-reload 策略。
// 使用 package tsdb 以便重置包级 storageMap / 默认策略状态。

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
)

type stubStorageMissReloadStrategy struct {
	calls atomic.Int32
}

func (s *stubStorageMissReloadStrategy) ReloadAfterMiss(context.Context, string) {
	s.calls.Add(1)
}

// resetStorageTestState 清空内存 map 和默认策略状态，避免用例间串扰。
func resetStorageTestState(t *testing.T) {
	t.Helper()
	storageLock.Lock()
	storageMap = make(map[string]*Storage)
	storageMapHash = ""
	storageLock.Unlock()
	SetStorageMissReloadStrategy(nil)
	defaultStorageMissReloadStrategy.resetForTest()
	getTsDBStorageInfo = consul.GetTsDBStorageInfo
}

// TestGetStorage_hit_doesNotTriggerReload：命中热路径不触发 miss-reload 策略。
func TestGetStorage_hit_doesNotTriggerReload(t *testing.T) {
	resetStorageTestState(t)
	stub := &stubStorageMissReloadStrategy{}
	SetStorageMissReloadStrategy(stub)

	SetStorage("1", &Storage{Address: "addr-a"})
	st, err := GetStorage(context.Background(), "1")
	require.NoError(t, err)
	require.Equal(t, "addr-a", st.Address)
	require.Equal(t, int32(0), stub.calls.Load())
}

// TestGetStorage_miss_reloadSecondHit：miss 触发 ReloadStorageFromConsul 写入 map 后，二次查找应命中。
func TestGetStorage_miss_reloadSecondHit(t *testing.T) {
	resetStorageTestState(t)

	SetStorageMissReloadFunc(func() error {
		SetStorage("99", &Storage{Address: "from-reload"})
		return nil
	})
	st, err := GetStorage(context.Background(), "99")
	require.NoError(t, err)
	require.Equal(t, "from-reload", st.Address)
}

// TestGetStorage_missReload_singleflight：并发多次 miss，ReloadStorageFromConsul 在同一刷新窗口内只执行一次。
func TestGetStorage_missReload_singleflight(t *testing.T) {
	resetStorageTestState(t)
	var calls atomic.Int32
	var wg sync.WaitGroup
	SetStorageMissReloadFunc(func() error {
		calls.Add(1)
		time.Sleep(20 * time.Millisecond)
		return nil
	})
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
	SetStorageMissReloadFunc(func() error {
		calls.Add(1)
		return nil
	})

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
	SetStorageMissReloadFunc(func() error {
		return errors.New("consul down")
	})
	_, err := GetStorage(context.Background(), "id")
	require.Error(t, err)
}

func TestGetStorage_missReloadFetchesConsulStorageIDs(t *testing.T) {
	resetStorageTestState(t)
	var fetchCalls atomic.Int32

	getTsDBStorageInfo = func() (map[string]*consul.Storage, error) {
		fetchCalls.Add(1)
		return map[string]*consul.Storage{
			"20": {},
			"3":  {},
			"11": {},
		}, nil
	}
	SetStorageMissReloadFunc(func() error {
		return nil
	})

	_, err := GetStorage(context.Background(), "404")
	require.Error(t, err)
	require.Equal(t, int32(1), fetchCalls.Load())
}

func TestGetStorage_missReloadFailureStillFetchesConsulStorageIDs(t *testing.T) {
	resetStorageTestState(t)
	var fetchCalls atomic.Int32

	getTsDBStorageInfo = func() (map[string]*consul.Storage, error) {
		fetchCalls.Add(1)
		return map[string]*consul.Storage{
			"20": {},
			"3":  {},
			"11": {},
		}, nil
	}
	SetStorageMissReloadFunc(func() error {
		return errors.New("consul down")
	})

	_, err := GetStorage(context.Background(), "404")
	require.Error(t, err)
	require.Equal(t, int32(1), fetchCalls.Load())
}

func TestStorageMissReloadStrategy_nilReloadFuncNoop(t *testing.T) {
	resetStorageTestState(t)
	strategy := newCooldownStorageMissReloadStrategy(time.Second, nil)

	strategy.ReloadAfterMiss(context.Background(), "id")

	require.Equal(t, int64(0), strategy.lastAttempt.Load())
}

func TestStorageMissReloadStrategy_nonPositiveCooldownFallsBackToDefault(t *testing.T) {
	resetStorageTestState(t)
	strategy := newCooldownStorageMissReloadStrategy(0, nil)

	require.Equal(t, defaultStorageMissReloadCooldown, strategy.cooldown())

	strategy.SetCooldown(-time.Second)
	require.Equal(t, defaultStorageMissReloadCooldown, strategy.cooldown())
}

func TestStorageMissReloadStrategy_settersTakeEffectImmediately(t *testing.T) {
	resetStorageTestState(t)
	strategy := newCooldownStorageMissReloadStrategy(time.Hour, nil)
	var calls atomic.Int32

	strategy.SetReloadFunc(func() error {
		calls.Add(1)
		return nil
	})
	strategy.ReloadAfterMiss(context.Background(), "id")
	require.Equal(t, int32(1), calls.Load())

	strategy.SetCooldown(time.Nanosecond)
	time.Sleep(time.Millisecond)
	strategy.ReloadAfterMiss(context.Background(), "id")
	require.Equal(t, int32(2), calls.Load())
}

func TestLoadConsulStorageIDs_sorted(t *testing.T) {
	resetStorageTestState(t)
	getTsDBStorageInfo = func() (map[string]*consul.Storage, error) {
		return map[string]*consul.Storage{
			"20": {},
			"3":  {},
			"11": {},
			"a":  {},
		}, nil
	}

	ids, err := loadConsulStorageIDs()
	require.NoError(t, err)
	require.Equal(t, []string{"3", "11", "20", "a"}, ids)
}

func TestLoadConsulStorageIDs_returnsError(t *testing.T) {
	resetStorageTestState(t)
	getTsDBStorageInfo = func() (map[string]*consul.Storage, error) {
		return nil, errors.New("consul unavailable")
	}

	ids, err := loadConsulStorageIDs()
	require.Error(t, err)
	require.Nil(t, ids)
}
