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
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// derivedSeries is columnarSeries with every id the one derived from the
// series and the point's time, as the UQ decoder makes them.
func derivedSeries(digest string, firstTime int64, values ...string) []contract.CanonicalRecordV2 {
	records := columnarSeries(digest, firstTime, values...)
	for index := range records {
		id, err := contract.DeriveRecordIDV2(digest, records[index].SourceTime)
		if err != nil {
			panic(err)
		}
		records[index].RecordID = id
	}
	return records
}

// recordIDCounts is the three counts, read together.
func recordIDCounts() (kept, outside, inWindow uint64) {
	return keptRecordIDRuns.Load(), recordIDsDerivedOutside.Load(), recordIDsDerivedInWindow.Load()
}

// A run whose ids are the derived ones keeps none, checked or stated, and
// counts no run that keeps them. In its window every reader reads back the
// records as given, ids included, without deriving any id outside it.
func TestADerivedRunKeepsNoIDsAndReadsThemBackInItsWindow(t *testing.T) {
	records := derivedSeries(digestA, 1757462400, `1`, `2.5`, `"up"`, `null`)
	for name, build := range map[string]func([]contract.CanonicalRecordV2) *Dataset{
		"checked": NewDataset, "stated": NewDatasetOfDerivedRecordIDs,
	} {
		keptBefore, outsideBefore, _ := recordIDCounts()
		dataset := build(records)
		if len(dataset.runs) != 1 || !dataset.runs[0].derived || dataset.runs[0].ids != "" {
			t.Fatalf("%s: %d runs, derived %v, %d id bytes kept; want one run keeping no ids", name, len(dataset.runs),
				dataset.runs[0].derived, len(dataset.runs[0].ids))
		}
		if _, err := dataset.MaterializeRecordIDs(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(dataset.Records(), records) {
			t.Fatalf("%s: Records() differ from the records given", name)
		}
		for index, want := range records {
			record, _ := dataset.Record(index)
			if record.RecordID() != want.RecordID {
				t.Fatalf("%s: record %d reads id %s, want %s", name, index, record.RecordID(), want.RecordID)
			}
		}
		if kept, outside, _ := recordIDCounts(); kept != keptBefore || outside != outsideBefore {
			t.Fatalf("%s: %d runs kept their ids and %d ids were derived outside a window; want none", name,
				kept-keptBefore, outside-outsideBefore)
		}
		dataset.DropRecordIDs()
	}
}

// A run with one id that is not the derived one keeps every id of the run,
// as a run did before ids were derived, is counted, and reads back exactly
// what it was given, in a window or not, deriving nothing.
func TestARunWithAnIDNotDerivedKeepsThemAllAndIsCounted(t *testing.T) {
	records := derivedSeries(digestA, 1757462400, `1`, `2`, `3`)
	records[1].RecordID = records[1].RecordID[:63] + "x"
	keptBefore, outsideBefore, inWindowBefore := recordIDCounts()
	dataset := NewDataset(records)
	if len(dataset.runs) != 1 || dataset.runs[0].derived || dataset.runs[0].ids == "" {
		t.Fatal("a run with an id not derived kept no ids, or was split")
	}
	if kept, _, _ := recordIDCounts(); kept-keptBefore != 1 {
		t.Fatalf("%d runs counted as keeping their ids, want 1", kept-keptBefore)
	}
	if window := dataset.RecordIDWindowBytes(); window != 0 {
		t.Fatalf("a run keeping its ids wants a window buffer of %d bytes, want none", window)
	}
	if added, err := dataset.MaterializeRecordIDs(); err != nil || added != 0 {
		t.Fatalf("materializing a run keeping its ids added %d bytes (%v), want none", added, err)
	}
	if !reflect.DeepEqual(dataset.Records(), records) {
		t.Fatal("Records() differ from the records given")
	}
	for index, want := range records {
		record, _ := dataset.Record(index)
		if record.RecordID() != want.RecordID {
			t.Fatalf("record %d reads id %s, want %s", index, record.RecordID(), want.RecordID)
		}
	}
	if _, outside, inWindow := recordIDCounts(); outside != outsideBefore || inWindow != inWindowBefore {
		t.Fatalf("a run keeping its ids derived %d ids outside a window and %d in one, want none", outside-outsideBefore, inWindow-inWindowBefore)
	}
}

// Outside a window a derived run still answers the right id, derived for
// the call, and counts each one so that a reader there shows.
func TestAnIDReadOutsideAWindowIsDerivedAndCounted(t *testing.T) {
	records := derivedSeries(digestA, 1757462400, `1`, `2`, `3`)
	dataset := NewDatasetOfDerivedRecordIDs(records)
	_, outsideBefore, _ := recordIDCounts()
	for index, want := range records {
		record, _ := dataset.Record(index)
		if record.RecordID() != want.RecordID {
			t.Fatalf("record %d reads id %s outside a window, want %s", index, record.RecordID(), want.RecordID)
		}
	}
	if _, outside, _ := recordIDCounts(); outside-outsideBefore != uint64(len(records)) {
		t.Fatalf("%d ids counted as derived outside a window, want %d", outside-outsideBefore, len(records))
	}
	if !reflect.DeepEqual(dataset.Records(), records) {
		t.Fatal("Records() outside a window differ from the records given")
	}
	if _, outside, _ := recordIDCounts(); outside-outsideBefore != 2*uint64(len(records)) {
		t.Fatalf("Records() outside a window counted %d ids, want %d", outside-outsideBefore-uint64(len(records)), len(records))
	}
}

// A window derives each point's id once, however often it is read and
// however often the window asks; the bytes it reports are the ones it
// asked for first, and dropping the buffer gives them back and sends the
// next read outside a window again.
func TestAWindowDerivesEachIDOnce(t *testing.T) {
	first := derivedSeries(digestA, 1757462400, `1`, `2`, `3`)
	second := derivedSeries(digestB, 1757462400, `4`, `5`)
	dataset := NewDatasetOfDerivedRecordIDs(append(append([]contract.CanonicalRecordV2(nil), first...), second...))
	points := len(first) + len(second)
	want := uint64(points)*recordIDLength + 2*recordIDWindowRunBytes
	if bytes := dataset.RecordIDWindowBytes(); bytes != want {
		t.Fatalf("a window of two runs of %d points wants %d bytes, want %d", points, bytes, want)
	}
	_, outsideBefore, inWindowBefore := recordIDCounts()
	added, err := dataset.MaterializeRecordIDs()
	if err != nil || added != want {
		t.Fatalf("materializing added %d bytes (%v), want %d", added, err, want)
	}
	if again, err := dataset.MaterializeRecordIDs(); err != nil || again != 0 || dataset.RecordIDWindowBytes() != 0 {
		t.Fatalf("materializing again added %d bytes (%v), want none", again, err)
	}
	for round := 0; round < 3; round++ {
		for index := 0; index < dataset.Len(); index++ {
			record, _ := dataset.Record(index)
			_ = record.RecordID()
		}
	}
	if _, outside, inWindow := recordIDCounts(); inWindow-inWindowBefore != uint64(points) || outside != outsideBefore {
		t.Fatalf("a window derived %d ids for %d points and %d outside it; want each point once, none outside",
			inWindow-inWindowBefore, points, outside-outsideBefore)
	}
	if dropped := dataset.DropRecordIDs(); dropped != want {
		t.Fatalf("dropping gave back %d bytes, want %d", dropped, want)
	}
	if dropped := dataset.DropRecordIDs(); dropped != 0 {
		t.Fatalf("dropping again gave back %d bytes, want none", dropped)
	}
	record, _ := dataset.Record(0)
	if record.RecordID() != first[0].RecordID {
		t.Fatal("a dropped window reads another id")
	}
	if _, outside, _ := recordIDCounts(); outside-outsideBefore != 1 {
		t.Fatalf("a read after the window was dropped counted %d, want 1", outside-outsideBefore)
	}
}
