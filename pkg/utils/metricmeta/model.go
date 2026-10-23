// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package metricmeta 合并指标元信息，以有界内存去重并周期重发。
package metricmeta

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

// ScopeType 标识元信息的业务范围类型，允许自定义非空值。
type ScopeType string

// 预定义范围类型由协议适配器选择。
const (
	ScopeAPM     ScopeType = "APM"
	ScopePlugin  ScopeType = "PLUGIN"
	ScopeDefault ScopeType = "DEFAULT"
)

// Scope 由使用方提供稳定名称，用于隔离服务或采集分组。
type Scope struct {
	Type ScopeType
	Name string
}

// Key 唯一标识鉴权 DataID 和业务范围内的实际输出指标。
type Key struct {
	DataID    int32
	ScopeType ScopeType
	Scope     string
	Metric    string
}

// NewKey 使用最终 DataID、范围和输出指标名构造主键。
func NewKey(dataID int32, scope Scope, metric string) Key {
	return Key{DataID: dataID, ScopeType: scope.Type, Scope: scope.Name, Metric: metric}
}

// 可选字段的存在标记用于区分未知与明确的空值。
const (
	PresentUnit uint8 = 1 << iota
	PresentDescription
	PresentInterval
	PresentUsage
)

// Value 保存指标原名及语义，Present 指示可选字段是否已知。
type Value struct {
	Name, Unit, Description string
	Type, OriginalType      string
	Temporality, Usage      string
	Interval                int64 // 毫秒。
	Present                 uint8
}

// Entry 将唯一主键与该指标的完整元信息组合为发送条目。
type Entry struct {
	Key   Key
	Value Value
}

// Observer 接受完整最新值，返回 false 表示本次元信息被拒绝。
type Observer interface {
	Observe(Key, Value) bool
}

func (e Entry) valid() bool {
	k, v := e.Key, e.Value
	switch v.Type {
	case "counter", "gauge", "unknown":
	default:
		return false
	}
	switch v.OriginalType {
	case "sum", "gauge", "histogram", "summary", "counter", "avg", "unknown":
	default:
		return false
	}
	if v.Present&PresentUsage != 0 {
		switch v.Usage {
		case "sum", "min", "max", "avg", "last":
		default:
			return false
		}
	}
	for _, field := range []string{string(k.ScopeType), k.Scope, k.Metric, v.Name, v.Unit, v.Description, v.Type, v.OriginalType, v.Temporality, v.Usage} {
		if !utf8.ValidString(field) {
			return false
		}
	}
	return k.DataID > 0 && k.ScopeType != "" && k.Metric != "" && v.Name != "" &&
		v.Type != "" && v.OriginalType != "" && (v.Temporality == "delta" || v.Temporality == "cumulative") &&
		(v.Present&PresentInterval == 0 || v.Interval > 0) && v.Present & ^uint8(15) == 0
}

func (e Entry) size() int {
	k, v := e.Key, e.Value
	return len(k.ScopeType) + len(k.Scope) + len(k.Metric) + len(v.Name) + len(v.Unit) +
		len(v.Description) + len(v.Type) + len(v.OriginalType) + len(v.Temporality) + len(v.Usage)
}

// clone 防止缓存引用解码器大缓冲区中的子串。
func (e Entry) clone() Entry {
	e.Key.ScopeType = ScopeType(strings.Clone(string(e.Key.ScopeType)))
	e.Key.Scope, e.Key.Metric = strings.Clone(e.Key.Scope), strings.Clone(e.Key.Metric)
	v := &e.Value
	v.Name, v.Unit, v.Description = strings.Clone(v.Name), strings.Clone(v.Unit), strings.Clone(v.Description)
	v.Type, v.OriginalType = strings.Clone(v.Type), strings.Clone(v.OriginalType)
	v.Temporality, v.Usage = strings.Clone(v.Temporality), strings.Clone(v.Usage)
	return e
}

func knownString(value string, present bool) *string {
	if !present {
		return nil
	}
	return &value
}

type wireEntry struct {
	DataID       int32     `json:"dataid"`
	ScopeType    ScopeType `json:"__scope_type"`
	Scope        string    `json:"__scope"`
	Metric       string    `json:"__metric"`
	Name         string    `json:"__name"`
	Description  *string   `json:"__description"`
	Unit         *string   `json:"__unit"`
	Temporality  string    `json:"__temporality"`
	Interval     *int64    `json:"__interval"`
	OriginalType string    `json:"__original_type"`
	Type         string    `json:"__type"`
	Usage        *string   `json:"__usage"`
}

// MarshalJSON 将未知可选字段编码为 null。
func (e Entry) MarshalJSON() ([]byte, error) {
	k, v := e.Key, e.Value
	var interval *int64
	if v.Present&PresentInterval != 0 {
		interval = &v.Interval
	}
	return json.Marshal(wireEntry{
		k.DataID, k.ScopeType, k.Scope, k.Metric, v.Name,
		knownString(v.Description, v.Present&PresentDescription != 0),
		knownString(v.Unit, v.Present&PresentUnit != 0), v.Temporality, interval,
		v.OriginalType, v.Type, knownString(v.Usage, v.Present&PresentUsage != 0),
	})
}

// UnmarshalJSON 恢复可空字段的存在标记，避免未知值被误当作空字符串。
func (e *Entry) UnmarshalJSON(data []byte) error {
	var w wireEntry
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	entry := Entry{
		Key:   Key{DataID: w.DataID, ScopeType: w.ScopeType, Scope: w.Scope, Metric: w.Metric},
		Value: Value{Name: w.Name, Type: w.Type, OriginalType: w.OriginalType, Temporality: w.Temporality},
	}
	for _, field := range []struct {
		src *string
		dst *string
		bit uint8
	}{
		{w.Description, &entry.Value.Description, PresentDescription},
		{w.Unit, &entry.Value.Unit, PresentUnit},
		{w.Usage, &entry.Value.Usage, PresentUsage},
	} {
		if field.src != nil {
			*field.dst = *field.src
			entry.Value.Present |= field.bit
		}
	}
	if w.Interval != nil {
		entry.Value.Interval = *w.Interval
		entry.Value.Present |= PresentInterval
	}
	if !entry.valid() {
		return errors.New("metricmeta: invalid entry")
	}
	*e = entry
	return nil
}
