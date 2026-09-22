// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package onemodel

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Filter 描述类型化布尔过滤；叶条件与组合条件互斥。
type Filter struct {
	All      []Filter              `json:"all,omitempty"`
	Any      []Filter              `json:"any,omitempty"`
	Not      *Filter               `json:"not,omitempty"`
	Field    string                `json:"field,omitempty"`
	Type     InstanceAttributeType `json:"type,omitempty"`
	Operator string                `json:"operator,omitempty"`
	Value    any                   `json:"value,omitempty"`
}

// Query 描述租户内一个模型的有界实例查询。
type Query struct {
	ModelID string `json:"model_id"`
	Where   Filter `json:"where"`
	Limit   int    `json:"limit"`
}

// Reader 是普通查询与关联读取共用的 SDK 端口。
type Reader interface {
	Search(context.Context, string, Query) ([]Instance, error)
	Related(context.Context, string, []Instance, string, string, Query) ([]Instance, error)
}

// Document 返回不包含物理 attribute_values 的业务文档。
func (i Instance) Document() map[string]any {
	out := map[string]any{"bk_tenant_id": i.TenantID, "model_id": i.ModelCode, "model_inst_id": i.InstanceID, "entity_uid": i.ModelCode + "|" + i.InstanceID, "attributes": i.Attributes}
	for _, k := range []string{"display_name", "bk_biz_ids", "source", "source_revision"} {
		if v, ok := i.Fields[k]; ok {
			out[k] = v
		}
	}
	return out
}

// Search 在后端筛选并复核完整结果；超过上限、部分失败或重复身份均返回错误。
func (c *Client) Search(ctx context.Context, tenant string, q Query) ([]Instance, error) {
	if ctx == nil || tenant == "" || q.ModelID == "" {
		return nil, fmt.Errorf("tenant and model_id are required")
	}
	if err := validateOneModelIdentity("model code", q.ModelID, 128); err != nil {
		return nil, err
	}
	if q.Limit == 0 {
		q.Limit = 1024
	}
	if q.Limit < 1 || q.Limit > 1024 {
		return nil, fmt.Errorf("query limit must be 1..1024")
	}
	filters := []any{term("bk_tenant_id", tenant), term("model_id", q.ModelID)}
	if !q.Where.Empty() {
		clause, err := q.Where.Compile()
		if err != nil {
			return nil, err
		}
		filters = append(filters, clause)
	}
	rows, err := c.searchAll(ctx, oneModelInstanceIndex, filters, q.Limit+1)
	if err != nil {
		return nil, err
	}
	if len(rows) > q.Limit {
		return nil, fmt.Errorf("onemodel result limit exceeded")
	}
	out := make([]Instance, 0, len(rows))
	seen := map[string]bool{}
	for _, row := range rows {
		i, _, err := parseInstanceSource(row, tenant, InstanceQuery{ModelCode: q.ModelID})
		if err != nil {
			return nil, err
		}
		if seen[i.InstanceID] {
			return nil, fmt.Errorf("%w: duplicate instance identity", ErrInvalidDataSourceResponse)
		}
		seen[i.InstanceID] = true
		out = append(out, i)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].InstanceID < out[j].InstanceID })
	return out, nil
}

// Related 按显式关系和方向展开实例，集合有界并在每一步复核租户和 canonical 身份。
func (c *Client) Related(ctx context.Context, tenant string, roots []Instance, relation, direction string, q Query) ([]Instance, error) {
	if relation == "" || len(roots) == 0 || len(roots) > 1024 {
		return nil, fmt.Errorf("relation and 1..1024 roots are required")
	}
	if direction != "out" && direction != "in" && direction != "both" {
		return nil, fmt.Errorf("relation direction must be out, in or both")
	}
	uids := make([]string, 0, len(roots))
	rootSet := map[string]bool{}
	for _, root := range roots {
		if root.TenantID != tenant {
			return nil, fmt.Errorf("relation root tenant mismatch")
		}
		uid := root.ModelCode + "|" + root.InstanceID
		uids = append(uids, uid)
		rootSet[uid] = true
	}
	ids := map[string]bool{}
	for _, dir := range []string{"out", "in"} {
		if direction != "both" && direction != dir {
			continue
		}
		from, to := "source", "target"
		if dir == "in" {
			from, to = to, from
		}
		rows, err := c.searchAll(ctx, oneModelEdgeIndex, []any{term("bk_tenant_id", tenant), term("producer", "cmdb_fact"), terms(from+"_entity_uid", uids), term(to+"_model_id", q.ModelID), term("relation_identity", relation)}, 1025)
		if err != nil {
			return nil, err
		}
		if len(rows) > 1024 {
			return nil, fmt.Errorf("relation edge limit exceeded")
		}
		for _, row := range rows {
			fromUID, _ := row[from+"_entity_uid"].(string)
			toUID, _ := row[to+"_entity_uid"].(string)
			prefix := q.ModelID + "|"
			if row["bk_tenant_id"] != tenant || row["producer"] != "cmdb_fact" || !rootSet[fromUID] || row[to+"_model_id"] != q.ModelID || row["relation_identity"] != relation || !strings.HasPrefix(toUID, prefix) || len(toUID) == len(prefix) {
				return nil, fmt.Errorf("%w: relation identity mismatch", ErrInvalidDataSourceResponse)
			}
			ids[strings.TrimPrefix(toUID, prefix)] = true
		}
	}
	if len(ids) == 0 {
		return []Instance{}, nil
	}
	if len(ids) > 1024 {
		return nil, fmt.Errorf("related instance limit exceeded")
	}
	values := make([]string, 0, len(ids))
	for id := range ids {
		values = append(values, id)
	}
	sort.Strings(values)
	idFilter := Filter{Field: "model_inst_id", Type: InstanceAttributeKeyword, Operator: "in", Value: values}
	if q.Where.Empty() {
		q.Where = idFilter
	} else {
		q.Where = Filter{All: []Filter{idFilter, q.Where}}
	}
	return c.Search(ctx, tenant, q)
}

// Empty 判断是否没有附加过滤。
func (f Filter) Empty() bool {
	return f.Field == "" && len(f.All) == 0 && len(f.Any) == 0 && f.Not == nil
}

// Compile 校验过滤树并生成后端条件；不允许配置包含租户字段。
func (f Filter) Compile() (map[string]any, error) { return f.compile(0) }

func (f Filter) compile(depth int) (map[string]any, error) {
	if depth > 16 {
		return nil, fmt.Errorf("filter depth exceeded")
	}
	count := 0
	if f.Field != "" {
		count++
	}
	if len(f.All) > 0 {
		count++
	}
	if len(f.Any) > 0 {
		count++
	}
	if f.Not != nil {
		count++
	}
	if count != 1 {
		return nil, fmt.Errorf("filter must select exactly one leaf/all/any/not")
	}
	if f.Not != nil {
		clause, err := f.Not.compile(depth + 1)
		return map[string]any{"bool": map[string]any{"must_not": []any{clause}}}, err
	}
	if len(f.All) > 0 || len(f.Any) > 0 {
		items := f.All
		if len(f.Any) > 0 {
			items = f.Any
		}
		if len(items) > 64 {
			return nil, fmt.Errorf("filter clause limit exceeded")
		}
		clauses := []any{}
		for _, item := range items {
			clause, err := item.compile(depth + 1)
			if err != nil {
				return nil, err
			}
			clauses = append(clauses, clause)
		}
		if len(f.Any) > 0 {
			return map[string]any{"bool": map[string]any{"should": clauses, "minimum_should_match": 1}}, nil
		}
		return map[string]any{"bool": map[string]any{"filter": clauses}}, nil
	}
	field := f.Field
	dynamic := strings.HasPrefix(field, "attributes.")
	if dynamic {
		field = strings.TrimPrefix(field, "attributes.")
		if err := validateFieldName(field); err != nil {
			return nil, err
		}
	} else {
		switch field {
		case "model_inst_id", "entity_uid", "display_name", "bk_biz_ids", "source":
		default:
			return nil, fmt.Errorf("unsupported root query field %q", field)
		}
	}
	slot := map[InstanceAttributeType]string{InstanceAttributeKeyword: "keyword_values", InstanceAttributeLong: "long_values", InstanceAttributeDouble: "double_values", InstanceAttributeBoolean: "boolean_values", InstanceAttributeDatetime: "datetime_values", InstanceAttributeIP: "ip_values"}[f.Type]
	if slot == "" {
		return nil, fmt.Errorf("query field type is required")
	}
	physical := field
	if dynamic {
		physical = "attribute_values." + slot
	}
	op := f.Operator
	negated := false
	switch op {
	case "ne":
		op = "eq"
		negated = true
	case "not_in":
		op = "in"
		negated = true
	case "not_contains":
		op = "contains"
		negated = true
	case "not_regex":
		op = "regex"
		negated = true
	}
	var clause map[string]any
	switch op {
	case "exists":
		clause = map[string]any{"exists": map[string]any{"field": physical}}
	case "in":
		raw, err := json.Marshal(f.Value)
		if err != nil {
			return nil, err
		}
		var values []any
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.UseNumber()
		if err := decoder.Decode(&values); err != nil || len(values) == 0 || len(values) > 1024 {
			return nil, fmt.Errorf("in requires 1..1024 values")
		}
		for i, v := range values {
			v, err = typedFilterValue(f.Type, v)
			if err != nil {
				return nil, err
			}
			values[i] = v
		}
		clause = map[string]any{"terms": map[string]any{physical: values}}
	case "eq", "gt", "gte", "lt", "lte", "contains", "regex":
		value, err := typedFilterValue(f.Type, f.Value)
		if err != nil {
			return nil, err
		}
		switch op {
		case "eq":
			clause = term(physical, value)
		case "contains", "regex":
			str, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("text operator requires string")
			}
			if op == "contains" {
				str = strings.NewReplacer("\\", "\\\\", "*", "\\*", "?", "\\?").Replace(str)
				clause = map[string]any{"wildcard": map[string]any{physical: map[string]any{"value": "*" + str + "*"}}}
			} else {
				clause = map[string]any{"regexp": map[string]any{physical: map[string]any{"value": str}}}
			}
		default:
			clause = map[string]any{"range": map[string]any{physical: map[string]any{op: value}}}
		}
	default:
		return nil, fmt.Errorf("unsupported query operator %q", f.Operator)
	}
	if dynamic {
		clause = map[string]any{"nested": map[string]any{"path": "attribute_values", "score_mode": "none", "query": map[string]any{"bool": map[string]any{"filter": []any{term("attribute_values.field_name", field), clause}}}}}
	}
	if negated {
		clause = map[string]any{"bool": map[string]any{"must_not": []any{clause}}}
	}
	return clause, nil
}

func typedFilterValue(t InstanceAttributeType, v any) (any, error) {
	switch t {
	case InstanceAttributeKeyword, InstanceAttributeIP, InstanceAttributeDatetime:
		if s, ok := v.(string); ok {
			return s, nil
		}
		return nil, fmt.Errorf("string query value required")
	case InstanceAttributeBoolean:
		if b, ok := v.(bool); ok {
			return b, nil
		}
		return nil, fmt.Errorf("boolean query value required")
	case InstanceAttributeLong, InstanceAttributeDouble:
		s := fmt.Sprint(v)
		if t == InstanceAttributeLong {
			i, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("integer query value required")
			}
			return i, nil
		}
		n, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsInf(n, 0) || math.IsNaN(n) {
			return nil, fmt.Errorf("finite numeric query value required")
		}
		return n, nil
	default:
		return nil, fmt.Errorf("unsupported field type")
	}
}
