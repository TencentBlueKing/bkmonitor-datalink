// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package uq

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The legacy points of a named-outputs query keep the ids the decoder
// derived as it built the records, not ids read back from the Dataset,
// which keeps none: decoding the answer derives no id outside a window, and
// every aligned record is keyed by the id derived from its series and time,
// the one the window reads back.
func TestNamedOutputsKeepTheIDsTheDecoderDerived(t *testing.T) {
	_, outsideBefore := execution.DatasetRecordIDCounts()
	completion, sink := decodeNamed(t, namedAttempt(t, namedOutputsAB), reservingAnswer())
	if _, outsideAfter := execution.DatasetRecordIDCounts(); outsideAfter != outsideBefore {
		t.Fatalf("decoding derived %d ids outside a window, want none: the points take the decoder's", outsideAfter-outsideBefore)
	}
	if completion.NamedOutputs == nil || len(completion.NamedOutputs.Records) != 2 || len(sink.batches) != 1 {
		t.Fatalf("named outputs %+v, %d batches; want the legacy series' two records", completion.NamedOutputs, len(sink.batches))
	}
	dataset := sink.batches[0].Dataset
	if dataset.RecordIDWindowBytes() == 0 {
		t.Fatal("the decoder's Dataset keeps its ids, want them derived")
	}
	if _, err := dataset.MaterializeRecordIDs(); err != nil {
		t.Fatal(err)
	}
	defer dataset.DropRecordIDs()
	for index := 0; index < dataset.Len(); index++ {
		record, _ := dataset.Record(index)
		want, err := contract.DeriveRecordIDV2(record.DimensionIdentityDigest(), record.SourceTime())
		if err != nil {
			t.Fatal(err)
		}
		if record.RecordID() != want {
			t.Fatalf("record %d reads id %s in its window, want %s", index, record.RecordID(), want)
		}
		if _, found := completion.NamedOutputs.Records[want]; !found {
			t.Fatalf("record %d (%s) has no aligned outputs; the points were keyed by other ids", index, want)
		}
	}
}
