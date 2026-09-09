// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycleprocess

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"linkd/internal/config"
	"linkd/internal/lifecycle"
	"linkd/internal/store/storetest"
)

func TestSourceHookAssemblyAndClose(t *testing.T) {
	source := []config.HookConfig{{Name: "active", Type: config.HookTypeActiveAlertByStrategy, Config: config.HookParameters{Redis: &config.RedisConfig{Address: "127.0.0.1:1"}, KeyPrefix: "active"}}}
	hooks, closeHooks, err := openHooks(source, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(hooks) != 1 || hooks[0].Name != "active" {
		t.Fatal("wrong hook binding")
	}
	// 未配置 strategy_id 时跳过，装配不要求输出 Redis 在线。
	input := lifecycle.FinalHookInput{Cause: lifecycle.AlertChangeCause{Type: lifecycle.AlertChangeCauseSourceEvent, ID: "event"}, Alert: storetest.Alert("tenant", "alert", "event", "fp", "warning")}
	result, err := hooks[0].Execute(context.Background(), input)
	if err != nil || !result.Skipped || result.Name != "active" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if err := closeHooks(); err != nil {
		t.Fatal(err)
	}
	if err := closeHooks(); err != nil {
		t.Fatal("close is not idempotent", err)
	}
	empty, closeEmpty, err := openHooks(nil, nil)
	if err != nil || len(empty) != 0 {
		t.Fatal("empty hooks not supported")
	}
	if err := closeEmpty(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := openHooks(append(source, source[0]), nil); err == nil {
		t.Fatal("duplicate instance accepted")
	}
}

func TestHookAssemblyFailureClosesEarlierInstances(t *testing.T) {
	specs := []config.HookConfig{}
	for _, name := range []string{"first", "second", "third"} {
		specs = append(specs, config.HookConfig{Name: name, Type: config.HookTypeActiveAlertByStrategy, Config: config.HookParameters{Redis: &config.RedisConfig{Address: "redis:6379"}, KeyPrefix: "active"}})
	}
	var closed []string
	fail := errors.New("construct failed")
	factory := func(spec config.HookConfig) (lifecycle.FinalHook, func() error, error) {
		if spec.Name == "third" {
			return nil, nil, fail
		}
		return lifecycle.NoopFinalHook{}, func() error { closed = append(closed, spec.Name); return nil }, nil
	}
	if hooks, closeHooks, err := assembleHooks(specs, nil, factory); !errors.Is(err, fail) || hooks != nil || closeHooks != nil {
		t.Fatalf("partial runtime escaped: %v", err)
	}
	if !slices.Equal(closed, []string{"second", "first"}) {
		t.Fatalf("cleanup order=%v", closed)
	}
	closed = nil
	_, closeHooks, err := assembleHooks(specs[:2], nil, factory)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := closeHooks(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if !slices.Equal(closed, []string{"second", "first"}) {
		t.Fatalf("concurrent close repeated: %v", closed)
	}
}
