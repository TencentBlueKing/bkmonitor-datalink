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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"linkd/internal/domain"
	"linkd/internal/jsonpath"
	"linkd/internal/onemodel"
)

// ErrMissing 表示正常的缺失/未命中，不包含依赖故障。
var ErrMissing = errors.New("enrichment value missing")

// DisplayReader 提供只读展示值转换，租户由执行器传递。
type DisplayReader interface {
	Format(context.Context, string, string, string, any) (any, error)
}

// Sources 注入查询和展示能力；普通字段规则不依赖外部连接。
type Sources struct {
	Instances onemodel.Reader
	Display   DisplayReader
}

// Trace 描述规则或操作的执行状态；Code 是脱敏稳定原因码。
type Trace struct {
	RuleID               string              `json:"rule_id"`
	OperationID          string              `json:"operation_id,omitempty"`
	Status               domain.EnrichStatus `json:"status"`
	Code                 string              `json:"code,omitempty"`
	Matches              int                 `json:"matches,omitempty"`
	DurationMilliseconds int64               `json:"duration_milliseconds"`
}

// Result 只返回已提交补丁和执行记录；调用方不必信任部分修改的工作树。
type Result struct {
	Status  domain.EnrichStatus
	Patches []domain.EnrichPatch
	Trace   []Trace
}

type run struct {
	tenant   string
	original map[string]any
	current  map[string]any
	sources  Sources
	calls    int
	cache    map[string][]onemodel.Instance
}

// Execute 对输入副本顺序执行规则；失败操作原子回滚，父取消返回 error。
func (p *Program) Execute(ctx context.Context, original, current map[string]any, tenant string, sources Sources) (Result, error) {
	if ctx == nil || tenant == "" {
		return Result{}, fmt.Errorf("context and tenant are required")
	}
	if err := jsonpath.ValidateTree(original); err != nil {
		return Result{}, err
	}
	if err := jsonpath.ValidateTree(current); err != nil {
		return Result{}, err
	}
	r := &run{tenant: tenant, original: jsonpath.Clone(original).(map[string]any), current: jsonpath.Clone(current).(map[string]any), sources: sources, cache: map[string][]onemodel.Instance{}}
	result := Result{Patches: []domain.EnrichPatch{}, Trace: []Trace{}}
	ruleStates := []domain.EnrichStatus{}
	for _, rule := range p.config.Rules {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		start := time.Now()
		before := len(result.Patches)
		failed := false
		skipped := false
		code := ""
		env := r.environment(nil)
		matches, err := r.condition(ctx, rule.When, env)
		if err == nil && !matches {
			skipped = true
		} else if err != nil {
			skipped = errors.Is(err, ErrMissing)
			failed = !skipped
			code = errorCode(err)
		} else if p.kind == "fields" {
			for _, op := range rule.Operations {
				opStart := time.Now()
				patches, err := r.operation(ctx, op)
				state := domain.EnrichStatusSucceeded
				if err == nil {
					err = r.commit(rule.ID, op.ID, patches, &result)
				}
				if err != nil {
					skipped = errors.Is(err, ErrMissing)
					failed = !skipped
					code = errorCode(err)
					state = domain.EnrichStatusFailed
					if skipped {
						state = domain.EnrichStatusSkipped
					}
				}
				result.Trace = append(result.Trace, Trace{RuleID: rule.ID, OperationID: op.ID, Status: state, Code: errorCode(err), DurationMilliseconds: time.Since(opStart).Milliseconds()})
				if err != nil {
					break
				}
			}
		} else {
			lookup, n, err := r.lookup(ctx, rule)
			if err == nil {
				env = r.environment(map[string]any{"lookup": lookup})
				patches, assignErr := r.assign(ctx, rule.Assignments, env)
				err = assignErr
				if err == nil {
					err = r.commit(rule.ID, "", patches, &result)
				}
			}
			if err != nil {
				skipped = errors.Is(err, ErrMissing)
				failed = !skipped
				code = errorCode(err)
			}
			result.Trace = append(result.Trace, Trace{RuleID: rule.ID, OperationID: "lookup", Status: stateFor(err), Code: errorCode(err), Matches: n})
		}
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		state := domain.EnrichStatusSucceeded
		if failed {
			state = domain.EnrichStatusFailed
		}
		if skipped {
			state = domain.EnrichStatusSkipped
		}
		if (failed || skipped) && len(result.Patches) > before {
			state = domain.EnrichStatusPartial
		}
		ruleStates = append(ruleStates, state)
		result.Trace = append(result.Trace, Trace{RuleID: rule.ID, Status: state, Code: code, DurationMilliseconds: time.Since(start).Milliseconds()})
	}
	result.Status = Aggregate(ruleStates)
	return result, nil
}

// Aggregate 统一空链、跳过、成功、部分成功和失败的聚合语义。
func Aggregate(states []domain.EnrichStatus) domain.EnrichStatus {
	if len(states) == 0 {
		return domain.EnrichStatusSucceeded
	}
	ok, fail, skip := 0, 0, 0
	for _, s := range states {
		switch s {
		case domain.EnrichStatusSucceeded:
			ok++
		case domain.EnrichStatusFailed:
			fail++
		case domain.EnrichStatusSkipped:
			skip++
		default:
			return domain.EnrichStatusPartial
		}
	}
	if skip == len(states) {
		return domain.EnrichStatusSkipped
	}
	if ok == len(states)-skip {
		return domain.EnrichStatusSucceeded
	}
	if fail == len(states)-skip {
		return domain.EnrichStatusFailed
	}
	return domain.EnrichStatusPartial
}

func stateFor(err error) domain.EnrichStatus {
	if err == nil {
		return domain.EnrichStatusSucceeded
	}
	if errors.Is(err, ErrMissing) {
		return domain.EnrichStatusSkipped
	}
	return domain.EnrichStatusFailed
}

func errorCode(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrMissing) {
		return "missing_field"
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "cancelled"
	}
	return "invalid_field_or_dependency"
}

func (r *run) environment(extra map[string]any) map[string]any {
	env := map[string]any{"original": r.original, "alert": r.current}
	for k, v := range extra {
		env[k] = v
	}
	return env
}

func (r *run) commit(rule, operation string, patches []domain.EnrichPatch, result *Result) error {
	if len(result.Patches)+len(patches) > 4096 {
		return fmt.Errorf("patch budget exceeded")
	}
	for i := range patches {
		patches[i].RuleID = rule
		patches[i].OperationID = operation
	}
	current, err := domain.ApplyEnrichPatches(r.current, patches)
	if err != nil {
		return err
	}
	all := append(append([]domain.EnrichPatch(nil), result.Patches...), patches...)
	data, err := json.Marshal(all)
	if err != nil {
		return err
	}
	if len(data) > 1<<20 {
		return fmt.Errorf("patch payload budget exceeded")
	}
	r.current = current
	result.Patches = all
	return nil
}

func (r *run) assign(ctx context.Context, assignments []Assignment, env map[string]any) ([]domain.EnrichPatch, error) {
	out := make([]domain.EnrichPatch, 0, len(assignments))
	for _, assignment := range assignments {
		value, err := r.value(ctx, &assignment.Value, env)
		if err != nil {
			return nil, err
		}
		patch, err := domain.NewEnrichPatch(assignment.Target, value)
		if err != nil {
			return nil, err
		}
		out = append(out, patch)
	}
	return out, nil
}

func (r *run) operation(ctx context.Context, op Operation) ([]domain.EnrichPatch, error) {
	env := r.environment(nil)
	switch op.Type {
	case "assign":
		return r.assign(ctx, op.Assignments, env)
	case "replace":
		v, found := op.target.Get(r.current)
		if !found {
			return nil, ErrMissing
		}
		text, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("replace target is not text")
		}
		for _, rep := range op.Replacements {
			text = strings.ReplaceAll(text, rep.From, rep.To)
			if len(text) > 1<<20 {
				return nil, fmt.Errorf("replace result limit exceeded")
			}
		}
		patch, err := domain.NewEnrichPatch(op.Target, text)
		if err != nil {
			return nil, err
		}
		return []domain.EnrichPatch{patch}, nil
	case "extract":
		v, err := r.value(ctx, op.Source, env)
		if err != nil {
			return nil, err
		}
		text, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("extract source is not text")
		}
		matches := op.regex.FindAllStringSubmatch(text, 1025)
		if len(matches) > 1024 {
			return nil, fmt.Errorf("extract match limit exceeded")
		}
		items := make([]any, 0, len(matches))
		for _, m := range matches {
			named := map[string]any{}
			for i, name := range op.regex.SubexpNames() {
				if i > 0 && name != "" {
					named[name] = m[i]
				}
			}
			groups := make([]any, 0, len(m)-1)
			for _, g := range m[1:] {
				groups = append(groups, g)
			}
			items = append(items, map[string]any{"text": m[0], "groups": groups, "named_groups": named})
		}
		env["extraction"] = map[string]any{"matches": items}
		return r.assign(ctx, op.Assignments, env)
	default:
		return nil, fmt.Errorf("unknown operation")
	}
}

func (r *run) value(ctx context.Context, v *Value, env map[string]any) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if v == nil {
		return nil, ErrMissing
	}
	var out any
	var err error
	switch {
	case len(v.Literal) > 0:
		d := json.NewDecoder(bytes.NewReader(v.Literal))
		d.UseNumber()
		err = d.Decode(&out)
	case v.query != nil:
		var nodes []any
		nodes, err = v.query.Select(ctx, env)
		if err == nil {
			if v.Select == "all" {
				out = []any(nodes)
			} else if len(nodes) == 0 {
				err = ErrMissing
			} else if len(nodes) > 1 {
				err = fmt.Errorf("JSONPath expected one node")
			} else {
				out = nodes[0]
			}
		}
	case v.Template != nil:
		variables := map[string]string{}
		names := make([]string, 0, len(v.Variables))
		for name := range v.Variables {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			variable := v.Variables[name]
			value, e := r.value(ctx, &variable, env)
			if e != nil {
				return nil, e
			}
			s, e := scalarText(value)
			if e != nil {
				return nil, e
			}
			variables[name] = s
		}
		out = variablePattern.ReplaceAllStringFunc(*v.Template, func(token string) string { return variables[token[2:len(token)-1]] })
	default:
		err = ErrMissing
	}
	if errors.Is(err, ErrMissing) && v.Default != nil {
		out, err = r.value(ctx, v.Default, env)
	}
	if err != nil {
		return nil, err
	}
	for _, t := range v.Transforms {
		out, err = r.transform(ctx, t, out)
		if err != nil {
			return nil, err
		}
	}
	if err := jsonpath.ValidateTree(out); err != nil {
		return nil, err
	}
	return out, nil
}

func scalarText(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case bool:
		return strconv.FormatBool(x), nil
	case json.Number:
		return x.String(), nil
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	case int:
		return strconv.Itoa(x), nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case nil:
		return "", fmt.Errorf("null is not text")
	default:
		return "", fmt.Errorf("template requires scalar values; use join explicitly")
	}
}

func (r *run) transform(ctx context.Context, t Transform, v any) (any, error) {
	switch t.Type {
	case "string":
		return scalarText(v)
	case "number":
		s, err := scalarText(v)
		if err != nil {
			return nil, err
		}
		n, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return nil, fmt.Errorf("invalid finite number")
		}
		return n, nil
	case "bool":
		if b, ok := v.(bool); ok {
			return b, nil
		}
		s, err := scalarText(v)
		if err != nil {
			return nil, err
		}
		switch strings.ToLower(s) {
		case "true", "1":
			return true, nil
		case "false", "0":
			return false, nil
		default:
			return nil, fmt.Errorf("invalid boolean")
		}
	case "join":
		a, ok := v.([]any)
		if !ok {
			return nil, fmt.Errorf("join requires array")
		}
		values := make([]string, len(a))
		for i, x := range a {
			s, err := scalarText(x)
			if err != nil {
				return nil, err
			}
			values[i] = s
		}
		return strings.Join(values, t.Separator), nil
	case "map":
		key, err := scalarText(v)
		if err != nil {
			return nil, err
		}
		mapped, ok := t.Mapping[key]
		if !ok {
			return nil, ErrMissing
		}
		return jsonpath.Clone(mapped), nil
	case "regex_extract":
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("regexp source must be text")
		}
		m := t.regex.FindStringSubmatch(s)
		if len(m) == 0 {
			return nil, ErrMissing
		}
		return m[t.Group], nil
	case "display":
		if r.sources.Display == nil {
			return nil, fmt.Errorf("display datasource unavailable")
		}
		if err := r.externalCall(); err != nil {
			return nil, err
		}
		return r.sources.Display.Format(ctx, r.tenant, t.ModelID, t.Field, v)
	default:
		return nil, fmt.Errorf("unknown transform")
	}
}

func (r *run) condition(ctx context.Context, c *Condition, env map[string]any) (bool, error) {
	if c == nil {
		return true, nil
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if c.Not != nil {
		value, err := r.condition(ctx, c.Not, env)
		return !value, err
	}
	if len(c.All) > 0 {
		for i := range c.All {
			v, err := r.condition(ctx, &c.All[i], env)
			if err != nil || !v {
				return false, err
			}
		}
		return true, nil
	}
	if len(c.Any) > 0 {
		for i := range c.Any {
			v, err := r.condition(ctx, &c.Any[i], env)
			if err != nil {
				return false, err
			}
			if v {
				return true, nil
			}
		}
		return false, nil
	}
	left, err := r.value(ctx, c.Left, env)
	if c.Operator == "exists" {
		if errors.Is(err, ErrMissing) {
			return false, nil
		}
		return err == nil, err
	}
	if errors.Is(err, ErrMissing) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	right, err := r.value(ctx, c.Right, env)
	if errors.Is(err, ErrMissing) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	switch c.Operator {
	case "eq":
		return equal(left, right), nil
	case "ne":
		return !equal(left, right), nil
	case "in", "not_in":
		items, ok := right.([]any)
		if !ok {
			return false, fmt.Errorf("in requires right array")
		}
		found := false
		for _, v := range items {
			if equal(left, v) {
				found = true
				break
			}
		}
		if c.Operator == "not_in" {
			found = !found
		}
		return found, nil
	case "contains", "not_contains", "regex", "not_regex":
		l, ok := left.(string)
		if !ok {
			return false, fmt.Errorf("text condition requires string")
		}
		rr, ok := right.(string)
		if !ok {
			return false, fmt.Errorf("text condition requires string")
		}
		if len(rr) > 2048 {
			return false, fmt.Errorf("pattern limit exceeded")
		}
		match := strings.Contains(l, rr)
		if c.Operator == "regex" || c.Operator == "not_regex" {
			pattern, err := regexp.Compile(rr)
			if err != nil {
				return false, err
			}
			match = pattern.MatchString(l)
		}
		if c.Operator == "not_contains" || c.Operator == "not_regex" {
			match = !match
		}
		return match, nil
	default:
		return false, fmt.Errorf("unknown condition")
	}
}

func equal(a, b any) bool {
	number := func(v any) (*big.Rat, bool) {
		switch v.(type) {
		case json.Number, float64, int, int64:
			r, ok := new(big.Rat).SetString(fmt.Sprint(v))
			return r, ok
		default:
			return nil, false
		}
	}
	aa, aok := number(a)
	bb, bok := number(b)
	if aok && bok {
		return aa.Cmp(bb) == 0
	}
	return reflect.DeepEqual(a, b)
}

func (r *run) predicate(ctx context.Context, p *Predicate, env map[string]any) (onemodel.Filter, error) {
	if p == nil {
		return onemodel.Filter{}, nil
	}
	f := onemodel.Filter{Field: p.Field, Type: p.Type, Operator: p.Operator}
	for i := range p.All {
		x, err := r.predicate(ctx, &p.All[i], env)
		if err != nil {
			return f, err
		}
		f.All = append(f.All, x)
	}
	for i := range p.Any {
		x, err := r.predicate(ctx, &p.Any[i], env)
		if err != nil {
			return f, err
		}
		f.Any = append(f.Any, x)
	}
	if p.Not != nil {
		x, err := r.predicate(ctx, p.Not, env)
		if err != nil {
			return f, err
		}
		f.Not = &x
	}
	if p.Value != nil {
		v, err := r.value(ctx, p.Value, env)
		if err != nil {
			return f, err
		}
		f.Value = v
	}
	return f, nil
}

func (r *run) externalCall() error {
	r.calls++
	if r.calls > 64 {
		return fmt.Errorf("external query budget exceeded")
	}
	return nil
}

func (r *run) lookup(ctx context.Context, rule Rule) (any, int, error) {
	if r.sources.Instances == nil {
		return nil, 0, fmt.Errorf("onemodel datasource unavailable")
	}
	where, err := r.predicate(ctx, rule.Lookup.Where, r.environment(nil))
	if err != nil {
		return nil, 0, err
	}
	q := onemodel.Query{ModelID: rule.Lookup.ModelID, Where: where, Limit: 1024}
	if rule.Lookup.Expect == "one" {
		q.Limit = 2
	}
	keyBytes, err := json.Marshal(q)
	if err != nil {
		return nil, 0, err
	}
	key := string(keyBytes)
	items, exists := r.cache[key]
	if !exists {
		if err := r.externalCall(); err != nil {
			return nil, 0, err
		}
		items, err = r.sources.Instances.Search(ctx, r.tenant, q)
		if err != nil {
			return nil, 0, err
		}
		r.cache[key] = items
	}
	lookup, err := lookupValue(items, rule.Lookup.Expect)
	if err != nil {
		return nil, len(items), err
	}
	roots := map[string][]onemodel.Instance{"root": items}
	relations := map[string]any{}
	for _, rel := range rule.Relations {
		env := r.environment(map[string]any{"lookup": lookup})
		filter, err := r.predicate(ctx, rel.Where, env)
		if err != nil {
			return nil, len(items), err
		}
		q := onemodel.Query{ModelID: rel.ModelID, Where: filter, Limit: 1024}
		if rel.Expect == "one" {
			q.Limit = 2
		}
		if err := r.externalCall(); err != nil {
			return nil, len(items), err
		}
		found, err := r.sources.Instances.Related(ctx, r.tenant, roots[rel.From], rel.Relation, rel.Direction, q)
		if err != nil {
			return nil, len(items), err
		}
		v, err := lookupValue(found, rel.Expect)
		if err != nil {
			return nil, len(items), err
		}
		relations[rel.ID] = v
		roots[rel.ID] = found
		if obj, ok := lookup.(map[string]any); ok {
			obj["relations"] = relations
		}
	}
	if rule.Topology {
		reader, ok := r.sources.Instances.(interface {
			FindHostTopology(context.Context, string, string) (onemodel.ResourceTopology, bool, error)
		})
		if !ok || len(items) != 1 || items[0].ModelCode != "cw-Host" {
			return nil, len(items), fmt.Errorf("topology requires a unique host")
		}
		if err := r.externalCall(); err != nil {
			return nil, len(items), err
		}
		top, found, err := reader.FindHostTopology(ctx, r.tenant, items[0].InstanceID)
		if err != nil {
			return nil, len(items), err
		}
		if found {
			lookup.(map[string]any)["topology"] = map[string]any{"bk_biz_id": top.BKBizID, "bk_biz_name": top.BKBizName, "bk_set_id": top.BKSetID, "bk_set_name": top.BKSetName, "bk_module_id": top.BKModuleID, "bk_module_name": top.BKModuleName}
		}
	}
	return lookup, len(items), nil
}

func lookupValue(items []onemodel.Instance, expect string) (any, error) {
	if len(items) == 0 {
		return nil, ErrMissing
	}
	if expect == "one" {
		if len(items) != 1 {
			return nil, fmt.Errorf("lookup matched multiple instances")
		}
		return jsonpath.Clone(items[0].Document()), nil
	}
	values := make([]any, len(items))
	for i, item := range items {
		values[i] = jsonpath.Clone(item.Document())
	}
	return values, nil
}
