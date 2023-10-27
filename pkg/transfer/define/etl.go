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
	"time"

	"github.com/pkg/errors"

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
