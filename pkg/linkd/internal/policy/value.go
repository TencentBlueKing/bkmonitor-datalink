// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package policy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"linkd/internal/domain"
)

// GroupKey 按有序字段值生成类型化分组身份。空字段列表代表唯一的策略内分组。
// 数字 0、false 与字符串 "0" 不相同；数组保留顺序，对象键排序。租户/策略作用域由调用方另行编码。
func GroupKey(values []any) (string, error) {
	if len(values) > 32 {
		return "", fmt.Errorf("group fields exceed 32")
	}
	normalized := make([]any, 0, len(values))
	nodes := 0
	for _, value := range values {
		item, err := canonicalGroupValue(value, 0, &nodes)
		if err != nil {
			return "", err
		}
		normalized = append(normalized, item)
	}
	data, err := json.Marshal(normalized)
	if err != nil || len(data) > 65536 {
		return "", fmt.Errorf("invalid_group_value: group exceeds 64 KiB")
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func canonicalGroupValue(value any, depth int, nodes *int) (any, error) {
	*nodes++
	if depth > 16 || *nodes > 4096 {
		return nil, fmt.Errorf("invalid_group_value: nesting or node budget exceeded")
	}
	if value == nil {
		return nil, fmt.Errorf("invalid_group_value: null or missing")
	}
	switch v := value.(type) {
	case json.RawMessage:
		if len(v) > 65536 {
			return nil, fmt.Errorf("invalid_group_value: JSON budget exceeded")
		}
		normalized, err := (domain.JSONObject{"value": v}).Normalize()
		if err != nil {
			return nil, fmt.Errorf("invalid_group_value: invalid or duplicate JSON")
		}
		var decoded any
		decoder := json.NewDecoder(bytes.NewReader(normalized["value"]))
		decoder.UseNumber()
		if !json.Valid(v) {
			return nil, fmt.Errorf("invalid_group_value: invalid JSON")
		}
		if err := decoder.Decode(&decoded); err != nil {
			return nil, err
		}
		return canonicalGroupValue(decoded, depth+1, nodes)
	case string:
		if strings.TrimSpace(v) == "" || len(v) > 65536 {
			return nil, fmt.Errorf("invalid_group_value: empty or oversized string")
		}
		return []any{"string", v}, nil
	case bool:
		return []any{"bool", v}, nil
	case []any:
		if len(v) == 0 || len(v) > 4096 {
			return nil, fmt.Errorf("invalid_group_value: array size")
		}
		out := make([]any, 0, len(v))
		for _, item := range v {
			c, err := canonicalGroupValue(item, depth+1, nodes)
			if err != nil {
				return nil, err
			}
			out = append(out, c)
		}
		return []any{"array", out}, nil
	case map[string]any:
		if len(v) == 0 || len(v) > 4096 {
			return nil, fmt.Errorf("invalid_group_value: object size")
		}
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out := make([]any, 0, len(keys))
		for _, key := range keys {
			if len(key) > 1024 {
				return nil, fmt.Errorf("invalid_group_value: object key size")
			}
			c, err := canonicalGroupValue(v[key], depth+1, nodes)
			if err != nil {
				return nil, err
			}
			out = append(out, []any{key, c})
		}
		return []any{"object", out}, nil
	}
	text, ok := numberText(value)
	if !ok {
		return nil, fmt.Errorf("invalid_group_value: unsupported value type")
	}
	number, err := canonicalNumber(text)
	if err != nil {
		return nil, fmt.Errorf("invalid_group_value: %w", err)
	}
	return []any{"number", number}, nil
}

func numberText(value any) (string, bool) {
	if number, ok := value.(json.Number); ok {
		return string(number), true
	}
	if value == nil {
		return "", false
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(v.Uint(), 10), true
	case reflect.Float32, reflect.Float64:
		if math.IsNaN(v.Float()) || math.IsInf(v.Float(), 0) {
			return "", false
		}
		return strconv.FormatFloat(v.Float(), 'g', -1, v.Type().Bits()), true
	default:
		return "", false
	}
}

func canonicalNumber(text string) (string, error) {
	if len(text) == 0 || len(text) > 512 || !json.Valid([]byte(text)) || (text[0] != '-' && (text[0] < '0' || text[0] > '9')) {
		return "", fmt.Errorf("invalid numeric value")
	}
	if i := strings.IndexAny(text, "eE"); i >= 0 {
		exp, err := strconv.Atoi(text[i+1:])
		if err != nil || exp > 1024 || exp < -1024 {
			return "", fmt.Errorf("numeric exponent exceeds budget")
		}
	}
	f, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return "", fmt.Errorf("numeric value is not finite")
	}
	rat, ok := new(big.Rat).SetString(text)
	if !ok {
		return "", fmt.Errorf("invalid numeric value")
	}
	return rat.RatString(), nil
}
