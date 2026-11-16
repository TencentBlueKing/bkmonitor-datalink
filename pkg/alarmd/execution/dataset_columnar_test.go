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
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// columnarSeries is one series as the UQ decoder builds it: every record
// pointing at one field list and one dimensions map, one value field, the
// values as the answer wrote them.
func columnarSeries(digest string, firstTime int64, values ...string) []contract.CanonicalRecordV2 {
	fields := []contract.DimensionFieldV2{{Name: "bk_target_ip", Value: json.RawMessage(`"192.0.2.10"`)},
		{Name: "device_name", Value: json.RawMessage(`"cpu0"`)}}
	dimensions := map[string]json.RawMessage{"bk_target_ip": json.RawMessage(`"192.0.2.10"`), "device_name": json.RawMessage(`"cpu0"`)}
	records := make([]contract.CanonicalRecordV2, len(values))
	for index, value := range values {
		at := firstTime + int64(index)*60
		records[index] = contract.CanonicalRecordV2{RecordID: fmt.Sprintf("%s-%010d", digest[:8], at), SourceTime: at, BusinessID: "2",
			DimensionIdentity: contract.DimensionIdentityV2{Fields: fields, Digest: digest},
			Values:            map[string]json.RawMessage{"_result_": json.RawMessage(value)},
			Dimensions:        dimensions, ReceivedTime: 1757462999}
	}
	return records
}

const (
	digestA = "9f2a7c1e5b3d8f0a4c6e2b9d7f1a3c5e9f2a7c1e5b3d8f0a4c6e2b9d7f1a3c5e"
	digestB = "3a7d2e0f5b8c1d4e7f0a3b6c9d2e5f8a3a7d2e0f5b8c1d4e7f0a3b6c9d2e5f8a"
)

// Every value an answer can carry reads back as the bytes it was given, from
// every reader, whether it is kept as a number or in the side table: plain
// numbers, a negative zero, exponents as the encoder writes them and as it
// does not, a trailing zero, an integer past float64's precision, a string,
// null, true.
func TestAColumnarRecordReadsBackAsItWasGiven(t *testing.T) {
	values := []string{`12.5`, `0`, `-0`, `-3`, `1e-7`, `1e21`, `1.5e+300`, `1e3`, `100.0`, `9007199254740993`,
		`0.1`, `123456789012345680000`, `"up"`, `null`, `true`, `""`}
	records := columnarSeries(digestA, 1757462400, values...)
	dataset := NewDataset(records)
	if len(dataset.runs) != 1 || dataset.runs[0].records != nil {
		t.Fatalf("one series kept as %d runs (whole: %v); want one columnar run", len(dataset.runs), dataset.runs[0].records != nil)
	}
	if !reflect.DeepEqual(dataset.Records(), records) {
		t.Fatal("Records() differ from the records given")
	}
	for index, want := range records {
		record, ok := dataset.Record(index)
		if !ok {
			t.Fatalf("record %d is missing", index)
		}
		value, found := record.Value("_result_")
		appended, appendedFound := record.AppendValue([]byte("x"), "_result_")
		if !found || string(value) != values[index] || !appendedFound || string(appended) != "x"+values[index] ||
			!reflect.DeepEqual(record.Values(), want.Values) {
			t.Fatalf("value %d read back as %q / %q / %v; want %s", index, value, appended, record.Values(), values[index])
		}
		if record.RecordID() != want.RecordID || record.SourceTime() != want.SourceTime || record.BusinessID() != want.BusinessID ||
			record.ReceivedTime() != want.ReceivedTime || record.CollectionTime() != nil ||
			!reflect.DeepEqual(record.DimensionIdentity(), want.DimensionIdentity) ||
			record.DimensionIdentityDigest() != want.DimensionIdentity.Digest || !reflect.DeepEqual(record.Dimensions(), want.Dimensions) {
			t.Fatalf("record %d reads back differently from the record given", index)
		}
		if _, other := record.Value("other"); other {
			t.Fatalf("record %d has a value it was not given", index)
		}
	}
}

// Records that are not one series' run are kept as given, in order, beside
// the runs that are: a second series, a record with two values, one with a
// collection time, and a record id of another length each start their own
// run, and every record reads back as given.
func TestRecordsOfNoRunAreKeptWholeInOrder(t *testing.T) {
	collected := int64(1757462350)
	twoValues := columnarSeries(digestA, 1757463000, `1`)[0]
	twoValues.Values = map[string]json.RawMessage{"_result_": json.RawMessage(`1`), "other": json.RawMessage(`2`)}
	withCollection := columnarSeries(digestA, 1757463060, `2`)[0]
	withCollection.CollectionTime = &collected
	otherID := columnarSeries(digestA, 1757463120, `3`)[0]
	otherID.RecordID += "-longer"
	var records []contract.CanonicalRecordV2
	records = append(records, columnarSeries(digestA, 1757462400, `1`, `2`, `3`)...)
	records = append(records, columnarSeries(digestB, 1757462400, `4`, `5`)...)
	records = append(records, twoValues, withCollection, otherID)
	records = append(records, columnarSeries(digestB, 1757463400, `6`)...)
	dataset := NewDataset(records)
	if len(dataset.runs) != 5 {
		t.Fatalf("%d runs; want two series, the two records kept whole together, the longer id and the last series", len(dataset.runs))
	}
	if dataset.Len() != len(records) || !reflect.DeepEqual(dataset.Records(), records) {
		t.Fatal("the records do not read back as given, in order")
	}
	for index, want := range records {
		record, _ := dataset.Record(index)
		if record.RecordID() != want.RecordID || !reflect.DeepEqual(record.Values(), want.Values) ||
			!reflect.DeepEqual(record.CollectionTime(), want.CollectionTime) {
			t.Fatalf("record %d reads back as %s %v, want %s %v", index, record.RecordID(), record.Values(), want.RecordID, want.Values)
		}
	}
	if _, ok := dataset.Record(len(records)); ok {
		t.Fatal("a record past the end")
	}
}

// A columnar Dataset stays immutable: writing to what a reader handed back
// reaches neither the Dataset nor the records it was built from.
func TestAColumnarDatasetHandsOutCopies(t *testing.T) {
	records := columnarSeries(digestA, 1757462400, `12.5`, `"up"`)
	dataset := NewDataset(records)
	for index := range records {
		record, _ := dataset.Record(index)
		record.Values()["_result_"][0] = 'X'
		record.Dimensions()["device_name"][1] = 'X'
		record.DimensionIdentity().Fields[0].Value[1] = 'X'
		if value, _ := record.Value("_result_"); len(value) > 0 {
			value[0] = 'X'
		}
	}
	records[0].Dimensions["device_name"][1] = 'Y'
	again := columnarSeries(digestA, 1757462400, `12.5`, `"up"`)
	for index := range again {
		record, _ := dataset.Record(index)
		if !reflect.DeepEqual(record.Values(), again[index].Values) || !reflect.DeepEqual(record.Dimensions(), again[index].Dimensions) ||
			!reflect.DeepEqual(record.DimensionIdentity(), again[index].DimensionIdentity) {
			t.Fatalf("record %d changed after its readers' copies or its source were written", index)
		}
	}
}

// A columnar run counts the series' parts once and eight bytes a point for
// the value beside the record id, and eight for the time when the points are
// not one a step: a series of 30 points costs less than half of the same
// records kept whole, and each further point adds its columns and its id,
// not another record. These ids are not the derived ones, so the run keeps
// them; a run whose ids are derived keeps none, and a further point adds its
// columns alone.
func TestAColumnarRunCountsItsSeriesOnceAndItsPointsAsColumns(t *testing.T) {
	values := make([]string, 30)
	for index := range values {
		values[index] = fmt.Sprintf("%d.5", index)
	}
	records := columnarSeries(digestA, 1757462400, values...)
	columnarBytes := NewDataset(records).RetainedBytes()
	whole := &Dataset{len: len(records), starts: []int{0}, runs: []datasetRun{{records: records}}}
	if wholeBytes := whole.RetainedBytes(); columnarBytes*2 >= wholeBytes {
		t.Fatalf("columnar %d bytes, whole %d; want the columnar run under half", columnarBytes, wholeBytes)
	}
	// One point a step: the run keeps its first time and its step, so a
	// further point is its value (8 bytes) and, kept, its id; out of step it
	// is its time (8 bytes) too.
	longer := NewDataset(columnarSeries(digestA, 1757462400, append(values, "30.5")...)).RetainedBytes()
	if step, want := longer-columnarBytes, uint64(8+len(records[0].RecordID)); step != want {
		t.Fatalf("one more point added %d bytes, want %d: its value and its id", step, want)
	}
	uneven := func(records []contract.CanonicalRecordV2) []contract.CanonicalRecordV2 {
		records[1].SourceTime += 7
		return records
	}
	unevenBytes := NewDataset(uneven(columnarSeries(digestA, 1757462400, values...))).RetainedBytes()
	unevenLonger := NewDataset(uneven(columnarSeries(digestA, 1757462400, append(values, "30.5")...))).RetainedBytes()
	if step, want := unevenLonger-unevenBytes, uint64(16+len(records[0].RecordID)); step != want {
		t.Fatalf("one more point out of step added %d bytes, want %d: its time, its value and its id", step, want)
	}
	derivedBytes := NewDataset(derivedSeries(digestA, 1757462400, values...)).RetainedBytes()
	derivedLonger := NewDataset(derivedSeries(digestA, 1757462400, append(values, "30.5")...)).RetainedBytes()
	if step := derivedLonger - derivedBytes; step != 8 {
		t.Fatalf("one more point of a derived run added %d bytes, want 8: its value", step)
	}
	if columnarBytes-derivedBytes != uint64(len(records)*len(records[0].RecordID)) {
		t.Fatalf("a derived run costs %d, the run keeping its ids %d; want the ids' bytes apart", derivedBytes, columnarBytes)
	}
}

// A value kept in the side table counts its bytes and its entry: the same
// series with string values for numbers costs at least the strings more.
func TestASideTableValueCountsItsBytes(t *testing.T) {
	long := `"` + strings.Repeat("a", 500) + `"`
	short := NewDataset(columnarSeries(digestA, 1757462400, `"a"`, `"b"`, `3`)).RetainedBytes()
	longer := NewDataset(columnarSeries(digestA, 1757462400, long, long, `3`)).RetainedBytes()
	if want := uint64(2 * (len(long) - len(`"a"`))); longer-short != want {
		t.Fatalf("two strings of %d bytes cost %d more than two of %d, want their %d bytes more", len(long), longer-short, len(`"a"`), want)
	}
}

// An identity field that is a dimension of the same bytes is counted once:
// a series whose identity is its dimensions costs a value's bytes less than
// the same series whose identity value differs from its dimension.
func TestAnIdentityFieldThatIsADimensionIsCountedOnce(t *testing.T) {
	value := json.RawMessage(`"` + strings.Repeat("x", 1000) + `"`)
	other := json.RawMessage(`"` + strings.Repeat("y", 1000) + `"`)
	series := func(dimension json.RawMessage) []contract.CanonicalRecordV2 {
		fields := []contract.DimensionFieldV2{{Name: "tag", Value: value}}
		dimensions := map[string]json.RawMessage{"tag": dimension}
		return []contract.CanonicalRecordV2{{RecordID: "r1", SourceTime: 60, BusinessID: "2",
			DimensionIdentity: contract.DimensionIdentityV2{Fields: fields, Digest: digestA},
			Values:            map[string]json.RawMessage{"_result_": json.RawMessage(`1`)}, Dimensions: dimensions}}
	}
	shared, apart := NewDataset(series(value)), NewDataset(series(other))
	if got, want := apart.RetainedBytes()-shared.RetainedBytes(), uint64(len(value)); got != want {
		t.Fatalf("an identity apart from its dimension cost %d more, want its %d bytes", got, want)
	}
	record, _ := shared.Record(0)
	if !reflect.DeepEqual(record.DimensionIdentity(), series(value)[0].DimensionIdentity) ||
		!reflect.DeepEqual(record.Dimensions(), series(value)[0].Dimensions) {
		t.Fatal("a shared value reads back differently")
	}
}
