// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A permit says whether its query had to wait for it: the downstream's errors
// are read against it, to tell errors this process's own contention brought
// on from errors that arrive whatever it does.
func TestAPermitSaysWhetherItsQueryWaitedForIt(t *testing.T) {
	clock := newMutableClock(time.Unix(300, 0))
	flights, err := NewFlightCoordinatorWithRecovery(testRecoveryLimits(), clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	deadline := clock.Now().Add(time.Minute)
	var held []*QueryPermit
	for i := 0; i < testRecoveryLimits().ProcessQueryPermits; i++ {
		permit, err := flights.AcquireQueryPermit(ctx, execution.SlotIdentity{QueryGroup: execution.QueryGroupIdentity(rune('a' + i)), EvaluationTime: 60}, execution.OperationNormal, deadline)
		if err != nil {
			t.Fatal(err)
		}
		if permit.Waited() {
			t.Fatalf("permit %d was free and says its query waited", i)
		}
		held = append(held, permit)
	}
	granted := make(chan *QueryPermit, 1)
	go func() {
		permit, err := flights.AcquireQueryPermit(ctx, execution.SlotIdentity{QueryGroup: "late", EvaluationTime: 60}, execution.OperationNormal, deadline)
		if err != nil {
			t.Error(err)
		}
		granted <- permit
	}()
	for flights.queryPermitSnapshot().NormalWaiting == 0 {
		time.Sleep(time.Millisecond)
	}
	held[0].Release()
	permit := <-granted
	if permit == nil || !permit.Waited() {
		t.Fatalf("a permit granted after a wait says its query did not wait")
	}
	permit.Release()
	for _, permit := range held[1:] {
		permit.Release()
	}
	var none *QueryPermit
	if none.Waited() {
		t.Fatal("a nil permit says it waited")
	}
}
