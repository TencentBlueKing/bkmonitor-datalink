// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strconv"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/racedetector"
)

// heapGrowth is how much live heap build leaves behind, after a collection
// on either side.
func heapGrowth(build func() any) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	kept := build()
	runtime.GC()
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(kept)
	if after.HeapAlloc <= before.HeapAlloc {
		// The collector freed more than the build kept: no measure.
		return 0
	}
	return after.HeapAlloc - before.HeapAlloc
}

// A named-output table is charged at least what it measures on the heap,
// and not much more: from one output to four, records keyed by RecordIDs
// of their real length, each value its own allocation, as the decoder
// builds them. The charge is what a Slot reserves for it, so an estimate
// under the measure would let a large table past the share unseen.
func TestANamedOutputTableCostsWhatItIsCharged(t *testing.T) {
	if racedetector.Enabled {
		t.Skip("the race detector changes what an allocation costs")
	}
	const records = 20000
	for outputs := 1; outputs <= MaxNamedOutputs; outputs++ {
		var table *NamedOutputRecords
		measured := heapGrowth(func() any {
			table = &NamedOutputRecords{Refs: []string{"A", "B", "C", "RESULT"}[:outputs],
				Records: make(map[string][]NamedOutputValue)}
			for record := 0; record < records; record++ {
				values := make([]NamedOutputValue, outputs)
				for output := range values {
					values[output] = NamedOutputValue{Value: json.RawMessage(strconv.Itoa(record*7 + output)), State: NamedOutputSuccess}
				}
				table.Records[fmt.Sprintf("%064x", record)] = values
			}
			return table
		})
		if measured == 0 {
			t.Fatalf("%d outputs: the heap did not grow across the build, nothing was measured", outputs)
		}
		charged := table.RetainedBytes()
		t.Logf("%d outputs: measured %d bytes a record, charged %d", outputs, measured/records, charged/records)
		if charged < measured || charged > measured+measured/4 {
			t.Errorf("%d outputs: charged %d bytes, measured %d: want at least the measure and within a quarter of it",
				outputs, charged, measured)
		}
	}
	if (*NamedOutputRecords)(nil).RetainedBytes() != 0 {
		t.Fatal("no table is charged")
	}
}
