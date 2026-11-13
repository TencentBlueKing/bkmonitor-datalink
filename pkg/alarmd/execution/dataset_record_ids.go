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
	"sync/atomic"
	"unsafe"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// recordIDLength is the length of every id DeriveRecordIDV2 derives: a
// SHA-256 in lowercase hex.
const recordIDLength = 64

// recordIDWindowRunBytes is what a run's window buffer holds beside its ids:
// the string header its pointer points at.
const recordIDWindowRunBytes = uint64(unsafe.Sizeof(""))

// The counts of record ids by how a run came by them.
//
// keptRecordIDRuns is the columnar runs that keep their ids because one of
// them is not the derived one. Every producer of a Dataset today builds its
// records from ids that were derived, so a rising count is a producer that
// does not, holding 64 bytes a point it was not measured at.
//
// recordIDsDerivedOutside is the points whose id was derived outside a
// window: RecordID or Records on a run with no window buffer. Every reader
// of a Slot's inputs reads inside the window of its series, so a rising
// count is a reader outside one, paying a hash per read.
//
// recordIDsDerivedInWindow is the points MaterializeRecordIDs derived, once
// a point per window load.
var (
	keptRecordIDRuns         atomic.Uint64
	recordIDsDerivedOutside  atomic.Uint64
	recordIDsDerivedInWindow atomic.Uint64
)

// DatasetRecordIDCounts reports the columnar runs that keep their record ids
// and the points whose id was derived outside a window.
func DatasetRecordIDCounts() (keptRuns, derivedOutsideWindow uint64) {
	return keptRecordIDRuns.Load(), recordIDsDerivedOutside.Load()
}

// recordIDsDerived is whether every record's id is the one derived from the
// series digest and its source time. It derives into a buffer of its own and
// compares, so checking a run costs a hash a point and nothing per point
// beside it.
func recordIDsDerived(records []contract.CanonicalRecordV2, digest string) bool {
	if len(records) == 0 || len(records[0].RecordID) != recordIDLength {
		return false
	}
	deriver, err := contract.NewRecordIDDeriverV2(digest)
	if err != nil {
		return false
	}
	var buffer [recordIDLength]byte
	for index := range records {
		id, err := deriver.AppendDerive(buffer[:0], records[index].SourceTime)
		if err != nil || string(id) != records[index].RecordID {
			return false
		}
	}
	return true
}

// recordID is the id of the run's point at offset.
func (run *datasetRun) recordID(offset int) string {
	if !run.derived {
		width := int(run.idWidth)
		return run.ids[offset*width : (offset+1)*width]
	}
	if window := run.window.Load(); window != nil {
		return (*window)[offset*recordIDLength : (offset+1)*recordIDLength]
	}
	recordIDsDerivedOutside.Add(1)
	id, err := contract.DeriveRecordIDV2(run.identity.Digest, run.time(offset))
	if err != nil {
		// A derived run's digest and times are the ones its ids were
		// derived from, so there is no error to meet here.
		return ""
	}
	return id
}

// allRecordIDs is every id of the run end to end, and their width: as kept,
// the window's buffer, or, outside a window, derived now with one deriver
// for the run and counted as RecordID counts each.
func (run *datasetRun) allRecordIDs() (string, int) {
	if !run.derived {
		return run.ids, int(run.idWidth)
	}
	if window := run.window.Load(); window != nil {
		return *window, recordIDLength
	}
	recordIDsDerivedOutside.Add(uint64(run.points()))
	ids, err := run.deriveRecordIDs()
	if err != nil {
		// Unreachable for a derived run, as for recordID: every point then
		// reads as no id.
		return "", 0
	}
	return ids, recordIDLength
}

// deriveRecordIDs derives every id of a derived run end to end, the series'
// part of the hash once.
func (run *datasetRun) deriveRecordIDs() (string, error) {
	deriver, err := contract.NewRecordIDDeriverV2(run.identity.Digest)
	if err != nil {
		return "", err
	}
	buffer := make([]byte, 0, run.points()*recordIDLength)
	for offset := range run.points() {
		if buffer, err = deriver.AppendDerive(buffer, run.time(offset)); err != nil {
			return "", err
		}
	}
	// The buffer is not written again: the string is its bytes, not a copy.
	return unsafe.String(unsafe.SliceData(buffer), len(buffer)), nil
}

// recordIDWindowBytes is what a window buffer of points ids holds.
func recordIDWindowBytes(points int) uint64 {
	return uint64(points)*recordIDLength + recordIDWindowRunBytes
}

// RecordIDWindowBytes is what MaterializeRecordIDs would add now: the window
// buffers of the runs whose ids are derived and not yet in one. A reader
// reserves it before it materializes.
func (dataset *Dataset) RecordIDWindowBytes() uint64 {
	if dataset == nil {
		return 0
	}
	var total uint64
	for index := range dataset.runs {
		run := &dataset.runs[index]
		if run.derived && run.window.Load() == nil {
			total += recordIDWindowBytes(run.points())
		}
	}
	return total
}

// MaterializeRecordIDs derives the ids of every point of the runs that keep
// none into a buffer per run, which RecordID reads from then on, and returns
// the bytes the buffers it added hold (RecordIDWindowBytes). Each id is
// derived once, and the series' part of the hash - its digest checked
// against the pattern, the version and the digest hashed - once per run, not
// per point. A run already in a window is left as it is, so a Dataset two
// series of one window read is derived once.
//
// The buffers are the window's: the window that loads the series calls it,
// and calls DropRecordIDs when it is let go. Until then the Dataset holds
// both its columns and the ids, which the window is charged for.
func (dataset *Dataset) MaterializeRecordIDs() (uint64, error) {
	if dataset == nil {
		return 0, nil
	}
	var added uint64
	for index := range dataset.runs {
		run := &dataset.runs[index]
		if !run.derived || run.window.Load() != nil {
			continue
		}
		ids, err := run.deriveRecordIDs()
		if err != nil {
			return added, err
		}
		recordIDsDerivedInWindow.Add(uint64(run.points()))
		if run.window.CompareAndSwap(nil, &ids) {
			added += recordIDWindowBytes(run.points())
		}
	}
	return added, nil
}

// DropRecordIDs lets go of the window buffers MaterializeRecordIDs made and
// returns the bytes they held. A RecordID read after it derives again, and
// is counted.
func (dataset *Dataset) DropRecordIDs() uint64 {
	if dataset == nil {
		return 0
	}
	var dropped uint64
	for index := range dataset.runs {
		run := &dataset.runs[index]
		if run.window.Swap(nil) != nil {
			dropped += recordIDWindowBytes(run.points())
		}
	}
	return dropped
}
