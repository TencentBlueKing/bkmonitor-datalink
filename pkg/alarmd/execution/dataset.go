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
	"errors"
	"sort"
	"sync/atomic"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// Dataset is immutable after construction. Multiple Plans may share one
// Dataset safely without exposing writable records, maps or RawMessage bytes.
//
// It keeps its records in runs. A run of consecutive records of one series -
// one business, one dimension identity and dimensions, one received time, no
// collection time and one value field - keeps those once and its points as
// columns: source times and values, a value that is a JSON number kept as
// the float64 it reads back to byte for byte, any other value (a string,
// null, a number written another way) kept as its bytes in a side table. A
// record of no such run is kept as it was given. RecordView builds the
// record's maps from the run when asked, so readers see what NewDataset was
// given either way.
//
// A run's record ids are not kept when every one of them is the id
// contract.DeriveRecordIDV2 derives from the series and the point's source
// time, which is what every id the UQ decoder makes is (decision-020 batch
// 8, trade-off two): 64 bytes a point less, held for as long as the Slot
// holds its inputs. They are derived once a point into a buffer when the
// window that reads the series loads (MaterializeRecordIDs) and let go with
// the window (DropRecordIDs); RecordID reads that buffer. A run any of whose
// ids is another keeps them all, end to end, as before.
type Dataset struct {
	runs []datasetRun
	// starts is each run's first record, for Record's search.
	starts []int
	len    int
}

// datasetRun is a columnar run, or records kept whole when records is set.
type datasetRun struct {
	records []contract.CanonicalRecordV2

	business   string
	identity   contract.DimensionIdentityV2
	dimensions map[string]json.RawMessage
	received   int64
	valueField string
	// ids holds the run's record ids end to end, idWidth bytes each, when
	// they are kept; empty when derived is set.
	ids     string
	idWidth int32
	// derived is set when every point's id is DeriveRecordIDV2(the run's
	// identity digest, its source time), and the ids are not kept. window is
	// then the ids derived end to end for the window reading the run, nil
	// outside one.
	derived bool
	window  atomic.Pointer[string]
	// times is each point's source time, nil when they are first, first +
	// step, ... one a step: the regular run a query at one step answers,
	// which then keeps the two and no time a point.
	times       []int64
	first, step int64
	values      []float64
	// sideMarks marks the points whose value is in side, one bit each; nil
	// when every value is a number.
	sideMarks []uint64
	side      []datasetSideValue
}

// datasetSideValue is a point's value kept as its bytes, by its offset in the
// run.
type datasetSideValue struct {
	offset int
	raw    string
}

// DatasetView is an immutable selector over one immutable Dataset.
type DatasetView struct {
	dataset  *Dataset
	ordinals []uint32
}

func NewDatasetView(dataset *Dataset, ordinals []uint32) (*DatasetView, error) {
	if dataset == nil {
		return nil, errors.New("alarmd execution: Dataset view requires a Dataset")
	}
	cloned := append([]uint32(nil), ordinals...)
	for index, ordinal := range cloned {
		if int(ordinal) >= dataset.Len() {
			return nil, errors.New("alarmd execution: Dataset view ordinal is outside its Dataset")
		}
		if index > 0 && ordinal <= cloned[index-1] {
			return nil, errors.New("alarmd execution: Dataset view ordinals must be unique and increasing")
		}
	}
	return &DatasetView{dataset: dataset, ordinals: cloned}, nil
}

func (view *DatasetView) Len() int {
	if view == nil {
		return 0
	}
	return len(view.ordinals)
}

func (view *DatasetView) Record(index int) (RecordView, bool) {
	if view == nil || index < 0 || index >= len(view.ordinals) {
		return RecordView{}, false
	}
	return view.dataset.Record(int(view.ordinals[index]))
}

func (view *DatasetView) Uses(dataset *Dataset) bool {
	return view != nil && view.dataset == dataset
}

// NewDataset keeps records as given. A columnar run's ids are checked
// against the ids derived from its series, point by point: a run whose every
// id is the derived one keeps none, and a run with any other id keeps them
// all (keptRecordIDRuns counts it).
func NewDataset(records []contract.CanonicalRecordV2) *Dataset {
	return newDataset(records, false)
}

// NewDatasetOfDerivedRecordIDs is NewDataset for records whose every id the
// caller has just derived as contract.DeriveRecordIDV2(the record's
// dimension identity digest, its source time) - the UQ decoder, which
// derives them as it builds the records. Checking them again would derive
// every id a second time to find what the caller already holds, so no
// columnar run keeps its ids.
func NewDatasetOfDerivedRecordIDs(records []contract.CanonicalRecordV2) *Dataset {
	return newDataset(records, true)
}

func newDataset(records []contract.CanonicalRecordV2, derived bool) *Dataset {
	dataset := &Dataset{len: len(records)}
	for start := 0; start < len(records); {
		end := start + 1
		if columnar(&records[start]) {
			for end < len(records) && sameSeriesRun(&records[start], &records[end]) {
				end++
			}
			// Filled in place: a run holds an atomic and is never copied
			// once it is in use.
			dataset.runs = append(dataset.runs, datasetRun{})
			dataset.starts = append(dataset.starts, start)
			dataset.runs[len(dataset.runs)-1].fillColumnar(records[start:end], derived)
		} else {
			for end < len(records) && !columnar(&records[end]) {
				end++
			}
			whole := make([]contract.CanonicalRecordV2, end-start)
			for index := range whole {
				whole[index] = cloneCanonicalRecord(records[start+index])
			}
			dataset.runs = append(dataset.runs, datasetRun{records: whole})
			dataset.starts = append(dataset.starts, start)
		}
		start = end
	}
	return dataset
}

// columnar is whether a record can be kept in a columnar run: one value
// field and no collection time.
func columnar(record *contract.CanonicalRecordV2) bool {
	return record.CollectionTime == nil && len(record.Values) == 1
}

// sameSeriesRun is whether next continues first's run: everything a run keeps
// once is the same, and so is the length of the record id.
func sameSeriesRun(first, next *contract.CanonicalRecordV2) bool {
	if !columnar(next) || next.BusinessID != first.BusinessID || next.ReceivedTime != first.ReceivedTime ||
		len(next.RecordID) != len(first.RecordID) || !sameIdentity(first.DimensionIdentity, next.DimensionIdentity) ||
		!sameRawMap(first.Dimensions, next.Dimensions) {
		return false
	}
	for field := range first.Values {
		_, ok := next.Values[field]
		return ok
	}
	return false
}

func sameIdentity(left, right contract.DimensionIdentityV2) bool {
	if left.Digest != right.Digest || len(left.Fields) != len(right.Fields) || (left.Fields == nil) != (right.Fields == nil) {
		return false
	}
	for index := range left.Fields {
		if left.Fields[index].Name != right.Fields[index].Name || string(left.Fields[index].Value) != string(right.Fields[index].Value) {
			return false
		}
	}
	return true
}

// sameRawMap is whether two maps hold the same entries, nil apart from empty.
func sameRawMap(left, right map[string]json.RawMessage) bool {
	if len(left) != len(right) || (left == nil) != (right == nil) {
		return false
	}
	for key, value := range left {
		other, ok := right[key]
		if !ok || string(other) != string(value) {
			return false
		}
	}
	return true
}

// fillColumnar makes run the columnar run of records. derived is the
// caller's statement that every id is the derived one; without it the ids
// are checked (recordIDsDerived).
func (run *datasetRun) fillColumnar(records []contract.CanonicalRecordV2, derived bool) {
	first := &records[0]
	run.business, run.identity = first.BusinessID, RecordView{record: first}.DimensionIdentity()
	run.dimensions, run.received, run.idWidth = cloneRawMap(first.Dimensions), first.ReceivedTime, int32(len(first.RecordID))
	run.values = make([]float64, len(records))
	run.first = records[0].SourceTime
	if len(records) > 1 {
		run.step = records[1].SourceTime - records[0].SourceTime
	}
	for index := range records {
		if records[index].SourceTime != run.first+int64(index)*run.step {
			run.times = make([]int64, len(records))
			break
		}
	}
	// An identity field is a dimension of the same name and bytes: it is kept
	// as those bytes once, the run handing either out only as a copy.
	for index, field := range run.identity.Fields {
		if value, ok := run.dimensions[field.Name]; ok && string(value) == string(field.Value) {
			run.identity.Fields[index].Value = value
		}
	}
	for field := range first.Values {
		run.valueField = field
	}
	for index := range records {
		record := &records[index]
		if run.times != nil {
			run.times[index] = record.SourceTime
		}
		raw := record.Values[run.valueField]
		if value, ok := numberOf(raw); ok {
			run.values[index] = value
			continue
		}
		if run.sideMarks == nil {
			run.sideMarks = make([]uint64, (len(records)+63)/64)
		}
		run.sideMarks[index/64] |= 1 << (index % 64)
		run.side = append(run.side, datasetSideValue{offset: index, raw: string(raw)})
	}
	if run.side != nil {
		run.side = append([]datasetSideValue(nil), run.side...)
	}
	run.derived = derived && run.idWidth == recordIDLength || recordIDsDerived(records, run.identity.Digest)
	if run.derived {
		return
	}
	keptRecordIDRuns.Add(1)
	ids := make([]byte, 0, len(records)*int(run.idWidth))
	for index := range records {
		ids = append(ids, records[index].RecordID...)
	}
	run.ids = string(ids)
}

// points is how many points a columnar run holds.
func (run *datasetRun) points() int { return len(run.values) }

// time is the source time of the run's point at offset.
func (run *datasetRun) time(offset int) int64 {
	if run.times != nil {
		return run.times[offset]
	}
	return run.first + int64(offset)*run.step
}

func (dataset *Dataset) Len() int {
	if dataset == nil {
		return 0
	}
	return dataset.len
}

// Records returns independent copies of every record in Dataset order. The
// Dataset stays immutable: callers that fold or re-shape records build a new
// Dataset from the copies.
func (dataset *Dataset) Records() []contract.CanonicalRecordV2 {
	if dataset == nil {
		return nil
	}
	records := make([]contract.CanonicalRecordV2, 0, dataset.len)
	for index := range dataset.runs {
		run := &dataset.runs[index]
		if run.records != nil {
			for _, record := range run.records {
				records = append(records, cloneCanonicalRecord(record))
			}
			continue
		}
		ids, width := run.allRecordIDs()
		for offset := range run.points() {
			records = append(records, RecordView{run: run, offset: offset}.canonical(ids[offset*width:(offset+1)*width]))
		}
	}
	return records
}

func (dataset *Dataset) Record(index int) (RecordView, bool) {
	if dataset == nil || index < 0 || index >= dataset.len {
		return RecordView{}, false
	}
	position := sort.SearchInts(dataset.starts, index+1) - 1
	run := &dataset.runs[position]
	offset := index - dataset.starts[position]
	if run.records != nil {
		return RecordView{record: &run.records[offset]}, true
	}
	return RecordView{run: run, offset: offset}, true
}

// RecordView exposes one immutable record. Map and byte getters return copies.
type RecordView struct {
	// record is a record kept whole; run and offset a point of a columnar run.
	record *contract.CanonicalRecordV2
	run    *datasetRun
	offset int
}

// canonical is a columnar point with its id as an independent record.
func (view RecordView) canonical(recordID string) contract.CanonicalRecordV2 {
	return contract.CanonicalRecordV2{RecordID: recordID, SourceTime: view.SourceTime(), BusinessID: view.BusinessID(),
		DimensionIdentity: view.DimensionIdentity(), Values: view.Values(), Dimensions: view.Dimensions(),
		ReceivedTime: view.ReceivedTime()}
}

// RecordID is the record's id: as kept, or, for a run whose ids are derived,
// from the buffer of the window reading it (MaterializeRecordIDs). Outside a
// window it is derived for the call and counted (recordIDsDerivedOutside): a
// reader there pays a hash per call, which a sort's comparator would pay
// O(n log n) times.
func (view RecordView) RecordID() string {
	switch {
	case view.record != nil:
		return view.record.RecordID
	case view.run != nil:
		return view.run.recordID(view.offset)
	}
	return ""
}

func (view RecordView) SourceTime() int64 {
	switch {
	case view.record != nil:
		return view.record.SourceTime
	case view.run != nil:
		return view.run.time(view.offset)
	}
	return 0
}

func (view RecordView) BusinessID() string {
	switch {
	case view.record != nil:
		return view.record.BusinessID
	case view.run != nil:
		return view.run.business
	}
	return ""
}

func (view RecordView) DimensionIdentity() contract.DimensionIdentityV2 {
	var identity contract.DimensionIdentityV2
	switch {
	case view.record != nil:
		identity = view.record.DimensionIdentity
	case view.run != nil:
		identity = view.run.identity
	default:
		return contract.DimensionIdentityV2{}
	}
	identity.Fields = append([]contract.DimensionFieldV2(nil), identity.Fields...)
	for index := range identity.Fields {
		identity.Fields[index].Value = cloneRawMessage(identity.Fields[index].Value)
	}
	return identity
}

// DimensionIdentityDigest returns the series digest alone. Callers that only
// compare series copy nothing: DimensionIdentity has to clone the field slice
// and every raw value in it to stay immutable, and a Slot compares series once
// per record per consuming Plan.
func (view RecordView) DimensionIdentityDigest() string {
	switch {
	case view.record != nil:
		return view.record.DimensionIdentity.Digest
	case view.run != nil:
		return view.run.identity.Digest
	}
	return ""
}

func (view RecordView) Values() map[string]json.RawMessage {
	switch {
	case view.record != nil:
		return cloneRawMap(view.record.Values)
	case view.run != nil:
		return map[string]json.RawMessage{view.run.valueField: view.run.appendValue(nil, view.offset)}
	}
	return nil
}

func (view RecordView) Value(name string) (json.RawMessage, bool) {
	switch {
	case view.record != nil:
		value, ok := view.record.Values[name]
		return cloneRawMessage(value), ok
	case view.run != nil && name == view.run.valueField:
		return view.run.appendValue(nil, view.offset), true
	}
	return nil, false
}

// AppendValue appends one value's bytes to dst. It is a copy, like Value,
// into a buffer the caller reuses: a reader keeping many values pays per
// buffer, not per value.
func (view RecordView) AppendValue(dst []byte, name string) ([]byte, bool) {
	switch {
	case view.record != nil:
		value, ok := view.record.Values[name]
		return append(dst, value...), ok
	case view.run != nil && name == view.run.valueField:
		return view.run.appendValue(dst, view.offset), true
	}
	return dst, false
}

func (view RecordView) Dimensions() map[string]json.RawMessage {
	switch {
	case view.record != nil:
		return cloneRawMap(view.record.Dimensions)
	case view.run != nil:
		return cloneRawMap(view.run.dimensions)
	}
	return nil
}

func (view RecordView) CollectionTime() *int64 {
	if view.record == nil || view.record.CollectionTime == nil {
		return nil
	}
	value := *view.record.CollectionTime
	return &value
}

func (view RecordView) ReceivedTime() int64 {
	switch {
	case view.record != nil:
		return view.record.ReceivedTime
	case view.run != nil:
		return view.run.received
	}
	return 0
}

// appendValue appends the bytes the point's value was given as.
func (run *datasetRun) appendValue(dst []byte, offset int) []byte {
	if run.sideMarks != nil && run.sideMarks[offset/64]&(1<<(offset%64)) != 0 {
		position := sort.Search(len(run.side), func(index int) bool { return run.side[index].offset >= offset })
		raw := run.side[position].raw
		if raw == "" {
			// A value given as no bytes reads back as none, as a whole
			// record's does.
			return dst
		}
		return append(dst, raw...)
	}
	return appendNumber(dst, run.values[offset])
}

func cloneCanonicalRecord(source contract.CanonicalRecordV2) contract.CanonicalRecordV2 {
	cloned := source
	cloned.DimensionIdentity = RecordView{record: &source}.DimensionIdentity()
	cloned.Values = cloneRawMap(source.Values)
	cloned.Dimensions = cloneRawMap(source.Dimensions)
	if source.CollectionTime != nil {
		value := *source.CollectionTime
		cloned.CollectionTime = &value
	}
	return cloned
}

func cloneRawMap(source map[string]json.RawMessage) map[string]json.RawMessage {
	if source == nil {
		return nil
	}
	cloned := make(map[string]json.RawMessage, len(source))
	for key, value := range source {
		cloned[key] = cloneRawMessage(value)
	}
	return cloned
}

func cloneRawMessage(source json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), source...)
}
