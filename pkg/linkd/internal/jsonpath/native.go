// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package jsonpath

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// Native 创建独立的 JSON 内存值；动态 JSON 字节只在这个输入边界解码。
func Native(value any) (any, error) { return native(value, 0) }

func native(value any, depth int) (any, error) {
	if depth > 64 {
		return nil, fmt.Errorf("JSON tree exceeds depth budget")
	}
	if scalar, ok := value.(interface{ JSONValue() (any, error) }); ok {
		v, err := scalar.JSONValue()
		if err != nil {
			return nil, err
		}
		return native(v, depth)
	}
	switch v := value.(type) {
	case nil, bool:
		return v, nil
	case string:
		return strings.Map(func(r rune) rune { return r }, v), nil
	case json.Number:
		if _, err := Measure(v); err != nil {
			return nil, err
		}
		return v, nil
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("invalid JSON number")
		}
		format := byte('f')
		if a := math.Abs(v); a != 0 && (a < 1e-6 || a >= 1e21) {
			format = 'e'
		}
		s := strconv.FormatFloat(v, format, -1, 64)
		if format == 'e' && len(s) >= 4 && s[len(s)-4:len(s)-1] == "e-0" {
			s = s[:len(s)-2] + s[len(s)-1:]
		}
		return json.Number(s), nil
	case []byte:
		if v == nil {
			return nil, nil
		}
		return base64.StdEncoding.EncodeToString(v), nil
	case json.RawMessage:
		if v == nil {
			return nil, nil
		}
		decoder := json.NewDecoder(bytes.NewReader(v))
		decoder.UseNumber()
		var result any
		if err := decoder.Decode(&result); err != nil {
			return nil, err
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return nil, fmt.Errorf("JSON value has trailing data")
		}
		if _, err := Measure(result); err != nil {
			return nil, err
		}
		return result, nil
	}
	rv := reflect.ValueOf(value)
	if _, ok := value.(json.Marshaler); ok {
		data, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		return native(json.RawMessage(data), depth)
	}
	switch rv.Kind() {
	case reflect.Map:
		if rv.Len() > 16384 {
			return nil, fmt.Errorf("JSON tree exceeds node budget")
		}
		if rv.Type().Key().Kind() != reflect.String {
			return nil, fmt.Errorf("JSON object key must be string")
		}
		if rv.IsNil() {
			return nil, nil
		}
		result := make(map[string]any, rv.Len())
		// 对非法 UTF-8 键按编码器的原键顺序处理，保持替换后同名键的覆盖结果确定。
		keys := rv.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
		for _, key := range keys {
			item, err := native(rv.MapIndex(key).Interface(), depth+1)
			if err != nil {
				return nil, err
			}
			result[strings.Map(func(r rune) rune { return r }, key.String())] = item
		}
		return result, nil
	case reflect.Slice, reflect.Array:
		if rv.Len() > 16384 {
			return nil, fmt.Errorf("JSON tree exceeds node budget")
		}
		if rv.Kind() == reflect.Slice && rv.IsNil() {
			return nil, nil
		}
		result := make([]any, rv.Len())
		for i := range result {
			item, err := native(rv.Index(i).Interface(), depth+1)
			if err != nil {
				return nil, err
			}
			result[i] = item
		}
		return result, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return json.Number(strconv.FormatInt(rv.Int(), 10)), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return json.Number(strconv.FormatUint(rv.Uint(), 10)), nil
	}
	// 自定义编码类型（Scalar、时间等）只在输入边界展开，后续查询不再编码。
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return native(json.RawMessage(data), depth)
}
