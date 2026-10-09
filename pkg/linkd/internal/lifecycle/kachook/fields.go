// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kachook

import (
	"encoding/json"
	"fmt"

	"linkd/internal/domain"
	"linkd/internal/enrich/kingeye"
	"linkd/internal/enrich/view"
	"linkd/internal/jsonpath"
)

func mappedPayload(message kingeye.AlarmMessage, alert domain.Alert, fields map[string]string) ([]byte, error) {
	if err := kingeye.ValidateFieldMappings(fields); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(message)
	if err != nil {
		return encoded, err
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &result); err != nil {
		return nil, err
	}
	effective, err := view.EnrichedAlert(alert)
	if err != nil {
		return nil, err
	}
	document, err := domain.AlertDocument(effective)
	if err != nil {
		return nil, err
	}
	// 目录由 KAC 的租户字段注册表发布到丰富快照；只取有效视图，绝不展开来源 payload。
	extra, _ := document["extra_data"].(map[string]any)
	if catalog, present := extra["__kac_custom_fields"]; present {
		items, ok := catalog.([]any)
		if !ok || len(items) > 128 {
			return nil, fmt.Errorf("invalid KAC custom field catalog")
		}
		declared := make(map[string]string, len(items))
		for _, item := range items {
			name, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("invalid KAC custom field name")
			}
			key, _ := json.Marshal(name)
			declared[name] = "$.extra_data[" + string(key) + "]"
		}
		if err := kingeye.ValidateFieldMappings(declared); err != nil {
			return nil, err
		}
		for name := range declared {
			if value, found := extra[name]; found {
				result[name], err = json.Marshal(value)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	for key, path := range fields {
		target, _ := jsonpath.ParseTarget(path)
		value, found := target.Get(document)
		if !found {
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		result[key] = encoded
	}
	return json.Marshal(result)
}

func mergeExtraInfo(base, overlay map[string]any) map[string]any {
	result := make(map[string]any, len(base)+len(overlay))
	for key, value := range base {
		result[key] = jsonpath.Clone(value)
	}
	for key, value := range overlay {
		if fields, ok := value.(map[string]any); ok {
			previous, _ := result[key].(map[string]any)
			result[key] = mergeExtraInfo(previous, fields)
		} else {
			result[key] = jsonpath.Clone(value)
		}
	}
	return result
}
