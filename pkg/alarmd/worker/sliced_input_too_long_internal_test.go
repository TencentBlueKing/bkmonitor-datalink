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
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A slice whose read the store refused as too long is named for it,
// INPUT_SLICE_QUERY_TOO_LONG with the slice's condition bytes; the same
// refusal of a read of the whole Query Group is the query's own length and
// is not; a slice refused for anything else is not either.
func TestASliceTheStoreRefusesAsTooLongIsNamedForIt(t *testing.T) {
	slice := execution.InputSlice{Conditions: []execution.InputSliceCondition{{Dimension: "host", Pattern: "^(?s:.*)[0-3]$"}}}
	refused := func(class string) execution.QueryExecutionCompletion {
		return execution.QueryExecutionCompletion{PhysicalQueries: []execution.PhysicalQueryCompletion{{
			RouteFacts: execution.ProviderRouteFacts{Attempts: []execution.RouteAttemptFact{{AttemptNo: 1,
				Rejection: &execution.ProviderRejection{Class: class}}}}}}}
	}
	err := sliceQueryTooLong(slice, refused(execution.RejectionClassQueryTooLong))
	if word, named := InputSliceRefusal(err); !named || word != contract.ReasonInputSliceQueryTooLong {
		t.Fatalf("err %v named %q, want INPUT_SLICE_QUERY_TOO_LONG", err, word)
	}
	if tooLong, ok := err.(*inputSliceQueryTooLongError); !ok || tooLong.bytes != slice.ConditionBytes() {
		t.Fatalf("err %#v, want the slice's %d condition bytes on it", err, slice.ConditionBytes())
	}
	// Read in slices, it is the refusal the scheduler finalizes the Slot on,
	// under its own code and the slice's bytes; not read in slices, the
	// error stays as it is.
	coordinator, _ := shareFixture(1<<20, 0, 0)
	sliced := &streamedExecution{coordinator: coordinator, slices: &sliceReading{}}
	if refusal, final := execution.ShareRefusalOf(sliced.finalSliceRefusal(err)); !final ||
		refusal.Reason != contract.ReasonInputSliceQueryTooLong || refusal.Detail == "" || refusal.LimitBytes != coordinator.qgShareBytes() {
		t.Fatalf("final refusal = %+v, %t; want INPUT_SLICE_QUERY_TOO_LONG with the slice's bytes", refusal, final)
	}
	if _, final := execution.ShareRefusalOf((&streamedExecution{coordinator: coordinator}).finalSliceRefusal(err)); final {
		t.Fatal("a refusal of a Slot not read in slices was made final")
	}
	if err := sliceQueryTooLong(execution.InputSlice{}, refused(execution.RejectionClassQueryTooLong)); err != nil {
		t.Fatalf("a whole read refused as too long named %v, want the query's own rejection", err)
	}
	if err := sliceQueryTooLong(slice, refused(execution.RejectionClassVMSampleLimit)); err != nil {
		t.Fatalf("a slice refused for its samples named %v, want nothing of its length", err)
	}
}
