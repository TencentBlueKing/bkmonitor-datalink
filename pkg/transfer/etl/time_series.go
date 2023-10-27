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
)

// TSSchemaRecord : record with time series dimensions
type TSSchemaRecord struct {
	*BaseRecord
	Group      Field
	Time       Field
	Metrics    *SimpleRecord
	Dimensions *SimpleRecord
}

// Finish :
func (r *TSSchemaRecord) Finish() error {
	err := r.Metrics.Finish()
	if err != nil {
		return err
	}

	return r.Dimensions.Finish()
}

// Transform : transform data
func (r *TSSchemaRecord) Transform(from Container, to Container) error {
	if r.Time != nil {
		err := r.Time.Transform(from, to)
		if err != nil {
			return err
		}
	}

	if r.Group != nil {
		err := r.Group.Transform(from, to)
		if err != nil {
			return err
		}
	}

	records := map[string]Record{
		define.RecordMetricsFieldName:    r.Metrics,
		define.RecordDimensionsFieldName: r.Dimensions,
	}

	for name := range records {
		record := records[name]
		if record == nil {
			return ErrRecordNotReady
		}

		container, err := to.Get(name)
		switch errors.Cause(err) {
		case nil:
			break
		case define.ErrItemNotFound:
			// init item
			err = to.Put(name, make(map[string]interface{}))
			if err != nil {
				return err
			}

			container, err = to.Get(name)
			if err != nil {
				return err
			}
		default:
			return err
		}

		err = record.Transform(from, container.(Container))
		if err != nil {
			return errors.WithMessagef(err, "record %v", record)
		}
	}
	return nil
}

// AddTime :
func (r *TSSchemaRecord) AddTime(field Field) *TSSchemaRecord {
	r.Time = field
	return r
}

// AddMetrics :
func (r *TSSchemaRecord) AddMetrics(fields ...Field) *TSSchemaRecord {
	r.Metrics.AddFields(fields...)
	return r
}

// AddDimensions :
func (r *TSSchemaRecord) AddDimensions(fields ...Field) *TSSchemaRecord {
	r.Dimensions.AddFields(fields...)
	return r
}

// AddGroup :
func (r *TSSchemaRecord) AddGroup(field Field) *TSSchemaRecord {
	r.Group = field
	return r
}

// NewTSSchemaRecord :
func NewTSSchemaRecord(name string) *TSSchemaRecord {
	return &TSSchemaRecord{
		BaseRecord: NewBaseRecord(name),
		Metrics:    NewEmptySimpleRecord(define.RecordMetricsFieldName),
		Dimensions: NewEmptySimpleRecord(define.RecordDimensionsFieldName),
	}
}
