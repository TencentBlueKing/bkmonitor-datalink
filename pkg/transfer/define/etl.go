// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define

import (
	"fmt"
	"maps"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/bufferpool"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/types"
)

// MetaFieldType :
type MetaFieldType string

const (
	// MetaFieldTypeNested :
	MetaFieldTypeNested MetaFieldType = "nested"
	// MetaFieldTypeObject :
	MetaFieldTypeObject MetaFieldType = "object"
	// MetaFieldTypeInt :
	MetaFieldTypeInt MetaFieldType = "int"
	// MetaFieldTypeUint :
	MetaFieldTypeUint MetaFieldType = "uint"
	// MetaFieldTypeFloat :
	MetaFieldTypeFloat MetaFieldType = "float"
	// MetaFieldTypeString :
	MetaFieldTypeString MetaFieldType = "string"
	// MetaFieldTypeBool :
	MetaFieldTypeBool MetaFieldType = "bool"
	// MetaFieldTypeTimestamp :
	MetaFieldTypeTimestamp MetaFieldType = "timestamp"
)

// MetaFieldTagType :
type MetaFieldTagType string

const (
	// MetaFieldTagMetric :
	MetaFieldTagMetric MetaFieldTagType = "metric"
	// MetaFieldTagDimension :
	MetaFieldTagDimension MetaFieldTagType = "dimension"
	// MetaFieldTagTime :
	MetaFieldTagTime MetaFieldTagType = "timestamp"
	// MetaFieldTagGroup :
	MetaFieldTagGroup MetaFieldTagType = "group"
)

// ETLRecord :
type ETLRecord struct {
	Time       *int64                 `json:"time"`
	Dimensions map[string]interface{} `json:"dimensions"`
	// metric
	// 对于事件数据而言，则是事件的内容
	// 对于时序数据而言，则是指标的内容
	// 对于日志数据而言，指标和维度到最后都是一视同仁的写入到ES，由ES的meta控制
	Metrics  map[string]interface{} `json:"metrics"`
	Exemplar map[string]interface{} `json:"exemplar"`
}

type ETLRecordFields struct {
	KeepMetrics    []string `json:"keep_metrics" mapstructure:"keep_metrics"`
	DropMetrics    []string `json:"drop_metrics" mapstructure:"drop_metrics"`
	KeepDimensions []string `json:"keep_dimensions" mapstructure:"keep_dimensions"`
	DropDimensions []string `json:"drop_dimensions" mapstructure:"drop_dimensions"`
	GroupKeys      []string `json:"group_keys" mapstructure:"group_keys"`
}

// Filter 过滤 ELTRecord
//
// 白名单规则优先与黑名单规则 当且仅当没有白名单时黑名单才会生效
func (f *ETLRecordFields) Filter(record ETLRecord) ETLRecord {
	newRecord := ETLRecord{
		Time:       record.Time,
		Exemplar:   record.Exemplar,
		Dimensions: record.Dimensions,
		Metrics:    record.Metrics,
	}

	if len(f.KeepMetrics) > 0 {
		// 指标白名单
		newMetrics := make(map[string]interface{})
		for _, k := range f.KeepMetrics {
			if v, ok := record.Metrics[k]; ok {
				newMetrics[k] = v
			}
		}
		newRecord.Metrics = newMetrics
	} else {
		// 指标黑名单
		if len(f.DropMetrics) > 0 {
			cloned := maps.Clone(record.Metrics)
			for _, k := range f.DropMetrics {
				if _, ok := cloned[k]; ok {
					delete(cloned, k)
				}
			}
			newRecord.Metrics = cloned
		}
	}

	if len(f.KeepDimensions) > 0 {
		// 维度白名单
		newDimensions := make(map[string]interface{})
		for _, k := range f.KeepDimensions {
			v, ok := record.Dimensions[k]
			if ok {
				newDimensions[k] = v
			}
		}
		newRecord.Dimensions = newDimensions
	} else {
		// 维度黑名单
		if len(f.DropDimensions) > 0 {
			cloned := maps.Clone(record.Dimensions)
			for _, k := range f.DropDimensions {
				if _, ok := cloned[k]; ok {
					delete(cloned, k)
				}
			}
			newRecord.Dimensions = cloned
		}
	}
	return newRecord
}

func (f *ETLRecordFields) GroupID(document map[string]interface{}) uint64 {
	buf := bufferpool.Get()
	defer bufferpool.Put(buf)

	for _, key := range f.GroupKeys {
		v, ok := document[key]
		if !ok {
			continue
		}
		buf.WriteString(key + "/")
		fmt.Fprintf(buf, "%s/", v)
	}
	return xxhash.Sum64(buf.Bytes())
}

type GroupETLRecord struct {
	*ETLRecord
	GroupInfo []map[string]interface{} `json:"group_info"`
	CMDBInfo  []map[string]interface{} `json:"bk_cmdb_level"`
}

// SetTime :
func (r *ETLRecord) SetTime(t time.Time) {
	ts := t.Unix()
	r.Time = &ts
}

// SetTimeStamp :
func (r *ETLRecord) SetTimeStamp(t types.TimeStamp) {
	r.SetTime(t.Time)
}

// GetTime :
func (r *ETLRecord) GetTime() (time.Time, error) {
	if r.Time == nil {
		return time.Time{}, errors.Wrapf(ErrOperationForbidden, "time is empty")
	}

	return time.Unix(*r.Time, 0), nil
}

// ETLRecordHandler :
type ETLRecordHandler func(*ETLRecord) error

// ETLRecordChainingHandler :
type ETLRecordChainingHandler func(*ETLRecord, ETLRecordHandler) error

// ETLRecordHandlerWrapper :
func ETLRecordHandlerWrapper(chaining ETLRecordChainingHandler, handler ETLRecordHandler) ETLRecordHandler {
	return func(record *ETLRecord) error {
		return chaining(record, handler)
	}
}

// NewETLRecord :
func NewETLRecord() *ETLRecord {
	return &ETLRecord{
		Dimensions: make(map[string]interface{}),
		Metrics:    make(map[string]interface{}),
	}
}
