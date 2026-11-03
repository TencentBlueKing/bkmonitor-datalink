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
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A round turned away from its Query Group's flight is told what held it,
// and is still ErrSlotInFlight.
func TestARoundTurnedAwayIsToldWhatHeldTheFlight(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	fence := execution.OwnerFence{QueryGroup: "query-group-1", OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "token-1"}
	for holder, take := range map[string]func(*FlightCoordinator) (func(), bool){
		FlightHeldBySupplement:  func(flights *FlightCoordinator) (func(), bool) { return flights.TrySupplement("query-group-1") },
		FlightHeldByMaintenance: func(flights *FlightCoordinator) (func(), bool) { return flights.TryMaintenance("query-group-1") },
		FlightHeldBySlot: func(flights *FlightCoordinator) (func(), bool) {
			release, _, held := flights.tryAcquireAs("query-group-1", FlightHeldBySlot)
			return release, held
		},
	} {
		flights := NewFlightCoordinator()
		release, held := take(flights)
		if !held {
			t.Fatalf("%s: setup could not take the flight", holder)
		}
		runner, err := NewRunner("query-group-1", &fakeSession{fence: fence, deadline: now.Add(time.Minute)},
			&fakeSlotSource{slot: frozenSlot("query-group-1")}, &followingRecordingExecutor{}, flights, func() time.Time { return now })
		if err != nil {
			t.Fatal(err)
		}
		_, attempted, err := runner.RunOne(context.Background())
		var inFlight *SlotInFlightError
		if attempted || !errors.Is(err, ErrSlotInFlight) || !errors.As(err, &inFlight) || inFlight.HeldBy != holder {
			t.Errorf("%s: attempted %v, err %v; want the round turned away and told it was %s", holder, attempted, err, holder)
		}
		release()
	}
}

// The end of a supplement's or maintenance's hold that turned a Slot away
// is told once; a hold that turned none away, and a Slot's hold, are not.
func TestTheEndOfAHoldThatTurnedASlotAwayIsTold(t *testing.T) {
	flights := NewFlightCoordinator()
	var told []execution.QueryGroupIdentity
	flights.OnTurnedAwayReleased(func(queryGroup execution.QueryGroupIdentity) { told = append(told, queryGroup) })

	release, _ := flights.TrySupplement("query-group-1")
	if holder, held := flights.FlightHeld("query-group-1"); !held || holder != FlightHeldBySupplement {
		t.Fatalf("flight held by %q (%v), want the supplement", holder, held)
	}
	for turn := 0; turn < 2; turn++ {
		if _, heldBy, acquired := flights.tryAcquireAs("query-group-1", FlightHeldBySlot); acquired || heldBy != FlightHeldBySupplement {
			t.Fatalf("a Slot took a held flight, or was told %q", heldBy)
		}
	}
	release()
	release()
	if len(told) != 1 || told[0] != "query-group-1" {
		t.Fatalf("told %v, want the one Query Group once", told)
	}

	release, _ = flights.TryMaintenance("query-group-1")
	release()
	slot, _, _ := flights.tryAcquireAs("query-group-1", FlightHeldBySlot)
	if _, _, acquired := flights.tryAcquireAs("query-group-1", FlightHeldBySlot); acquired {
		t.Fatal("a second Slot took the flight")
	}
	slot()
	if len(told) != 1 {
		t.Fatalf("told %v, want nothing more for a hold that turned no Slot away or a Slot's own hold", told)
	}
}
