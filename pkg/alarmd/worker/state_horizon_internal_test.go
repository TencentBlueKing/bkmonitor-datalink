// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// How long a Plan's series keep their runtime state: the Plan's own no-data
// horizon when it has one frozen, else the platform's in force now, else
// none. Three distinct numbers so a wrong source cannot pass as the right one.
func TestAPlansStateHorizonIsItsOwnFrozenOneElseThePlatformsNow(t *testing.T) {
	platform := int64(7200)
	coordinator := &SlotExecutionCoordinator{ports: Ports{StateHorizon: func() int64 { return platform }}}

	frozen := execution.DuePlan{CompiledPlan: noDataPreflightPlan(t, "7", &contract.NoDataConfigV1{
		Continuous: 1, Level: 2, AggDimension: []string{"bk_target_ip"}, TrackingHorizonSeconds: 3600,
	})}
	if got := coordinator.stateHorizon(frozen); got != 3600 {
		t.Fatalf("a Plan with its own frozen horizon gets %d, want its 3600", got)
	}
	if got := coordinator.stateHorizon(execution.DuePlan{}); got != platform {
		t.Fatalf("a Plan with none gets %d, want the platform's %d in force now", got, platform)
	}
	platform = 600
	if got := coordinator.stateHorizon(execution.DuePlan{}); got != 600 {
		t.Fatalf("after the platform's horizon moved to 600 a Plan with none gets %d: it is read per Slot", got)
	}
	unwired := &SlotExecutionCoordinator{}
	if got := unwired.stateHorizon(execution.DuePlan{}); got != 0 {
		t.Fatalf("a worker given no horizon caps nothing, got %d", got)
	}
}

// The horizon travels on every apply request the Slot's chunks send: the line
// between the coordinator and the store that sets the key's lifetime.
func TestEveryApplyChunkCarriesTheHorizon(t *testing.T) {
	store := &chunkStore{}
	fixture := newChunkFixture(store, 8192)
	if _, err := fixture.coordinator.applyState(context.Background(), execution.OperationNormal, fixture.contract,
		fixture.fence, "", chunkRetention, 3600, chunkMutations(8192+1), nil); err != nil {
		t.Fatal(err)
	}
	if len(store.horizons) != 2 {
		t.Fatalf("applied in %d chunks, want two so every chunk is checked", len(store.horizons))
	}
	for index, horizon := range store.horizons {
		if horizon != 3600 {
			t.Fatalf("chunk %d carried horizon %d, want 3600", index, horizon)
		}
	}
}
