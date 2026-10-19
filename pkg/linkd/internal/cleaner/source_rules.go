// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cleaner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"linkd/internal/config"
	"linkd/internal/policy"
	"linkd/internal/textregexp"
)

// ErrFiltered 是正常过滤结果；消费确认但不存储 Event 或进入生命周期。
var ErrFiltered = errors.New("source event filtered")

func sourceField(value any, path string) (any, bool) {
	parts := strings.Split(strings.TrimPrefix(path, "$."), ".")
	for i, part := range parts {
		switch item := value.(type) {
		case map[string]any:
			if exact, ok := item[strings.Join(parts[i:], ".")]; ok {
				return exact, true
			}
			var ok bool
			value, ok = item[part]
			if !ok {
				return nil, false
			}
		case []any:
			n, err := strconv.Atoi(part)
			if err != nil || n < 0 || n >= len(item) {
				return nil, false
			}
			value = item[n]
		default:
			return nil, false
		}
	}
	return value, true
}

func assignSourceField(value map[string]any, path string, assigned any) error {
	parts := strings.Split(strings.TrimPrefix(path, "$."), ".")
	var node any = value
	for i, part := range parts {
		last := i == len(parts)-1
		switch item := node.(type) {
		case map[string]any:
			if last {
				item[part] = assigned
				return nil
			}
			next, ok := item[part]
			if !ok {
				next = map[string]any{}
				item[part] = next
			}
			node = next
		case []any:
			n, err := strconv.Atoi(part)
			if err != nil || n < 0 || n >= len(item) {
				return fmt.Errorf("source assignment array path invalid")
			}
			if last {
				item[n] = assigned
				return nil
			}
			node = item[n]
		default:
			return fmt.Errorf("source assignment path is not an object")
		}
	}
	return nil
}

func (c StandardCleaner) applyRules(ctx context.Context, message RawEventMessage) ([]byte, error) {
	if len(c.Rules) == 0 || string(message.Headers["kac_ingest_mode"]) == "recovery" {
		return message.Payload, nil
	}
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(message.Payload))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	tenant := message.BKTenantID
	if tenant == "" {
		tenant, _ = value["bk_tenant_id"].(string)
	}
	// 1. 仅执行认证后租户的规则，不能从自定义字段切换租户。
	for _, rule := range c.Rules {
		if rule.TenantID != "" && rule.TenantID != tenant {
			continue
		}
		// 2. 每个赋值读取本轮输入事实，与既有简单提取语义一致。
		original, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		var input map[string]any
		decoder := json.NewDecoder(bytes.NewReader(original))
		decoder.UseNumber()
		if err := decoder.Decode(&input); err != nil {
			return nil, err
		}
		for _, a := range rule.Assignments {
			v, ok := sourceField(input, a.Field)
			if !ok {
				v = ""
			}
			if a.Rule == "regexp_extract" {
				pattern, err := textregexp.Compile(a.Value)
				if err != nil {
					return nil, err
				}
				text, ok := v.(string)
				if !ok {
					return nil, fmt.Errorf("source regexp field must be string")
				}
				matches, err := pattern.FindAll(text, 1)
				if err != nil {
					return nil, err
				}
				v = ""
				if len(matches) > 0 {
					v = matches[0].Text
				}
			}
			if err := assignSourceField(value, a.Target, v); err != nil {
				return nil, err
			}
		}
		// 3. 过滤读取赋值后的 standard；正常未命中不生成失败 Event。
		filter, paths, err := config.CompileSourceFilter(rule.Filter)
		if err != nil {
			return nil, err
		}
		if filter != nil {
			fields := map[string]any{}
			for name, path := range paths {
				if v, ok := sourceField(value, path); ok {
					fields[name] = v
				}
			}
			result, err := filter.Match(ctx, policy.Fields{Values: fields})
			if err != nil {
				return nil, err
			}
			if !result.Evaluated {
				return nil, fmt.Errorf("source filter cannot be evaluated")
			}
			if !result.Matched {
				return nil, ErrFiltered
			}
		}
	}
	return json.Marshal(value)
}
