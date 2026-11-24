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
	"encoding/json"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// derivedDataset is points of one series as the UQ decoder keeps them: every
// id derived, none kept.
func derivedDataset(t *testing.T, digest string, points int) *execution.Dataset {
	t.Helper()
	records := make([]contract.CanonicalRecordV2, points)
	for index := range records {
		at := int64(1_788_000_000 - 60*(points-index))
		id, err := contract.DeriveRecordIDV2(digest, at)
		if err != nil {
			t.Fatal(err)
		}
		records[index] = contract.CanonicalRecordV2{RecordID: id, SourceTime: at, BusinessID: "2",
			DimensionIdentity: contract.DimensionIdentityV2{Digest: digest},
			Values:            map[string]json.RawMessage{"value": json.RawMessage(`1`)}, ReceivedTime: 1_788_000_000}
	}
	return execution.NewDatasetOfDerivedRecordIDs(records)
}

// A series' record ids are derived into its Datasets' buffers before it is
// evaluated, charged to the input phase and to the window, and read from
// there; a Dataset two series of the window read is derived once. When the
// window lands the buffers of its series go and give their bytes back, and
// a buffer a series of the next window also read stays, its bytes that
// window's.
func TestAWindowsRecordIDsAreDerivedOnceChargedAndLetGoWithIt(t *testing.T) {
	fixture := newWindowedFixture(t)
	stream := fixture.stream
	for _, name := range []string{"series-a", "series-b", "series-c"} {
		fixture.streamInput(t, name, 1_000)
	}
	first := derivedDataset(t, strings.Repeat("a", 64), 5)
	shared := derivedDataset(t, strings.Repeat("b", 64), 3)
	// Series a reads first; b and c both read shared, as one series digest
	// feeding two Plans' series does.
	stream.windows.prepared[0].inputs[0].Inputs[0].Dataset = first
	stream.windows.prepared[1].inputs[0].Inputs[0].Dataset = shared
	stream.windows.prepared[2].inputs[0].Inputs[0].Dataset = shared
	firstBytes, sharedBytes := first.RecordIDWindowBytes(), shared.RecordIDWindowBytes()
	if firstBytes == 0 || sharedBytes == 0 {
		t.Fatal("the Datasets keep their ids; want them derived")
	}
	_, outsideBefore := execution.DatasetRecordIDCounts()
	input, windowBytes := stream.retainedByPhase[retainPhaseInput], stream.windows.bytes
	for position := range stream.windows.prepared {
		if err := stream.materializeRecordIDs(context.Background(), position, stream.windows.prepared[position].inputs); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := stream.retainedByPhase[retainPhaseInput]-input, firstBytes+sharedBytes; got != want {
		t.Fatalf("the buffers were charged %d to the input phase, want %d: each Dataset once", got, want)
	}
	if got, want := stream.windows.bytes-windowBytes, firstBytes+sharedBytes; got != want {
		t.Fatalf("the window counts %d of the buffers, want %d", got, want)
	}
	for _, dataset := range []*execution.Dataset{first, shared} {
		for index := 0; index < dataset.Len(); index++ {
			record, _ := dataset.Record(index)
			want, _ := contract.DeriveRecordIDV2(record.DimensionIdentityDigest(), record.SourceTime())
			if record.RecordID() != want {
				t.Fatalf("record %d reads %s in its window, want %s", index, record.RecordID(), want)
			}
		}
	}
	if _, outside := execution.DatasetRecordIDCounts(); outside != outsideBefore {
		t.Fatalf("%d ids were derived outside the window, want none", outside-outsideBefore)
	}

	// The window of series a and b lands; c, which also read shared, is the
	// next window's.
	stream.windows.bytes = 0
	stream.dropWindowRecordIDs(2, false)
	if got, want := stream.retainedByPhase[retainPhaseInput], input+sharedBytes; got != want {
		t.Fatalf("input phase = %d after the window landed, want %d: only the buffer the next window reads", got, want)
	}
	if stream.windows.bytes != sharedBytes || len(stream.windows.recordIDs) != 1 || first.RecordIDWindowBytes() != firstBytes ||
		shared.RecordIDWindowBytes() != 0 {
		t.Fatalf("window bytes %d, %d buffers held after the window landed; want shared's %d alone", stream.windows.bytes,
			len(stream.windows.recordIDs), sharedBytes)
	}
	stream.windows.bytes = 0
	stream.dropWindowRecordIDs(len(stream.windows.prepared), true)
	if stream.retainedByPhase[retainPhaseInput] != input || stream.windows.recordIDs != nil || shared.RecordIDWindowBytes() != sharedBytes ||
		stream.windows.bytes != 0 {
		t.Fatalf("input phase = %d after the last window, want %d and no buffer held", stream.retainedByPhase[retainPhaseInput], input)
	}
}
