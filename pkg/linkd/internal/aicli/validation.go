// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package aicli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, problem("io_error", "读取失败")
	}
	if int64(len(raw)) > limit {
		return nil, problem("size_limit", "内容超过大小上限")
	}
	return raw, nil
}

// decodeJSON 拒绝重复键和过深结构，保证 dry-run 所展示的参数与实际 JSON 请求含义一致。
// 数字使用 json.Number 保留 revision 和实体 ID 的精度。
func decodeJSON(raw []byte) (any, error) {
	if !utf8.Valid(raw) {
		return nil, problem("invalid_argument", "JSON 必须为 UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := decodeValue(decoder, 0)
	if err != nil {
		return nil, problem("invalid_argument", "JSON 无效、重复键或嵌套超过 64 层")
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, problem("invalid_argument", "只能提供一个 JSON 值")
	}
	return value, nil
}

func decodeValue(decoder *json.Decoder, depth int) (any, error) {
	if depth > 64 {
		return nil, errors.New("depth limit")
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return token, nil
	}
	switch delimiter {
	case '{':
		object := map[string]any{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, errors.New("invalid key")
			}
			if _, duplicate := object[key]; duplicate {
				return nil, errors.New("duplicate key")
			}
			value, err := decodeValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return object, nil
	case '[':
		items := []any{}
		for decoder.More() {
			value, err := decodeValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			items = append(items, value)
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return items, nil
	default:
		return nil, errors.New("invalid delimiter")
	}
}

func parsePairs(pairs []string, fields []parameter, path bool) (map[string]string, error) {
	values := map[string]string{}
	for _, pair := range pairs {
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			return nil, problem("invalid_argument", "参数格式必须为 key=value")
		}
		if _, exists := values[key]; exists {
			return nil, problem("invalid_argument", "不允许重复参数")
		}
		values[key] = value
	}
	object := map[string]any{}
	for key, value := range values {
		index := slices.IndexFunc(fields, func(p parameter) bool { return p.Name == key })
		if index < 0 {
			return nil, problem("invalid_argument", "存在未登记的参数；请使用 api describe 查看支持的参数")
		}
		if fields[index].Type == "integer" {
			object[key] = json.Number(value)
		} else {
			object[key] = value
		}
		// 不允许身份参数变成另一段路由；不会二次解码用户提供的百分号转义。
		if path && (value == "." || value == ".." || strings.ContainsAny(value, "/\\%")) {
			return nil, problem("invalid_argument", "路径身份必须是单段原始值，不允许斜线、点路径或百分号转义")
		}
	}
	if err := validateObject(object, fields); err != nil {
		return nil, err
	}
	return values, nil
}

func validateObject(object map[string]any, fields []parameter) error {
	for key := range object {
		if !slices.ContainsFunc(fields, func(p parameter) bool { return p.Name == key }) {
			return problem("invalid_argument", "JSON 包含未登记字段；请使用 api describe 查看请求结构")
		}
	}
	for _, field := range fields {
		value, exists := object[field.Name]
		if !exists {
			if field.Required {
				return problem("invalid_argument", field.Name+" 为必填参数")
			}
			continue
		}
		if err := validateParameter(value, field); err != nil {
			return err
		}
	}
	return nil
}

func validateParameter(value any, field parameter) error {
	invalid := func() error {
		return problem("invalid_argument", fmt.Sprintf("%s 的类型、格式或取值范围无效；请使用 api describe", field.Name))
	}
	switch field.Type {
	case "string":
		s, ok := value.(string)
		if !ok || !utf8.ValidString(s) || strings.ContainsAny(s, "\x00\r\n") || (field.Required && strings.TrimSpace(s) == "") || (field.Max > 0 && int64(len(s)) > field.Max) {
			return invalid()
		}
		if len(field.Enum) > 0 && !slices.Contains(field.Enum, s) {
			return invalid()
		}
		switch field.Format {
		case "datetime":
			if _, err := time.Parse(time.RFC3339Nano, s); err != nil || !strings.HasSuffix(s, "Z") {
				return invalid()
			}
		case "uuid":
			if !regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-8][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`).MatchString(s) {
				return invalid()
			}
		case "source-id":
			if !regexp.MustCompile(`^[a-zA-Z0-9_-]{1,32}$`).MatchString(s) {
				return invalid()
			}
		case "hook-name":
			if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`).MatchString(s) {
				return invalid()
			}
		}
	case "integer":
		n, ok := value.(json.Number)
		if !ok {
			return invalid()
		}
		v, err := strconv.ParseInt(n.String(), 10, 64)
		if err != nil || v < field.Min || v > field.Max {
			return invalid()
		}
	case "object":
		m, ok := value.(map[string]any)
		if !ok || m == nil {
			return invalid()
		}
		if field.Fields != nil {
			return validateObject(m, field.Fields)
		}
	case "array":
		items, ok := value.([]any)
		if !ok || int64(len(items)) < field.Min || int64(len(items)) > field.Max {
			return invalid()
		}
		for _, item := range items {
			m, ok := item.(map[string]any)
			if !ok {
				return invalid()
			}
			if err := validateObject(m, field.Fields); err != nil {
				return err
			}
		}
	default:
		return invalid()
	}
	return nil
}

func validateRequest(op operation, paths, query map[string]string, body map[string]any) error {
	if len(op.BodyParams) > 0 {
		if err := validateObject(body, op.BodyParams); err != nil {
			return err
		}
		encoded, err := json.Marshal(body)
		if err != nil || int64(len(encoded)) > op.BodyLimit {
			return problem("size_limit", "JSON 编码后的请求超过接口大小上限")
		}
	}
	if from, to := query["from"], query["to"]; from != "" && to != "" {
		start, _ := time.Parse(time.RFC3339Nano, from)
		end, _ := time.Parse(time.RFC3339Nano, to)
		if end.Before(start) {
			return problem("invalid_argument", "to 不能早于 from")
		}
	}
	switch op.Name {
	case "event-sources.apply":
		spec := body["spec"].(map[string]any)
		if spec["event_source_id"] != paths["id"] {
			return problem("invalid_argument", "spec.event_source_id 必须匹配路径中的 id")
		}
	case "enrich.preview":
		input := body["input"].(map[string]any)
		_, id := input["alert_id"]
		alert, raw := input["alert"]
		if id == raw {
			return problem("invalid_argument", "input.alert_id 与 input.alert 必须二选一")
		}
		if raw {
			for _, key := range []string{"bk_tenant_id", "event_source_id"} {
				if value, exists := alert.(map[string]any)[key]; exists && value != body[key] {
					return problem("invalid_argument", "临时告警的租户或来源与请求不匹配")
				}
			}
		}
	}
	return nil
}
