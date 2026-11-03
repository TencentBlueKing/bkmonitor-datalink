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
	"encoding/json"
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// namedOutputCompletion is a completion whose one query aligned records
// records of two outputs.
func namedOutputCompletion(records int) execution.QueryExecutionCompletion {
	table := &execution.NamedOutputRecords{Refs: []string{"A", "C"}, Records: map[string][]execution.NamedOutputValue{}}
	for record := 0; record < records; record++ {
		table.Records[fmt.Sprintf("%064x", record)] = []execution.NamedOutputValue{
			{Value: json.RawMessage("1"), State: execution.NamedOutputSuccess}, {Value: json.RawMessage("11"), State: execution.NamedOutputSuccess}}
	}
	return execution.QueryExecutionCompletion{PhysicalQueries: []execution.PhysicalQueryCompletion{{NamedOutputs: table}}}
}

func namedOutputStream(maxRetained, own uint64) (*SlotExecutionCoordinator, *streamedExecution) {
	co := &SlotExecutionCoordinator{budget: ProvisionalBudget{
		MaxSeries: 10, MaxRetainedBytes: maxRetained, MaxStateMutations: 10, MaxEvents: 10, MaxGapMutations: 10,
	}}
	co.reservations.retainedBytes = own
	return co, &streamedExecution{coordinator: co, began: true, retainedByPhase: retainedSeed(own)}
}

// The named outputs a Slot's queries aligned are input it retains: charged
// to it when the completion arrives, at what the table holds, to the input
// phase and the pool, and given back with the Slot. A completion of queries
// that asked for none is charged nothing.
func TestANamedOutputTableIsChargedToTheSlotAndReleasedWithIt(t *testing.T) {
	completion := namedOutputCompletion(10)
	retained := namedOutputRecordsRetainedBytes(completion)
	if retained == 0 || retained != completion.PhysicalQueries[0].NamedOutputs.RetainedBytes() {
		t.Fatalf("a table of 10 records is charged %d", retained)
	}
	co, stream := namedOutputStream(1<<20, 100)
	if err := stream.retainNamedOutputs(context.Background(), completion); err != nil {
		t.Fatal(err)
	}
	if stream.retainedByPhase[retainPhaseInput] != 100+retained || co.reservations.retainedBytes != 100+retained {
		t.Fatalf("input %d pool %d, want %d", stream.retainedByPhase[retainPhaseInput], co.reservations.retainedBytes, 100+retained)
	}
	stream.releaseProvisional()
	if co.reservations.retainedBytes != 0 {
		t.Fatalf("%d bytes still reserved after the Slot", co.reservations.retainedBytes)
	}
	co, stream = namedOutputStream(1<<20, 100)
	if err := stream.retainNamedOutputs(context.Background(), execution.QueryExecutionCompletion{
		PhysicalQueries: []execution.PhysicalQueryCompletion{{}}}); err != nil || co.reservations.retainedBytes != 100 {
		t.Fatalf("a completion without named outputs was charged: %v, pool %d", err, co.reservations.retainedBytes)
	}
}

// A table that takes the Slot past the share one Query Group may hold is
// refused as a share refusal of the Slot's own total - the same one a
// series over the share gets, which the scheduler stops re-reading - and
// reserves nothing.
func TestANamedOutputTablePastTheShareIsRefused(t *testing.T) {
	completion := namedOutputCompletion(10)
	retained := namedOutputRecordsRetainedBytes(completion)
	// The share is three quarters of the pool: room for the Slot's own 100
	// bytes and not for the table beside them.
	co, stream := namedOutputStream((100+retained)/3*4-4, 100)
	err := stream.retainNamedOutputs(context.Background(), completion)
	refusal, deterministic := execution.ShareRefusalOf(fmt.Errorf("alarmd worker: complete Slot: %w", err))
	if err == nil || !deterministic || refusal.RequestedBytes != retained || refusal.OwnUsedBytes != 100 {
		t.Fatalf("refusal %+v deterministic %v error %v", refusal, deterministic, err)
	}
	if co.reservations.retainedBytes != 100 || stream.retainedByPhase[retainPhaseInput] != 100 {
		t.Fatalf("a refused table reserved: pool %d input %d", co.reservations.retainedBytes, stream.retainedByPhase[retainPhaseInput])
	}
}

// What a Slot's queries reserve for their named outputs is held to the
// share between them, as its series are: once one query holds the share
// the next is refused at once, as a share refusal of the Slot's own total,
// and reserves nothing; a release makes the room again.
func TestTwoNamedQueriesOfASlotCannotExceedTheShareBetweenThem(t *testing.T) {
	co, stream := namedOutputStream(1000, 0) // a share of 750
	if err := stream.ReserveRetainedBytes(context.Background(), 700); err != nil {
		t.Fatal(err)
	}
	err := stream.ReserveRetainedBytes(context.Background(), 100)
	if _, deterministic := execution.ShareRefusalOf(err); err == nil || !deterministic {
		t.Fatalf("the second query past the share: %v", err)
	}
	if stream.namedReserved != 700 || co.reservations.retainedBytes != 700 || stream.retainedByPhase[retainPhaseInput] != 700 {
		t.Fatalf("named %d pool %d input %d after a refusal", stream.namedReserved, co.reservations.retainedBytes, stream.retainedByPhase[retainPhaseInput])
	}
	stream.ReleaseRetainedBytes(700)
	if err := stream.ReserveRetainedBytes(context.Background(), 100); err != nil || co.reservations.retainedBytes != 100 ||
		stream.RetainedInputBytesNow() != 100 {
		t.Fatalf("after the release: %v, pool %d input now %d", err, co.reservations.retainedBytes, stream.RetainedInputBytesNow())
	}
}

// Named queries of two Slots are held to the pool between them: each
// under its own share, the second is refused once the first and the second
// would pass the pool, and that refusal is the pool's, not the share's.
func TestNamedQueriesOfTwoSlotsCannotExceedThePool(t *testing.T) {
	co, first := namedOutputStream(1000, 0)
	second := &streamedExecution{coordinator: co, began: true}
	if err := first.ReserveRetainedBytes(context.Background(), 600); err != nil {
		t.Fatal(err)
	}
	err := second.ReserveRetainedBytes(context.Background(), 600)
	if _, deterministic := execution.ShareRefusalOf(err); err == nil || deterministic {
		t.Fatalf("the second Slot past the pool: %v (a share refusal: %v)", err, deterministic)
	}
	if co.reservations.retainedBytes != 600 || second.namedReserved != 0 || second.retainedTotal() != 0 {
		t.Fatalf("pool %d second %d/%d after the refusal", co.reservations.retainedBytes, second.namedReserved, second.retainedTotal())
	}
}

// At the completion what the queries reserved becomes the tables' charge,
// not a second reservation of it: what is left reserved is the tables', a
// query's reservation beyond them is given back, and a table no query
// reserved for is reserved then. After the Slot, nothing.
func TestTheNamedReservationBecomesTheTablesChargeAtTheCompletion(t *testing.T) {
	completion := namedOutputCompletion(10)
	table := namedOutputRecordsRetainedBytes(completion)
	for _, reservedBefore := range []uint64{table, table + 500, table / 2} {
		co, stream := namedOutputStream(1<<20, 0)
		if err := stream.ReserveRetainedBytes(context.Background(), reservedBefore); err != nil {
			t.Fatal(err)
		}
		if err := stream.retainNamedOutputs(context.Background(), completion); err != nil {
			t.Fatal(err)
		}
		if stream.namedReserved != table || co.reservations.retainedBytes != table || stream.retainedByPhase[retainPhaseInput] != table {
			t.Fatalf("reserved %d before: named %d pool %d input %d, want the table's %d", reservedBefore,
				stream.namedReserved, co.reservations.retainedBytes, stream.retainedByPhase[retainPhaseInput], table)
		}
		stream.releaseProvisional()
		if co.reservations.retainedBytes != 0 || stream.namedReserved != 0 {
			t.Fatalf("after the Slot: pool %d named %d", co.reservations.retainedBytes, stream.namedReserved)
		}
	}
}
