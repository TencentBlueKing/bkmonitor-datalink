// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package custom

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"linkd/internal/domain"
	"linkd/internal/jsonpath"
	"linkd/internal/onemodel"
)

func identityFields() []string {
	return []string{"bk_obj_id", "model_id", "model_name", "bk_inst_id", "model_inst_id"}
}

func compileIdentity(i *Identity) error {
	if i.BKObjID == "" || len(i.BKObjID) > 128 || len(i.ModelName) > 1024 {
		return fmt.Errorf("identity requires bounded bk_obj_id and model_name")
	}
	if i.Fields == nil {
		i.Fields = map[string]string{}
	}
	allowed := map[string]bool{}
	for _, name := range identityFields() {
		allowed[name] = true
	}
	for name := range i.Fields {
		if !allowed[name] {
			return fmt.Errorf("unsupported identity field %q", name)
		}
	}
	i.targets = map[string]jsonpath.Target{}
	for _, name := range identityFields() {
		path := i.Fields[name]
		if path == "" {
			path = "$.labels." + name
			i.Fields[name] = path
		}
		if err := validateTarget(path); err != nil {
			return err
		}
		target, err := jsonpath.ParseTarget(path)
		if err != nil {
			return err
		}
		for _, previous := range i.targets {
			if target.Overlaps(previous) {
				return fmt.Errorf("identity targets overlap")
			}
		}
		i.targets[name] = target
	}
	return nil
}

// identityProvided 沿用旧身份分组的真值判断；数值 0 和空容器不构成实例身份。
func identityProvided(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return x != ""
	case bool:
		return x
	case json.Number:
		number, err := x.Float64()
		return err == nil && number != 0
	case int:
		return x != 0
	case int64:
		return x != 0
	case float64:
		return x != 0
	case []any:
		return len(x) > 0
	case []string:
		return len(x) > 0
	case []int64:
		return len(x) > 0
	default:
		return true
	}
}

func (i *Identity) read(document map[string]any, field string) any {
	if target, ok := i.targets[field]; ok {
		if v, found := target.Get(document); found && identityProvided(v) {
			return v
		}
	}
	for _, root := range []string{"labels", "extra_data", "dimensions"} {
		if object, ok := document[root].(map[string]any); ok && identityProvided(object[field]) {
			return object[field]
		}
	}
	return nil
}

func (i *Identity) patches(values map[string]any) ([]domain.EnrichAssignment, error) {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]domain.EnrichAssignment, 0, len(names))
	for _, name := range names {
		path := i.Fields[name]
		if path == "" {
			path = "$.extra_data." + name
		}
		target, ok := i.targets[name]
		if !ok {
			var err error
			target, err = jsonpath.ParseTarget(path)
			if err != nil {
				return nil, err
			}
		}
		assignment, err := domain.NewEnrichAssignment(target, values[name])
		if err != nil {
			return nil, err
		}
		out = append(out, assignment)
	}
	return out, nil
}

func (r *run) matchRule(ctx context.Context, rule Rule, _ *RuntimeResult) (bool, error) {
	if rule.TenantID != "" && rule.TenantID != r.tenant {
		return false, nil
	}
	if rule.Identity == nil {
		return r.condition(ctx, rule.When, r.environment(nil))
	}
	identity := rule.Identity
	for _, field := range []string{"model_id", "cw_object_model_code"} {
		if model := identity.read(r.current, field); identityProvided(model) {
			return fmt.Sprint(model) == rule.Lookup.ModelID, nil
		}
	}
	if object := identity.read(r.current, "bk_obj_id"); identityProvided(object) {
		return fmt.Sprint(object) == identity.BKObjID, nil
	}
	if rule.When == nil {
		return false, nil
	}
	// 对象条件只选择候选方案；定位成功之前不提交中间身份。
	return r.condition(ctx, rule.When, r.environment(nil))
}

func (r *run) bindInstance(ctx context.Context, rule Rule, lookup map[string]any, result *RuntimeResult) error {
	i := rule.Identity
	id := lookup["model_inst_id"]
	bkID := id
	attribute := "bk_inst_id"
	switch i.BKObjID {
	case "host", "biz", "set", "module":
		attribute = "bk_" + i.BKObjID + "_id"
	}
	attributes, _ := lookup["attributes"].(map[string]any)
	if identityProvided(attributes[attribute]) {
		bkID = attributes[attribute]
	}
	patches, err := i.patches(map[string]any{"bk_obj_id": i.BKObjID, "model_id": rule.Lookup.ModelID, "model_name": i.ModelName, "bk_inst_id": bkID, "model_inst_id": id})
	if err != nil {
		return err
	}
	if err = r.commit(rule.ID, "identity", patches, result); err != nil {
		return err
	}
	// 身份定位成功独立于后续展示字段是否成功。
	r.fieldSucceeded = true
	return r.fillTopology(ctx, rule, lookup, result, len(rule.Assignments) > 0)
}

func (r *run) fillTopology(ctx context.Context, rule Rule, lookup map[string]any, result *RuntimeResult, includeHostTopology bool) error {
	i := rule.Identity
	id := lookup["model_inst_id"]
	attributes, _ := lookup["attributes"].(map[string]any)
	// 旧内置拓扑在属性赋值前只补空字段；属性规则仍可以显式覆盖。
	values := map[string]any{}
	instanceBizID := attributes["bk_biz_id"]
	if !identityProvided(instanceBizID) {
		if ids, ok := lookup["bk_biz_ids"].([]any); ok && len(ids) == 1 {
			instanceBizID = ids[0]
		}
	}
	currentBizID := i.read(r.current, "bk_biz_id")
	if !identityProvided(currentBizID) && identityProvided(instanceBizID) {
		values["bk_biz_id"] = instanceBizID
		currentBizID = instanceBizID
	}
	// 实例冗余名称只能与其归属 ID 配对，不能补到来源明确指定的另一业务。
	if !identityProvided(i.read(r.current, "bk_biz_name")) && identityProvided(attributes["bk_biz_name"]) &&
		identityProvided(instanceBizID) && fmt.Sprint(currentBizID) == fmt.Sprint(instanceBizID) {
		values["bk_biz_name"] = attributes["bk_biz_name"]
	}
	patches, err := i.patches(values)
	if err != nil {
		return err
	}
	if err = r.commit(rule.ID, "topology", patches, result); err != nil {
		return err
	}
	values = map[string]any{}
	if i.BKObjID == "host" && includeHostTopology {
		reader, ok := r.sources.Instances.(interface {
			FindCMDBTopology(context.Context, string, string) (onemodel.ResourceTopology, bool, error)
		})
		if !ok {
			return fmt.Errorf("CMDB topology datasource unavailable")
		}
		if err = r.externalCall(); err != nil {
			return err
		}
		top, found, e := reader.FindCMDBTopology(ctx, r.tenant, fmt.Sprint(id))
		if e != nil {
			return e
		}
		if found {
			for name, value := range map[string]any{"bk_biz_id": top.BKBizID, "bk_biz_name": top.BKBizName, "bk_set_id": top.BKSetIDs, "bk_set_name": top.BKSetNames, "bk_module_id": top.BKModuleIDs, "bk_module_name": top.BKModuleNames} {
				if current := i.read(r.current, "bk_biz_id"); name == "bk_biz_name" && identityProvided(current) && fmt.Sprint(current) != fmt.Sprint(top.BKBizID) {
					continue
				}
				if !identityProvided(i.read(r.current, name)) && identityProvided(value) {
					values[name] = value
				}
			}
		}
	}
	patches, err = i.patches(values)
	if err != nil {
		return err
	}
	return r.commit(rule.ID, "topology", patches, result)
}

// kingeyeText 保留旧 CMDB 属性赋值的 Python 标量文本；缺失属性由 Value.default 处理。
func kingeyeText(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case bool:
		if x {
			return "True"
		}
		return "False"
	case []any:
		parts := make([]string, len(x))
		for i, value := range x {
			if text, ok := value.(string); ok {
				parts[i] = "'" + strings.ReplaceAll(strings.ReplaceAll(text, "\\", "\\\\"), "'", "\\'") + "'"
			} else {
				parts[i] = kingeyeText(value)
			}
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		names := make([]string, 0, len(x))
		for name := range x {
			names = append(names, name)
		}
		sort.Strings(names)
		parts := make([]string, 0, len(names))
		for _, name := range names {
			parts = append(parts, strconv.Quote(name)+": "+kingeyeText(x[name]))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	default:
		return fmt.Sprint(v)
	}
}

func (r *run) fallbackTopology(ctx context.Context, rules []Rule, result *RuntimeResult) error {
	r.state.Rollback(r.start)
	r.patchBytes = 2
	for _, rule := range rules {
		if rule.Identity == nil || rule.TenantID != "" && rule.TenantID != r.tenant {
			continue
		}
		i := rule.Identity
		obj, inst := i.read(r.current, "bk_obj_id"), i.read(r.current, "bk_inst_id")
		if !identityProvided(obj) || !identityProvided(inst) {
			if alias := i.read(r.current, "cw_object_model_code"); fmt.Sprint(alias) == rule.Lookup.ModelID {
				if !identityProvided(obj) {
					obj = i.BKObjID
				}
				if !identityProvided(inst) {
					inst = i.read(r.current, "cw_object_model_inst_id")
				}
			}
		}
		if fmt.Sprint(obj) != i.BKObjID || !identityProvided(inst) {
			continue
		}
		if r.sources.Instances == nil {
			return fmt.Errorf("onemodel datasource unavailable")
		}
		id, err := scalarText(inst)
		if err != nil {
			return err
		}
		if err = r.externalCall(); err != nil {
			return err
		}
		items, err := r.sources.Instances.Search(ctx, r.tenant, onemodel.Query{ModelID: rule.Lookup.ModelID, First: true, Limit: 1, Where: onemodel.Filter{Field: "model_inst_id", Type: onemodel.InstanceAttributeKeyword, Operator: "eq", Value: id}})
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return nil
		}
		return r.fillTopology(ctx, rule, items[0].Document(), result, true)
	}
	return nil
}
