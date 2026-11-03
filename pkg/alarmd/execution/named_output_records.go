// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

import "encoding/json"

// NamedOutputState is what one named output said at one record.
type NamedOutputState string

const (
	// NamedOutputSuccess and NamedOutputPartial are an output that answered
	// at the record, whole or in part; its value is the one it answered.
	NamedOutputSuccess NamedOutputState = "SUCCESS"
	NamedOutputPartial NamedOutputState = "PARTIAL"
	// NamedOutputMissing is an output that answered, and not at the record's
	// time under its labels.
	NamedOutputMissing NamedOutputState = "MISSING"
	// NamedOutputError is an output the provider failed to compute.
	NamedOutputError NamedOutputState = "ERROR"
	// NamedOutputUnsupported is an output the provider did not answer as
	// one: a provider older than the named-outputs contract.
	NamedOutputUnsupported NamedOutputState = "UNSUPPORTED"
)

// NamedOutputValue is one output at one record: its state, and the value it
// answered there when it answered one.
type NamedOutputValue struct {
	Value json.RawMessage `json:",omitempty"`
	State NamedOutputState
}

// NamedOutputRecords is a query's named outputs aligned to the records of
// its legacy output, the records detection reads.
//
// Refs are the outputs in the order the query asked for them, the legacy
// one among them; Records holds, by RecordID, one value per ref in that
// order. The legacy output's value is the record's own, the one detection
// reads; every other output's is the point it answered at the record's time
// under the record's labels, __name__ aside.
//
// It is kept beside the records, not in them: a record's Values reach its
// delivery proof, and its fields the record digest, and neither may move
// for what is read beside the detection.
type NamedOutputRecords struct {
	Refs []string
	// LegacyRef is which of Refs is the legacy output, the one detection
	// reads; one of Refs' own strings, and charged with them.
	LegacyRef string
	Records   map[string][]NamedOutputValue
}

// What a table holds, measured (TestANamedOutputTableCostsWhatItIsCharged):
// a record's map entry, its slice and its RecordID, and each output's
// value beside its bytes; and each ref.
const (
	namedOutputRecordBytes = 112
	namedOutputValueBytes  = 56
	namedOutputRefBytes    = 16
)

// RetainedBytes is what the table holds for as long as it is kept: the
// figure a Slot is charged for it, the sum of NamedOutputRefsRetainedBytes
// and NamedOutputRecordRetainedBytes. Zero for no table.
func (records *NamedOutputRecords) RetainedBytes() uint64 {
	if records == nil {
		return 0
	}
	total := NamedOutputRefsRetainedBytes(records.Refs)
	for recordID, values := range records.Records {
		total += NamedOutputRecordRetainedBytes(recordID, values)
	}
	return total
}

// NamedOutputRefsRetainedBytes is what a table's refs hold.
func NamedOutputRefsRetainedBytes(refs []string) uint64 {
	var total uint64
	for _, ref := range refs {
		total += namedOutputRefBytes + uint64(len(ref))
	}
	return total
}

// NamedOutputRecordRetainedBytes is what one record of a table holds: its
// entry, its RecordID and its values.
func NamedOutputRecordRetainedBytes(recordID string, values []NamedOutputValue) uint64 {
	total := namedOutputRecordBytes + uint64(len(recordID))
	for _, value := range values {
		total += namedOutputValueBytes + uint64(len(value.Value))
	}
	return total
}
