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
	"encoding/json"
	"fmt"
	"testing"
	"unsafe"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// retainedSeries is a series of points points as the UQ decoder hands it on,
// with one binding a view of all of it.
func retainedSeries(t *testing.T, points int, wireBytes uint64) execution.SeriesExecutionBatch {
	t.Helper()
	digest := fmt.Sprintf("%064x", points)
	fields := []contract.DimensionFieldV2{{Name: "bk_target_ip", Value: json.RawMessage(`"192.0.2.10"`)}}
	dimensions := map[string]json.RawMessage{"bk_target_ip": json.RawMessage(`"192.0.2.10"`)}
	records := make([]contract.CanonicalRecordV2, points)
	ordinals := make([]uint32, points)
	for index := range records {
		at := int64(1_700_000_000 + index*60)
		records[index] = contract.CanonicalRecordV2{RecordID: fmt.Sprintf("%064x", at), SourceTime: at, BusinessID: "2",
			DimensionIdentity: contract.DimensionIdentityV2{Fields: fields, Digest: digest},
			Values:            map[string]json.RawMessage{"value": json.RawMessage(fmt.Sprintf("%d.5", index))},
			Dimensions:        dimensions, ReceivedTime: 1_700_000_999}
		ordinals[index] = uint32(index)
	}
	dataset := execution.NewDataset(records)
	view, err := execution.NewDatasetView(dataset, ordinals)
	if err != nil {
		t.Fatal(err)
	}
	return execution.SeriesExecutionBatch{Dataset: dataset, Delivery: execution.SeriesDelivery{Series: 1, Records: uint64(points), Bytes: wireBytes},
		Inputs: []execution.NamedInputBinding{{RequirementID: "a", Dataset: dataset, View: view}}}
}

// What a batch holds is its Dataset at its RetainedBytes, each view once and
// an entry per binding beside them: the bytes the answer encoded the series
// in are no part of it, a second binding of the same Dataset and view adds
// only its entry in the streamed map, and one of another Dataset adds that
// Dataset and its view too. The part a binding shares with its query's other
// series is charged once, when it is first kept (keepStreamed), not here.
func TestARetainedSizeIsTheDatasetOnceAndEachBindingBesideIt(t *testing.T) {
	batch := retainedSeries(t, 15, 4096)
	size := streamedRetainedSize(batch)
	view := uint64(unsafe.Sizeof(execution.DatasetView{})) + 4*15
	entry := (uint64(unsafe.Sizeof(streamedInputKey{})+unsafe.Sizeof(streamedInput{})) + 1) * 8 / 7
	if want := batch.Dataset.RetainedBytes() + view + entry; size != want {
		t.Fatalf("a batch of one binding holds %d, want its Dataset %d, its view %d and one entry %d", size, batch.Dataset.RetainedBytes(), view, entry)
	}
	if wider := retainedSeries(t, 15, 1<<20); streamedRetainedSize(wider) != size {
		t.Fatalf("the same series encoded in 1 MiB holds %d, in 4 KiB %d; want the wire bytes no part of it", streamedRetainedSize(wider), size)
	}
	again := batch
	again.Inputs = append([]execution.NamedInputBinding{batch.Inputs[0]}, batch.Inputs[0])
	if got := streamedRetainedSize(again) - size; got != entry {
		t.Fatalf("a second binding of the same Dataset and view added %d; want its entry, %d", got, entry)
	}
	other := retainedSeries(t, 30, 4096)
	apart := batch
	apart.Inputs = append([]execution.NamedInputBinding{batch.Inputs[0]}, other.Inputs[0])
	if got, want := streamedRetainedSize(apart)-size, other.Dataset.RetainedBytes()+uint64(unsafe.Sizeof(execution.DatasetView{}))+4*30+entry; got != want {
		t.Fatalf("a binding of another Dataset added %d, want that Dataset, its view and an entry, %d", got, want)
	}
}

// A binding's facts are held with the part it shares: the room its quality
// facts and terminals keep is counted at their size, once for every series
// of the query that shares them, and a series' own charge carries none of it.
func TestABindingsFactsAreCountedWithIt(t *testing.T) {
	batch := retainedSeries(t, 1, 0)
	size := streamedRetainedSize(batch)
	plain := sharedBindingHeldBytes(&batch.Inputs[0])
	batch.Inputs[0].QualityFacts = make([]execution.InputQualityFact, 0, 10)
	batch.Inputs[0].Terminals = make([]execution.InputTerminal, 3)
	want := 10*uint64(unsafe.Sizeof(execution.InputQualityFact{})) + 3*uint64(unsafe.Sizeof(execution.InputTerminal{}))
	if got := sharedBindingHeldBytes(&batch.Inputs[0]) - plain; got != want {
		t.Fatalf("ten quality facts and three terminals added %d to the shared binding, want %d", got, want)
	}
	if got := streamedRetainedSize(batch); got != size {
		t.Fatalf("the facts added %d to the series' own charge, want none", got-size)
	}
	var shared sharedBindings
	first, fresh := shared.share(batch.Inputs[0])
	second := retainedSeries(t, 1, 0).Inputs[0]
	second.QualityFacts, second.Terminals = make([]execution.InputQualityFact, 0, 10), make([]execution.InputTerminal, 3)
	again, freshAgain := shared.share(second)
	if !fresh || freshAgain || first != again || len(shared.all) != 1 {
		t.Fatalf("two series with the same facts kept %d shared bindings (fresh %v, %v), want one, kept once", len(shared.all), fresh, freshAgain)
	}
}
