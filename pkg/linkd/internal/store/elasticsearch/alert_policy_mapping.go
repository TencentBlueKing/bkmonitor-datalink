// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearchstore

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
)

func alertPolicyProperties() map[string]map[string]any {
	return map[string]map[string]any{
		"shield_main_alert_ids": keywordProperty(),
		"end_operation":         opaqueObjectProperty(),
		"last_shield_operation": opaqueObjectProperty(),
		"action_work":           {"type": "boolean"},
		"action_pending":        opaqueObjectProperty(),
		"projection_work":       {"type": "boolean"},
		"projection":            {"properties": map[string]any{"targets": opaqueObjectProperty()}},
		"revision":              {"type": "long"},
		"merge_work":            {"type": "boolean"},
		"merge_change":          {"properties": map[string]any{"kind": keywordProperty(), "relation_id": keywordProperty(), "operation_id": keywordProperty(), "window_id": keywordProperty(), "effective_at": dateNanosProperty(), "before": opaqueObjectProperty(), "after": opaqueObjectProperty(), "action_ready": map[string]any{"type": "boolean"}}},
		"merge":                 {"properties": map[string]any{"role": keywordProperty(), "state": keywordProperty(), "pending": opaqueObjectProperty(), "relation_ids": keywordProperty(), "operation_id": keywordProperty(), "relations_ready": map[string]any{"type": "boolean"}}},
		"shield":                {"properties": map[string]any{"active": map[string]any{"type": "boolean"}, "next_check_at": dateNanosProperty(), "bindings": opaqueObjectProperty()}},
		"admission":             {"properties": map[string]any{"admitted_at": dateNanosProperty(), "severity": keywordProperty(), "cause_type": keywordProperty(), "cause_id": keywordProperty()}},
		"policy_tags":           {"type": "long"},
		"policy_change":         {"properties": map[string]any{"operation_id": keywordProperty(), "effective_at": dateNanosProperty(), "before": opaqueObjectProperty(), "after": opaqueObjectProperty()}},
	}
}

// 仅对已验证为当前部署的索引追加策略字段；包括活动与归档索引，不重建或清除历史数据。
func (r *Repository) ensureAlertPolicyMapping(ctx context.Context, index string, properties map[string]map[string]any) error {
	missing := map[string]any{}
	for name, expected := range alertPolicyProperties() {
		existing, ok := properties[name]
		if !ok {
			missing[name] = expected
			continue
		}
		addition, err := mappingAdditions(existing, expected)
		if err != nil {
			return fmt.Errorf("alert index %q has incompatible %s mapping: %w", index, name, err)
		}
		if len(addition) > 0 {
			missing[name] = addition
		}
	}
	if len(missing) == 0 {
		return nil
	}
	body, err := json.Marshal(map[string]any{"properties": missing})
	if err != nil {
		return err
	}
	return r.performJSON(ctx, http.MethodPut, "/"+index+"/_mapping", nil, body, nil)
}

func mappingContains(current, expected map[string]any) bool {
	for key, wanted := range expected {
		if child, ok := wanted.(map[string]any); ok {
			actual, ok := current[key].(map[string]any)
			if !ok || !mappingContains(actual, child) {
				return false
			}
		} else if !reflect.DeepEqual(current[key], wanted) {
			return false
		}
	}
	return true
}

// mappingAdditions 只增加对象中新出现的属性，既有叶子类型/索引语义不能原地改写。
func mappingAdditions(current, expected map[string]any) (map[string]any, error) {
	wanted, object := expected["properties"].(map[string]any)
	if !object {
		if !mappingContains(current, expected) {
			return nil, fmt.Errorf("existing leaf mapping differs")
		}
		return nil, nil
	}
	if kind, ok := current["type"]; ok && kind != "object" {
		return nil, fmt.Errorf("existing field is not an object")
	}
	if current["enabled"] == false {
		return nil, fmt.Errorf("existing object is disabled")
	}
	existing, ok := current["properties"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("existing object has no properties")
	}
	for key, value := range expected {
		if key != "properties" && !reflect.DeepEqual(current[key], value) {
			return nil, fmt.Errorf("existing object mapping differs")
		}
	}
	additions := map[string]any{}
	for key, value := range wanted {
		have, found := existing[key]
		if !found {
			additions[key] = value
			continue
		}
		oldMap, oldOK := have.(map[string]any)
		newMap, newOK := value.(map[string]any)
		if !oldOK || !newOK {
			return nil, fmt.Errorf("invalid nested mapping")
		}
		delta, err := mappingAdditions(oldMap, newMap)
		if err != nil {
			return nil, err
		}
		if len(delta) > 0 {
			additions[key] = delta
		}
	}
	if len(additions) == 0 {
		return nil, nil
	}
	return map[string]any{"properties": additions}, nil
}
