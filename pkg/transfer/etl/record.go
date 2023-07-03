// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package etl

import (
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

var ErrRecordNotReady = errors.New("record not ready")

// BaseRecord :
type BaseRecord struct {
	name string
}

// String
func (r *BaseRecord) String() string {
	return r.name
}

// String
func (r *BaseRecord) Name() string {
	return r.name
}

// Finish : finish transform
func (r *BaseRecord) Finish() error {
	return nil
}

// NewBaseRecord :
func NewBaseRecord(name string) *BaseRecord {
	return &BaseRecord{
		name: name,
	}
}

// LazyFieldsMixin
type LazyFieldsMixin struct{}

// Transform
func (f *LazyFieldsMixin) LazyTransform(fields []Field, from Container, to Container) error {
	lazyFields := make([]Field, 0)
	for _, f := range fields {
		err := f.Transform(from, to)
		switch err {
		case ErrFieldNotReady:
			lazyFields = append(lazyFields, f) // 后续执行
		case nil:
			continue
		default:
			return err
		}
	}

	for _, f := range lazyFields {
		err := f.Transform(from, to)
		if err != nil {
			return errors.WithMessagef(err, "lazy field %v", f)
		}
	}
	return nil
}

// SimpleRecord : to manage fields
type SimpleRecord struct {
	*BaseRecord
	LazyFieldsMixin
	Fields  []Field
	Records []Record
}

// Finish :
func (r *SimpleRecord) Finish() error {
	for _, f := range r.Records {
		err := f.Finish()
		if err != nil {
			return err
		}
	}
	return nil
}

// DeclareField : set fields
func (r *SimpleRecord) AddFields(fields ...Field) *SimpleRecord {
	r.Fields = append(r.Fields, fields...)
	return r
}

// AddRecords : set future records
func (r *SimpleRecord) AddRecords(records ...Record) *SimpleRecord {
	r.Records = append(r.Records, records...)
	return r
}

// Transform : transform data
func (r *SimpleRecord) Transform(from Container, to Container) error {
	for _, fr := range r.Records {
		err := fr.Transform(from, to)
		if err != nil {
			return errors.WithMessagef(err, "record %v", fr)
		}
	}

	return r.LazyTransform(r.Fields, from, to)
}

// NewNamedSimpleRecord :
func NewNamedSimpleRecord(name string, fields []Field) *SimpleRecord {
	return &SimpleRecord{
		BaseRecord: NewBaseRecord(name),
		Fields:     fields,
		Records:    make([]Record, 0),
	}
}

// NewSimpleRecord :
func NewSimpleRecord(fields []Field) *SimpleRecord {
	return NewNamedSimpleRecord("", fields)
}

// NewEmptySimpleRecord :
func NewEmptySimpleRecord(name string) *SimpleRecord {
	return NewNamedSimpleRecord(name, make([]Field, 0))
}

// IterationRecord
type IterationRecord struct {
	Record
	field *SimpleField
}

// Transform
func (r *IterationRecord) Transform(from Container, to Container) error {
	value, err := r.field.GetValue(from)
	if err != nil {
		return err
	}

	items, ok := value.([]interface{})
	if !ok {
		return errors.WithMessagef(define.ErrType, "expect []interface{} but %#v", value)
	}

	if len(items) == 0 {
		return nil
	}

	name := r.field.name
	index := name + "_index"
	for i, item := range items {
		err = from.Put(name, item)
		if err != nil {
			return errors.WithMessagef(err, "put item %v into name %s", item, name)
		}
		err = from.Put(index, i)
		if err != nil {
			return errors.WithMessagef(err, "put index %v into name %s", i, index)
		}

		err = r.Record.Transform(from, to)
		if err != nil {
			return err
		}
	}

	err = from.Del(name)
	if err != nil {
		return err
	}

	return from.Del(index)
}

// NewIterationRecord
func NewIterationRecord(name string, extract ExtractFn, base Record) *IterationRecord {
	return NewIterationRecordWithTransformer(name, extract, TransformAsIs, base)
}

// NewIterationRecordWithTransformer
func NewIterationRecordWithTransformer(name string, extract ExtractFn, transform TransformFn, base Record) *IterationRecord {
	return &IterationRecord{
		field:  NewSimpleField(name, extract, transform),
		Record: base,
	}
}

// OptionalRecord
type OptionalRecord struct {
	*BaseRecord
	fields []Field
}

// Transform
func (r *OptionalRecord) Transform(from Container, to Container) error {
	for _, field := range r.fields {
		err := field.Transform(from, to)
		if err != nil {
			logging.Debugf("transform %s error %v, continue because of optional field", field, err)
		}
	}
	return nil
}

// NewOptionalRecord
func NewOptionalRecord(name string, fields []Field) *OptionalRecord {
	return &OptionalRecord{
		BaseRecord: NewBaseRecord(name),
		fields:     fields,
	}
}

// ComplexRecord
type ComplexRecord struct {
	*BaseRecord
	records []Record
}

// Transform
func (r *ComplexRecord) Transform(from Container, to Container) error {
	for _, record := range r.records {
		err := record.Transform(from, to)
		if err != nil {
			return err
		}
	}
	return nil
}

// Finish
func (r *ComplexRecord) Finish() error {
	errs := utils.NewMultiErrors()
	for _, record := range r.records {
		errs.Add(record.Finish())
	}
	return errs.AsError()
}

// NewComplexRecord
func NewComplexRecord(name string, records []Record) *ComplexRecord {
	return &ComplexRecord{
		BaseRecord: NewBaseRecord(name),
		records:    records,
	}
}

// PrepareRecord
type PrepareRecord struct {
	*ComplexRecord
}

// Transform : from -> from
func (r *PrepareRecord) Transform(from Container, to Container) error {
	return r.ComplexRecord.Transform(from, from)
}

// NewPrepareRecord
func NewPrepareRecord(records []Record) *PrepareRecord {
	return &PrepareRecord{
		ComplexRecord: NewComplexRecord("", records),
	}
}

// PrepareRecord
type ReprocessRecord struct {
	*ComplexRecord
}

// Transform : to -> to
func (r *ReprocessRecord) Transform(from Container, to Container) error {
	return r.ComplexRecord.Transform(to, to)
}

// NewPrepareRecord
func NewReprocessRecord(records []Record) *ReprocessRecord {
	return &ReprocessRecord{
		ComplexRecord: NewComplexRecord("", records),
	}
}

// FunctionalRecord
type FunctionalRecord struct {
	*BaseRecord
	fn func(from Container, to Container) error
}

// Transform
func (r *FunctionalRecord) Transform(from Container, to Container) error {
	return r.fn(from, to)
}

// NewFunctionalRecord
func NewFunctionalRecord(name string, fn func(from Container, to Container) error) *FunctionalRecord {
	return &FunctionalRecord{
		BaseRecord: NewBaseRecord(name),
		fn:         fn,
	}
}

// NewCopyRecord
func NewCopyRecord() *FunctionalRecord {
	return NewFunctionalRecord("", func(from Container, to Container) error {
		for _, key := range from.Keys() {
			v, err := from.Get(key)
			if err != nil {
				return err
			}
			err = to.Put(key, v)
			if err != nil {
				return err
			}
		}
		return nil
	})
}
