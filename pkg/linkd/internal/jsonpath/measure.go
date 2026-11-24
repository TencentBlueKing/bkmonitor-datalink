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
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"unicode/utf8"
)

// Measurement 记录 JSON 值的编码体积和结构预算，不分配完整编码结果。
type Measurement struct {
	Bytes int
	Nodes int
	Depth int
}

// Check 检查与 ValidateTree 相同的深度、节点数和编码体积边界。
func (m Measurement) Check() error {
	if m.Depth > 64 || m.Nodes > 16384 {
		return fmt.Errorf("JSON tree exceeds depth or node budget")
	}
	if m.Bytes > 1<<20 {
		return fmt.Errorf("JSON exceeds 1 MiB")
	}
	return nil
}

// StringBytes 返回 encoding/json 默认 HTML 转义下字符串的编码长度。
func StringBytes(s string) int {
	n := 2
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			switch c {
			case '\\', '"', '\n', '\r', '\t', '\b', '\f':
				n += 2
			case '<', '>', '&':
				n += 6
			default:
				if c < 0x20 {
					n += 6
				} else {
					n++
				}
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == '\u2028' || r == '\u2029' || r == utf8.RuneError && size == 1 {
			n += 6
		} else {
			n += size
		}
		i += size
	}
	return n
}

// Measure 对已解码的 Go JSON 值计量；只在非 JSON 原生类型的边界调用其编码器。
func Measure(value any) (Measurement, error) {
	return measure(value, 0)
}

func measure(value any, depth int) (Measurement, error) {
	if depth > 64 {
		return Measurement{}, fmt.Errorf("JSON tree exceeds depth or node budget")
	}
	m := Measurement{Nodes: 1}
	add := func(v any) error {
		child, err := measure(v, depth+1)
		if err != nil {
			return err
		}
		m.Bytes += child.Bytes
		m.Nodes += child.Nodes
		m.Depth = max(m.Depth, child.Depth+1)
		return m.Check()
	}
	switch v := value.(type) {
	case nil:
		m.Bytes = 4
	case bool:
		if v {
			m.Bytes = 4
		} else {
			m.Bytes = 5
		}
	case string:
		m.Bytes = StringBytes(v)
	case json.Number:
		if len(v) == 0 || (v[0] != '-' && (v[0] < '0' || v[0] > '9')) || v[len(v)-1] < '0' || v[len(v)-1] > '9' || !json.Valid([]byte(v)) {
			return Measurement{}, fmt.Errorf("invalid JSON number")
		}
		m.Bytes = len(v)
	case map[string]any:
		if v == nil {
			m.Bytes = 4
			break
		}
		m.Bytes = 2
		for k, item := range v {
			m.Bytes += StringBytes(k) + 1
			if err := add(item); err != nil {
				return Measurement{}, err
			}
		}
		if len(v) > 0 {
			m.Bytes += len(v) - 1
		}
	case []any:
		if v == nil {
			m.Bytes = 4
			break
		}
		m.Bytes = 2
		for _, item := range v {
			if err := add(item); err != nil {
				return Measurement{}, err
			}
		}
		if len(v) > 0 {
			m.Bytes += len(v) - 1
		}
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return Measurement{}, fmt.Errorf("invalid JSON number")
		}
		format := byte('f')
		if a := math.Abs(v); a != 0 && (a < 1e-6 || a >= 1e21) {
			format = 'e'
		}
		s := strconv.FormatFloat(v, format, -1, 64)
		m.Bytes = len(s)
		if format == 'e' && len(s) >= 4 && s[len(s)-4:len(s)-1] == "e-0" {
			m.Bytes--
		}
	case int:
		m.Bytes = len(strconv.Itoa(v))
	case int64:
		m.Bytes = len(strconv.FormatInt(v, 10))
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return Measurement{}, err
		}
		// 泛型调用方的类型仍按真实 JSON 结构检查，不能只检查其字节长度。
		var decoded any
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		if err := decoder.Decode(&decoded); err != nil {
			return Measurement{}, err
		}
		measured, err := measure(decoded, depth)
		if err != nil {
			return Measurement{}, err
		}
		measured.Bytes = len(data)
		return measured, measured.Check()
	}
	return m, m.Check()
}
