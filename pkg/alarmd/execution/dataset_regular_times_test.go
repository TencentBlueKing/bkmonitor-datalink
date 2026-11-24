// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// A series answered one point a step keeps its first time and its step, not
// a time a point: thirty points hold 8 bytes a point fewer than thirty at
// uneven times. Either way every point reads back its own time, from a
// record, from Records and as the time its derived id is derived from; and a
// run with one point out of step keeps every time, the ones in step included.
func TestASeriesOneAStepKeepsItsFirstTimeAndStepAndEveryPointReadsItsOwn(t *testing.T) {
	values := make([]string, 30)
	for index := range values {
		values[index] = "1.5"
	}
	regular := derivedSeries(digestA, 1757462400, values...)
	uneven := derivedSeries(digestA, 1757462400, values...)
	uneven[17].SourceTime += 7
	id, err := contract.DeriveRecordIDV2(digestA, uneven[17].SourceTime)
	if err != nil {
		t.Fatal(err)
	}
	uneven[17].RecordID = id

	for name, records := range map[string][]contract.CanonicalRecordV2{"one a step": regular, "one out of step": uneven} {
		dataset := NewDataset(records)
		for index := range records {
			record, ok := dataset.Record(index)
			if !ok || record.SourceTime() != records[index].SourceTime || record.RecordID() != records[index].RecordID {
				t.Fatalf("%s: point %d reads time %d id %q, want %d %q", name, index, record.SourceTime(), record.RecordID(),
					records[index].SourceTime, records[index].RecordID)
			}
		}
		for index, record := range dataset.Records() {
			if record.SourceTime != records[index].SourceTime || record.RecordID != records[index].RecordID {
				t.Fatalf("%s: Records()[%d] = %d %q, want %d %q", name, index, record.SourceTime, record.RecordID,
					records[index].SourceTime, records[index].RecordID)
			}
		}
	}
	if got, want := NewDataset(uneven).RetainedBytes()-NewDataset(regular).RetainedBytes(), uint64(8*len(values)); got != want {
		t.Fatalf("thirty points out of step hold %d more than one a step, want a time a point, %d", got, want)
	}
}
