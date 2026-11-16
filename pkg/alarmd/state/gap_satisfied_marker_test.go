// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The marker that records a Plan judged new at its first Slot is a clear
// with no scope on a key that holds nothing (design 013 section 4.6): the
// store writes it as a tombstone at the Slot's version, which the next Slot
// reads as satisfied warming. Written again by a retry of the same Slot it
// is the same write, already applied; a warm-up marker at the same version
// is another write of the key at that version, and is refused.
func TestAClearOnAMissingGapKeyIsWrittenAsATombstone(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	router, _ := NewFixedRouter("state-01", backend)
	store, _ := NewExecutionStore(ExecutionStoreOptions{Prefix: "alarmd", Router: router, MaxValueBytes: 4096, MaxItemsPerCall: 4,
		MinTTL: time.Minute, MaxTTL: time.Hour, RestartMargin: time.Minute})
	identity := execution.PlanGapIdentity{Plan: stateIdentityV2().Plan, StateGeneration: "generation"}
	satisfied, err := execution.BuildPlanGapMutation(execution.PlanGapMutation{Identity: identity, ApplyVersion: applyVersion(),
		ScheduleRevision: "plan-r1", Scopes: []execution.GapScopeMutation{{Scope: execution.GapScope{}, Kind: execution.GapClear}}})
	if err != nil {
		t.Fatal(err)
	}
	request := execution.GapGuardApplyRequest{Retention: execution.GenerationRetention{Unknown: true}, Contract: frozenRef(),
		Items: []execution.PlanGapMutation{satisfied}}
	if apply, err := store.ApplyGap(context.Background(), request); err != nil || apply.Items[0].Status != execution.GapGuardApplied {
		t.Fatalf("ApplyGap(clear on a missing key) = (%+v, %v), want it applied", apply, err)
	}
	loaded, err := store.LoadGaps(context.Background(), execution.GapLoadRequest{Contract: frozenRef(),
		Items: []execution.PlanGapLoadItem{{Identity: identity, ApplyVersion: applyVersion(), ScheduleRevision: "plan-r1"}}})
	if err != nil || loaded.Items[0].Status != execution.GapClearedTombstone {
		t.Fatalf("LoadGaps() = (%+v, %v), want the satisfied marker read as a tombstone", loaded, err)
	}

	if apply, err := store.ApplyGap(context.Background(), request); err != nil || apply.Items[0].Status != execution.GapGuardAlreadyApplied {
		t.Fatalf("ApplyGap(the same clear, retried) = (%+v, %v), want it already applied", apply, err)
	}
	key, _ := PlanGapKeyV2("alarmd", identity)
	original := append([]byte(nil), backend.values[key]...)
	warming, err := execution.BuildPlanGapMutation(execution.PlanGapMutation{Identity: identity, ApplyVersion: applyVersion(),
		ScheduleRevision: "plan-r1", ExpectedMarkerRevision: 1, Scopes: []execution.GapScopeMutation{{Scope: execution.GapScope{},
			Kind: execution.GapOpen, ReasonCode: execution.ReasonCode(contract.ReasonConfigDrift), RequiredFullSlots: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	request.Items = []execution.PlanGapMutation{warming}
	if apply, err := store.ApplyGap(context.Background(), request); err != nil || apply.Items[0].Status != execution.GapGuardConflict ||
		string(backend.values[key]) != string(original) {
		t.Fatalf("ApplyGap(a warm-up marker at the same version) = (%+v, %v), want a conflict and the tombstone kept", apply, err)
	}
}
