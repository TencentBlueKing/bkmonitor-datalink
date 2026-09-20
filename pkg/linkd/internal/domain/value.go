// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
)

// ScalarKind 描述告警维度标量的具体 JSON 类型。
type ScalarKind string

const (
	ScalarKindString ScalarKind = "string"
	ScalarKindNumber ScalarKind = "number"
	ScalarKindBool   ScalarKind = "bool"
)

// Scalar 是只允许字符串、有限数字或布尔值的领域标量。
// 零值无效，调用方应通过构造函数或 JSON 解码创建。
type Scalar struct {
	kind        ScalarKind
	stringValue string
	numberValue float64
	boolValue   bool
}

// NewStringScalar 创建字符串标量。
func NewStringScalar(value string) Scalar {
	return Scalar{kind: ScalarKindString, stringValue: value}
}

// NewNumberScalar 创建有限数字标量。
func NewNumberScalar(value float64) (Scalar, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return Scalar{}, fmt.Errorf("scalar number must be finite: %v", value)
	}
	return Scalar{kind: ScalarKindNumber, numberValue: value}, nil
}

// NewBoolScalar 创建布尔标量。
func NewBoolScalar(value bool) Scalar {
	return Scalar{kind: ScalarKindBool, boolValue: value}
}

// Kind 返回标量的具体类型。
func (s Scalar) Kind() ScalarKind {
	return s.kind
}

// StringValue 返回字符串值；标量不是字符串时 ok 为 false。
func (s Scalar) StringValue() (value string, ok bool) {
	return s.stringValue, s.kind == ScalarKindString
}

// NumberValue 返回数字值；标量不是数字时 ok 为 false。
func (s Scalar) NumberValue() (value float64, ok bool) {
	return s.numberValue, s.kind == ScalarKindNumber
}

// BoolValue 返回布尔值；标量不是布尔值时 ok 为 false。
func (s Scalar) BoolValue() (value bool, ok bool) {
	return s.boolValue, s.kind == ScalarKindBool
}

// Valid 报告标量是否满足领域约束。
func (s Scalar) Valid() bool {
	switch s.kind {
	case ScalarKindString, ScalarKindBool:
		return true
	case ScalarKindNumber:
		return !math.IsNaN(s.numberValue) && !math.IsInf(s.numberValue, 0)
	default:
		return false
	}
}

// MarshalJSON 保留标量的原始 JSON 类型。
func (s Scalar) MarshalJSON() ([]byte, error) {
	if !s.Valid() {
		return nil, fmt.Errorf("marshal invalid scalar kind %q", s.kind)
	}
	switch s.kind {
	case ScalarKindString:
		return json.Marshal(s.stringValue)
	case ScalarKindNumber:
		return json.Marshal(s.numberValue)
	case ScalarKindBool:
		return json.Marshal(s.boolValue)
	default:
		return nil, fmt.Errorf("marshal unsupported scalar kind %q", s.kind)
	}
}

// UnmarshalJSON 只接受字符串、有限数字或布尔值。
func (s *Scalar) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("decode scalar: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return err
	}

	switch typed := value.(type) {
	case string:
		*s = NewStringScalar(typed)
		return nil
	case bool:
		*s = NewBoolScalar(typed)
		return nil
	case json.Number:
		number, err := typed.Float64()
		if err != nil {
			return fmt.Errorf("decode scalar number: %w", err)
		}
		created, err := NewNumberScalar(number)
		if err != nil {
			return err
		}
		*s = created
		return nil
	default:
		return fmt.Errorf("scalar must be string, finite number, or boolean")
	}
}

// DimensionMap 保存扁平、类型稳定的事件或告警维度。
type DimensionMap map[string]Scalar

// Validate 校验维度键和值。
func (m DimensionMap) Validate() error {
	for key, value := range m {
		if key == "" {
			return fmt.Errorf("dimension key must not be empty")
		}
		if !value.Valid() {
			return fmt.Errorf("dimension %q contains invalid scalar", key)
		}
	}
	return nil
}

// Clone 返回与原 map 不共享可变状态的副本。
func (m DimensionMap) Clone() DimensionMap {
	if m == nil {
		return nil
	}
	cloned := make(DimensionMap, len(m))
	for key, value := range m {
		cloned[key] = value
	}
	return cloned
}

// Normalize 把缺失和空字典统一为空字典，避免持久化省略字段后破坏幂等比较。
func (m DimensionMap) Normalize() DimensionMap {
	if len(m) == 0 {
		return DimensionMap{}
	}
	return m.Clone()
}

// JSONObject 保存需要原样持久化、但不在核心领域中展开的 JSON 对象。
// 每个值会被规范化，保证幂等比较不受无意义空白影响。
type JSONObject map[string]json.RawMessage

// Normalize 校验并返回深拷贝后的规范 JSON 对象。
func (o JSONObject) Normalize() (JSONObject, error) {
	if o == nil {
		return JSONObject{}, nil
	}
	normalized := make(JSONObject, len(o))
	for key, raw := range o {
		if key == "" {
			return nil, fmt.Errorf("JSON object key must not be empty")
		}
		value, err := decodeJSONValue(raw)
		if err != nil {
			return nil, fmt.Errorf("normalize JSON object field %q: %w", key, err)
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encode JSON object field %q: %w", key, err)
		}
		normalized[key] = encoded
	}
	return normalized, nil
}

// Clone 返回与原对象不共享 RawMessage 字节的副本。
func (o JSONObject) Clone() JSONObject {
	if o == nil {
		return nil
	}
	cloned := make(JSONObject, len(o))
	for key, raw := range o {
		cloned[key] = append(json.RawMessage(nil), raw...)
	}
	return cloned
}

func decodeJSONValue(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := decodeJSONToken(decoder)
	if err != nil {
		return nil, fmt.Errorf("decode JSON value: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, err
	}
	return value, nil
}

func decodeJSONToken(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return token, nil
	}
	switch delimiter {
	case '{':
		object := make(map[string]any)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, fmt.Errorf("JSON object key must be a string")
			}
			if _, exists := object[key]; exists {
				return nil, fmt.Errorf("JSON object contains duplicate key %q", key)
			}
			value, err := decodeJSONToken(decoder)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		closing, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		if closing != json.Delim('}') {
			return nil, fmt.Errorf("JSON object has invalid closing delimiter")
		}
		return object, nil
	case '[':
		array := make([]any, 0)
		for decoder.More() {
			value, err := decodeJSONToken(decoder)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		closing, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		if closing != json.Delim(']') {
			return nil, fmt.Errorf("JSON array has invalid closing delimiter")
		}
		return array, nil
	default:
		return nil, fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if err == nil {
		return fmt.Errorf("JSON value contains trailing data")
	}
	if err != io.EOF {
		return fmt.Errorf("decode trailing JSON data: %w", err)
	}
	return nil
}
