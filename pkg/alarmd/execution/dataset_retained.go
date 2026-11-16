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
	"unsafe"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// The sizes RetainedBytes counts by. A map of string to json.RawMessage is
// a header and groups of eight slots, each slot a 16-byte key and a 24-byte
// value plus a control byte, filled to seven eighths before it grows.
const (
	rawMapHeaderBytes = 48
	rawMapGroupBytes  = 8 + 8*(16+24)
	rawMapGroupFill   = 7
)

var (
	datasetBytes         = uint64(unsafe.Sizeof(Dataset{}))
	datasetRunBytes      = uint64(unsafe.Sizeof(datasetRun{}))
	datasetSideBytes     = uint64(unsafe.Sizeof(datasetSideValue{}))
	canonicalRecordBytes = uint64(unsafe.Sizeof(contract.CanonicalRecordV2{}))
	dimensionFieldBytes  = uint64(unsafe.Sizeof(contract.DimensionFieldV2{}))
)

// RetainedBytes is what the Dataset holds on the heap, as its structure
// says: the runs and their starts; per columnar run its header, its series
// parts once - identity, dimensions, an identity field's value counted once
// where it is a dimension's - and its columns, eight bytes a point
// for the time and eight for the value, the record ids end to end where the
// run keeps them, and the side table and its marks; per record kept whole
// the record and its maps. A run whose ids are derived keeps none and counts
// none: its window buffer (MaterializeRecordIDs) is the window's, charged
// to the window that loads it, so the input a Slot holds to its end is
// counted without it. It is computed, not read from the runtime, so the same
// Dataset always counts the same; the calibration test holds it to the heap
// it takes.
func (dataset *Dataset) RetainedBytes() uint64 {
	if dataset == nil {
		return 0
	}
	total := datasetBytes + uint64(cap(dataset.runs))*datasetRunBytes + uint64(cap(dataset.starts))*8
	for index := range dataset.runs {
		run := &dataset.runs[index]
		if run.records != nil {
			total += uint64(cap(run.records)) * canonicalRecordBytes
			for record := range run.records {
				total += wholeRecordBytes(&run.records[record])
			}
			continue
		}
		total += identityBytes(run.identity) - sharedIdentityBytes(run) + rawMapBytes(run.dimensions) + uint64(len(run.ids)) +
			8*uint64(cap(run.times)) + 8*uint64(cap(run.values)) + 8*uint64(cap(run.sideMarks)) +
			uint64(cap(run.side))*datasetSideBytes
		for _, side := range run.side {
			total += uint64(len(side.raw))
		}
	}
	return total
}

// wholeRecordBytes is what a record kept whole holds beyond its struct.
func wholeRecordBytes(record *contract.CanonicalRecordV2) uint64 {
	total := uint64(len(record.RecordID)) + identityBytes(record.DimensionIdentity) +
		rawMapBytes(record.Values) + rawMapBytes(record.Dimensions)
	if record.CollectionTime != nil {
		total += 8
	}
	return total
}

func identityBytes(identity contract.DimensionIdentityV2) uint64 {
	total := uint64(len(identity.Digest)) + uint64(cap(identity.Fields))*dimensionFieldBytes
	for _, field := range identity.Fields {
		total += uint64(len(field.Name) + len(field.Value))
	}
	return total
}

// sharedIdentityBytes is what of a run's identity is its dimensions' bytes,
// held and counted once.
func sharedIdentityBytes(run *datasetRun) uint64 {
	var shared uint64
	for _, field := range run.identity.Fields {
		if value, ok := run.dimensions[field.Name]; ok && len(value) > 0 && unsafe.SliceData(value) == unsafe.SliceData(field.Value) {
			shared += uint64(len(value))
		}
	}
	return shared
}

func rawMapBytes(values map[string]json.RawMessage) uint64 {
	if values == nil {
		return 0
	}
	groups := (uint64(len(values)) + rawMapGroupFill - 1) / rawMapGroupFill
	total := rawMapHeaderBytes + max(groups, 1)*rawMapGroupBytes
	for key, value := range values {
		total += uint64(len(key) + len(value))
	}
	return total
}
