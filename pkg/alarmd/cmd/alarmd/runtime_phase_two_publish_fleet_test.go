// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The fleet publish keeps cursors and caches between calls, and it is called
// from the maintenance ticker and from anything that asks for a publish now
// (the election test asks the Leader to write its record while the ticker
// runs). The bundle runs one publish at a time whoever calls it.
func TestTheBundleRunsOneFleetPublishAtATime(t *testing.T) {
	var inside, most atomic.Int32
	publish := func(context.Context) {
		n := inside.Add(1)
		for {
			seen := most.Load()
			if n <= seen || most.CompareAndSwap(seen, n) {
				break
			}
		}
		time.Sleep(time.Millisecond) // fixed-wait: the simulated publish time that makes concurrent publishes overlap, the behaviour measured
		inside.Add(-1)
	}
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"}, initialResult: healthyResult()}
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{"query-group-1"}, runner: newFakePhaseTwoQueryGroup()}
	bundle, err := newPhaseTwoWorkerBundle(phaseTwoWorkerBundleDependencies{
		Config: validGoAccessRuntimeConfig(), Health: newPhaseTwoApplicationHealth(), Control: control, Ownership: owner,
		Observer: observability.NopObserver{}, Now: time.Now, PublishFleet: publish,
	})
	if err != nil {
		t.Fatalf("newPhaseTwoWorkerBundle() error = %v", err)
	}
	var callers sync.WaitGroup
	for i := 0; i < 8; i++ {
		callers.Add(1)
		go func() {
			defer callers.Done()
			for j := 0; j < 10; j++ {
				bundle.dependencies.PublishFleet(context.Background())
			}
		}()
	}
	callers.Wait()
	if got := most.Load(); got != 1 {
		t.Fatalf("publishes running at once = %d, want 1", got)
	}
}
