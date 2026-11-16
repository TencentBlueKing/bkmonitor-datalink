// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// fixedHolding is a live hold of a set number of bytes for one Slot.
type fixedHolding struct {
	slot  execution.SlotIdentity
	bytes uint64
}

func (holding fixedHolding) retainedHolding() retainedHolding {
	return retainedHolding{slot: holding.slot, operation: execution.OperationNormal, since: time.Unix(0, 0),
		bytes: map[string]uint64{RetainedKindInput: holding.bytes}}
}

// A first write is not judged against the share beside what its Slot still
// holds when it writes, as it was before decision-020 batch 8: it holds one
// segment for the length of the write, and the pool, not the share, is what
// that segment has to fit. Beside a hold of its own Slot that leaves less of
// the share than the whole memory takes, and beside another Slot's, it is
// written and nothing is refused.
func TestAFirstNoDataWriteIsNotRefusedBesideWhatItsSlotHolds(t *testing.T) {
	writing := execution.SlotIdentity{QueryGroup: "query-group", EvaluationTime: 60}
	other := execution.SlotIdentity{QueryGroup: "other-group", EvaluationTime: 60}
	request := execution.SlotExecutionRequest{Operation: execution.OperationNormal,
		Contract: execution.FrozenExecutionContractRef{Slot: writing}}
	const groups = 1000
	for _, c := range []struct {
		name   string
		holder execution.SlotIdentity
	}{
		{"beside its own Slot's holds", writing},
		{"beside another Slot's", other},
	} {
		coordinator, store, observed := pooledNoDataFixture(1 << 20)
		share := coordinator.qgShareBytes()
		// Less of the share left than the memory's keys alone take twice.
		coordinator.holders.add(fixedHolding{slot: c.holder, bytes: share - groups*noDataGroupKeyLength})
		err := coordinator.applyNoDataMemory(context.Background(), request, refusedMemoryDue(t),
			[]execution.PlanNoDataMutation{wholeMemory(t, groups)})
		refusals := 0
		for _, observation := range *observed {
			if observation.Stage == observability.StageNoDataMemoryRefused {
				refusals++
			}
		}
		if err != nil || refusals != 0 || store.writes != 1 {
			t.Fatalf("%s: ended %v with %d refusals and %d writes; want it written and nothing refused", c.name, err,
				refusals, store.writes)
		}
	}
}
