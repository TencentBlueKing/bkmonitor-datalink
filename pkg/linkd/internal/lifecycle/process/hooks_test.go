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
	"errors"
	"slices"
	"sync"
	"testing"

	"linkd/internal/config"
	"linkd/internal/lifecycle"
)

func TestSourceHookAssemblyAndClose(t *testing.T) {
	source := []config.HookConfig{{Name: "active", Type: config.HookTypeKafka, Config: config.HookParameters{Brokers: []string{"127.0.0.1:1"}, Topic: "alerts"}}}
	hooks, closeHooks, err := openHooks(source, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(hooks) != 1 {
		t.Fatal("wrong binding")
	}
	if err := closeHooks(); err != nil {
		t.Fatal(err)
	}
	if err := closeHooks(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := openHooks(append(source, source[0]), nil); err == nil {
		t.Fatal("duplicate accepted")
	}
	if _, _, err := openHooks([]config.HookConfig{{Name: "old", Type: "active-alert-by-strategy"}}, nil); err == nil {
		t.Fatal("removed hook accepted")
	}
}

func TestOpenHookSupportsKAC(t *testing.T) {
	spec := config.HookConfig{Name: "kac-main", Type: config.HookTypeKAC, Config: config.HookParameters{
		Brokers: []string{"127.0.0.1:1"}, Topic: "kac-alerts",
	}}
	hook, closeHook, err := openHook(spec)
	if err != nil {
		t.Fatal(err)
	}
	if hook == nil || closeHook == nil {
		t.Fatal("KAC hook runtime is incomplete")
	}
	if err := closeHook(); err != nil {
		t.Fatal(err)
	}
}

func TestHookAssemblyFailureClosesEarlierInstances(t *testing.T) {
	specs := []config.HookConfig{}
	for _, name := range []string{"first", "second", "third"} {
		specs = append(specs, config.HookConfig{Name: name, Type: config.HookTypeKafka, Config: config.HookParameters{Brokers: []string{"kafka:9092"}, Topic: "alerts"}})
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
