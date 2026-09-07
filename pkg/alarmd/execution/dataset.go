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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// Dataset is immutable after construction. Multiple Plans may share one
// Dataset safely without exposing writable records, maps or RawMessage bytes.
type Dataset struct {
	records []contract.CanonicalRecordV2
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

func NewDataset(records []contract.CanonicalRecordV2) *Dataset {
	cloned := make([]contract.CanonicalRecordV2, len(records))
	for index := range records {
		cloned[index] = cloneCanonicalRecord(records[index])
	}
	return &Dataset{records: cloned}
}

func (dataset *Dataset) Len() int {
	if dataset == nil {
		return 0
	}
	return len(dataset.records)
}

// Records returns independent copies of every record in Dataset order. The
// Dataset stays immutable: callers that fold or re-shape records build a new
// Dataset from the copies.
func (dataset *Dataset) Records() []contract.CanonicalRecordV2 {
	if dataset == nil {
		return nil
	}
	records := make([]contract.CanonicalRecordV2, len(dataset.records))
	for index := range dataset.records {
		records[index] = cloneCanonicalRecord(dataset.records[index])
	}
	return records
}

func (dataset *Dataset) Record(index int) (RecordView, bool) {
	if dataset == nil || index < 0 || index >= len(dataset.records) {
		return RecordView{}, false
	}
	return RecordView{record: &dataset.records[index]}, true
}

// RecordView exposes one immutable record. Map and byte getters return copies.
type RecordView struct {
	record *contract.CanonicalRecordV2
}

func (view RecordView) RecordID() string {
	if view.record == nil {
		return ""
	}
	return view.record.RecordID
}

func (view RecordView) SourceTime() int64 {
	if view.record == nil {
		return 0
	}
	return view.record.SourceTime
}

func (view RecordView) BusinessID() string {
	if view.record == nil {
		return ""
	}
	return view.record.BusinessID
}

func (view RecordView) DimensionIdentity() contract.DimensionIdentityV2 {
	if view.record == nil {
		return contract.DimensionIdentityV2{}
	}
	identity := view.record.DimensionIdentity
	identity.Fields = append([]contract.DimensionFieldV2(nil), identity.Fields...)
	for index := range identity.Fields {
		identity.Fields[index].Value = cloneRawMessage(identity.Fields[index].Value)
	}
	return identity
}

func (view RecordView) Values() map[string]json.RawMessage {
	if view.record == nil {
		return nil
	}
	return cloneRawMap(view.record.Values)
}

func (view RecordView) Value(name string) (json.RawMessage, bool) {
	if view.record == nil {
		return nil, false
	}
	value, ok := view.record.Values[name]
	return cloneRawMessage(value), ok
}

func (view RecordView) Dimensions() map[string]json.RawMessage {
	if view.record == nil {
		return nil
	}
	return cloneRawMap(view.record.Dimensions)
}

func (view RecordView) CollectionTime() *int64 {
	if view.record == nil || view.record.CollectionTime == nil {
		return nil
	}
	value := *view.record.CollectionTime
	return &value
}

func (view RecordView) ReceivedTime() int64 {
	if view.record == nil {
		return 0
	}
	return view.record.ReceivedTime
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
