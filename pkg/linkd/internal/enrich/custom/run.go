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
	"errors"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"linkd/internal/domain"
	"linkd/internal/jsonpath"
	"linkd/internal/onemodel"
	"linkd/internal/textregexp"
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
	// Error 只用于当前调用日志，不进入结果 JSON。
	Error                error               `json:"-"`
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
	tenant         string
	original       map[string]any
	state          *domain.EnrichEvaluation
	start          int
	patchBytes     int
	current        map[string]any
	sources        Sources
	calls          int
	fieldSucceeded bool
	cache          map[string][]onemodel.Instance
}

// Execute 是独立规则/预览边界，输入隔离后在返回时编码补丁。
func (p *Program) Execute(ctx context.Context, original, current map[string]any, tenant string, sources Sources) (Result, error) {
	state, err := domain.NewEnrichDocumentEvaluation(original, current)
	if err != nil {
		return Result{}, err
	}
	runtime, err := p.ExecuteEvaluation(ctx, state, tenant, sources)
	if err != nil {
		return Result{}, err
	}
	result := Result{Status: runtime.Status, Trace: runtime.Trace, Patches: make([]domain.EnrichPatch, 0, len(runtime.Patches))}
	for _, assignment := range runtime.Patches {
		patch, err := assignment.Patch()
		if err != nil {
			return Result{}, err
		}
		result.Patches = append(result.Patches, patch)
	}
	return result, nil
}

// RuntimeResult 保存运行中已应用的 Go 补丁，不重新编码或再次执行。
type RuntimeResult struct {
	Status  domain.EnrichStatus
	Patches []domain.EnrichAssignment
	Trace   []Trace
}

// ExecuteEvaluation 在共享工作对象上顺序执行；错误/取消恢复到本次 Processor 入口。
func (p *Program) ExecuteEvaluation(ctx context.Context, state *domain.EnrichEvaluation, tenant string, sources Sources) (result RuntimeResult, err error) {
	if ctx == nil || tenant == "" || state == nil {
		return RuntimeResult{}, fmt.Errorf("context, tenant and evaluation are required")
	}
	point := state.Savepoint()
	completed := false
	defer func() {
		if !completed {
			state.Rollback(point)
		}
	}()
	r := &run{tenant: tenant, original: state.OriginalDocument(), current: state.Document(), state: state, start: point, patchBytes: 2, sources: sources, cache: map[string][]onemodel.Instance{}}
	result = RuntimeResult{Patches: []domain.EnrichAssignment{}, Trace: []Trace{}}
	ruleStates := []domain.EnrichStatus{}
	for _, rule := range p.config.Rules {
		if err := ctx.Err(); err != nil {
			return RuntimeResult{}, err
		}
		start := time.Now()
		before := len(result.Patches)
		traceStart := len(result.Trace)
		failed := false
		skipped := false
		code := ""
		matches, err := r.matchRule(ctx, rule, &result)
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
				result.Trace = append(result.Trace, Trace{RuleID: rule.ID, OperationID: op.ID, Status: state, Code: errorCode(err), Error: err, DurationMilliseconds: time.Since(opStart).Milliseconds()})
				// 单步输出原子提交；后续步骤仍可读取之前成功的字段。
			}
		} else {
			lookup, n, err := r.lookup(ctx, rule, &result)
			if err == nil {
				if rule.Identity != nil {
					err = r.bindInstance(ctx, rule, lookup.(map[string]any), &result)
					if err != nil && ctx.Err() == nil {
						// 旧 builtin_cmdb_enrich 失败不阻止已命中实例的显式属性丰富。
						failed = true
						code = errorCode(err)
						result.Trace = append(result.Trace, Trace{RuleID: rule.ID, OperationID: "topology", Status: stateFor(err), Code: errorCode(err), Error: err})
						err = nil
					}
				}
			}
			if err == nil {
				env := r.environment(map[string]any{"lookup": lookup})
				if rule.Identity != nil {
					// 旧 KAC 逐字段赋值；某个展示字段失败时，前面完成的字段仍有效。
					for index, assignment := range rule.Assignments {
						operationID := fmt.Sprintf("field_%d", index+1)
						patches, e := r.assign(ctx, []Assignment{assignment}, env)
						if e == nil {
							e = r.commit(rule.ID, operationID, patches, &result)
						}
						result.Trace = append(result.Trace, Trace{RuleID: rule.ID, OperationID: operationID, Status: stateFor(e), Code: errorCode(e), Error: e})
						if e != nil {
							if err == nil {
								err = e
							}
							continue
						}
						r.fieldSucceeded = true
					}
				} else {
					patches, assignErr := r.assign(ctx, rule.Assignments, env)
					err = assignErr
					if err == nil {
						err = r.commit(rule.ID, "", patches, &result)
					}
				}
			}
			if err != nil {
				skipped = errors.Is(err, ErrMissing)
				failed = !skipped
				code = errorCode(err)
			}
			result.Trace = append(result.Trace, Trace{RuleID: rule.ID, OperationID: "lookup", Status: stateFor(err), Code: errorCode(err), Error: err, Matches: n})
		}
		if err := ctx.Err(); err != nil {
			return RuntimeResult{}, err
		}
		for _, trace := range result.Trace[traceStart:] {
			failed = failed || trace.Status == domain.EnrichStatusFailed
			skipped = skipped || trace.Status == domain.EnrichStatusSkipped
		}
		state := domain.EnrichStatusSucceeded
		if skipped {
			state = domain.EnrichStatusSkipped
		}
		if failed {
			state = domain.EnrichStatusFailed
		}
		if (failed || skipped) && len(result.Patches) > before {
			state = domain.EnrichStatusPartial
		}
		ruleStates = append(ruleStates, state)
		result.Trace = append(result.Trace, Trace{RuleID: rule.ID, Status: state, Code: code, Error: err, DurationMilliseconds: time.Since(start).Milliseconds()})
	}
	if p.config.RollbackUnmatched && !r.fieldSucceeded {
		result.Patches = []domain.EnrichAssignment{}
		// 失败和查空仍在 trace 中可见；中间身份不能进入后续 fields 与策略。
		for index := range ruleStates {
			if ruleStates[index] != domain.EnrichStatusPartial {
				continue
			}
			ruleStates[index] = domain.EnrichStatusSkipped
			for _, trace := range result.Trace {
				if trace.RuleID == p.config.Rules[index].ID && trace.Status == domain.EnrichStatusFailed {
					ruleStates[index] = domain.EnrichStatusFailed
				}
			}
		}
		err := r.fallbackTopology(ctx, p.config.Rules, &result)
		if ctx.Err() != nil {
			return RuntimeResult{}, ctx.Err()
		}
		if err != nil {
			ruleStates = append(ruleStates, domain.EnrichStatusFailed)
		} else if len(result.Patches) > 0 {
			ruleStates = append(ruleStates, domain.EnrichStatusSucceeded)
		}
		result.Trace = append(result.Trace, Trace{RuleID: "cmdb", OperationID: "fallback_topology", Status: stateFor(err), Code: errorCode(err), Error: err})
		result.Trace = append(result.Trace, Trace{RuleID: "cmdb", OperationID: "rollback", Status: domain.EnrichStatusSkipped, Code: "no_cmdb_fields_enriched"})
	}
	result.Status = Aggregate(ruleStates)
	completed = true
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
	if errors.Is(err, textregexp.ErrTimeout) {
		return "regex_timeout"
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "cancelled"
	}
	return "invalid_field_or_dependency"
}

func (r *run) environment(extra map[string]any) map[string]any {
	env := map[string]any{"original": r.original, "event": r.current, "evaluation": r.current["evaluation"]}
	for k, v := range extra {
		env[k] = v
	}
	return env
}

func (r *run) commit(rule, operation string, patches []domain.EnrichAssignment, result *RuntimeResult) error {
	if len(result.Patches)+len(patches) > 4096 {
		return fmt.Errorf("patch budget exceeded")
	}
	for i := range patches {
		patches[i].RuleID = rule
		patches[i].OperationID = operation
	}
	bytes := r.patchBytes
	for _, patch := range patches {
		if len(result.Patches) > 0 || bytes > 2 {
			bytes++
		}
		bytes += patch.EncodedSize()
	}
	if bytes > 1<<20 {
		return fmt.Errorf("patch payload budget exceeded")
	}
	if err := r.state.Apply(patches); err != nil {
		return err
	}
	r.patchBytes = bytes
	result.Patches = append(result.Patches, patches...)
	return nil
}

func (r *run) assign(ctx context.Context, assignments []Assignment, env map[string]any) ([]domain.EnrichAssignment, error) {
	out := make([]domain.EnrichAssignment, 0, len(assignments))
	for _, assignment := range assignments {
		value, err := r.value(ctx, &assignment.Value, env)
		if err != nil {
			return nil, err
		}
		patch, err := domain.NewEnrichAssignment(assignment.target, value)
		if err != nil {
			return nil, err
		}
		out = append(out, patch)
	}
	return out, nil
}

func (r *run) operation(ctx context.Context, op Operation) ([]domain.EnrichAssignment, error) {
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
		pairs := make([]string, 0, len(op.Replacements)*2)
		for _, rep := range op.Replacements {
			pairs = append(pairs, rep.From, rep.To)
		}
		// 一次替换只扫描原输入；替换生成的文本留给后续显式步骤处理。
		text = strings.NewReplacer(pairs...).Replace(text)
		if len(text) > 1<<20 {
			return nil, fmt.Errorf("replace result limit exceeded")
		}
		patch, err := domain.NewEnrichAssignment(op.target, text)
		if err != nil {
			return nil, err
		}
		return []domain.EnrichAssignment{patch}, nil
	case "extract":
		v, err := r.value(ctx, op.Source, env)
		if err != nil {
			return nil, err
		}
		text, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("extract source is not text")
		}
		limit := 1025
		if op.MatchMode == "first" || (op.MatchMode == "" && op.MatchIndex > 0) {
			limit = op.MatchIndex + 1
		}
		matches, err := op.regex.FindAll(text, limit)
		if err != nil {
			return nil, err
		}
		if len(matches) > 1024 {
			return nil, fmt.Errorf("extract match limit exceeded")
		}
		if len(matches) == 0 {
			return nil, ErrMissing
		}
		items := make([]any, 0, len(matches))
		for _, m := range matches {
			items = append(items, map[string]any{"text": m.Text, "groups": m.Groups, "named_groups": m.NamedGroups})
		}
		if op.MatchMode == "all" {
			return r.assignAllMatches(ctx, op, env, items)
		}
		if op.MatchMode == "first" || op.MatchIndex > 0 {
			if op.MatchIndex >= len(items) {
				return nil, ErrMissing
			}
			items = items[op.MatchIndex : op.MatchIndex+1]
		}
		env["extraction"] = map[string]any{"matches": items}
		return r.assign(ctx, op.Assignments, env)
	default:
		return nil, fmt.Errorf("unknown operation")
	}
}

// assignAllMatches 逐字段累计匹配结果，复用既有 1 MiB 补丁预算后一次提交。
// 展示文本只保存前 2000 字符；截断之后仍求值后续匹配，不能掩盖错误或取消。
func (r *run) assignAllMatches(ctx context.Context, op Operation, env map[string]any, items []any) ([]domain.EnrichAssignment, error) {
	patches := make([]domain.EnrichAssignment, 0, len(op.Assignments))
	bytes := r.patchBytes
	for _, assignment := range op.Assignments {
		var output strings.Builder
		root := assignment.target.Parts()[0]
		display := root == "title" || root == "subject_name"
		chars := 0
		appendText := func(text string) error {
			if display {
				if chars == domain.DisplayTextMaxChars {
					return nil
				}
				for offset := range text {
					if chars == domain.DisplayTextMaxChars {
						text = text[:offset]
						break
					}
					chars++
				}
			}
			if bytes+output.Len()+len(text) > 1<<20 {
				return fmt.Errorf("patch payload budget exceeded")
			}
			output.WriteString(text)
			return nil
		}
		for index, item := range items {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			env["extraction"] = map[string]any{"matches": []any{item}}
			if index > 0 {
				if err := appendText(op.Separator); err != nil {
					return nil, err
				}
			}
			value := &assignment.Value
			if value.Template != nil && len(value.Transforms) == 0 {
				// 不先生成包含完整正文的临时模板字符串，再丢弃截断部分。
				if err := r.renderTemplate(ctx, value, env, appendText); err != nil {
					return nil, err
				}
				continue
			}
			v, err := r.value(ctx, value, env)
			if err != nil {
				return nil, err
			}
			text, err := scalarText(v)
			if err != nil {
				return nil, err
			}
			if err := appendText(text); err != nil {
				return nil, err
			}
		}
		patch, err := domain.NewEnrichAssignment(assignment.target, output.String())
		if err != nil {
			return nil, err
		}
		bytes += patch.EncodedSize() + 1
		if bytes > 1<<20 {
			return nil, fmt.Errorf("patch payload budget exceeded")
		}
		patches = append(patches, patch)
	}
	return patches, nil
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
		out = v.literal
	case v.query != nil:
		var nodes []any
		if err = r.state.ValidateEnvironment(env); err == nil {
			nodes, err = v.query.SelectPrepared(ctx, env)
		}
		if err == nil {
			if v.Select == "all" {
				out = []any(nodes)
				if len(nodes) == 0 && v.query.HasPrefix("lookup", "relations") {
					err = ErrMissing
				}
			} else if len(nodes) == 0 {
				err = ErrMissing
			} else if len(nodes) > 1 {
				err = fmt.Errorf("JSONPath expected one node")
			} else {
				out = nodes[0]
				if out == nil && v.query.HasPrefix("extraction") {
					err = ErrMissing
				}
			}
		}
	case v.Template != nil:
		var output strings.Builder
		if err := r.renderTemplate(ctx, v, env, func(text string) error {
			if output.Len()+len(text) > 1<<20 {
				return fmt.Errorf("template result limit exceeded")
			}
			output.WriteString(text)
			return nil
		}); err != nil {
			return nil, err
		}
		out = output.String()
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

// renderTemplate 先求全部变量，再逐段输出；输出端截断不会跳过变量失败。
func (r *run) renderTemplate(ctx context.Context, v *Value, env map[string]any, write func(string) error) error {
	variables := make(map[string]string, len(v.Variables))
	names := make([]string, 0, len(v.Variables))
	for name := range v.Variables {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		variable := v.Variables[name]
		value, err := r.value(ctx, &variable, env)
		if err != nil {
			return err
		}
		text, err := scalarText(value)
		if err != nil {
			return err
		}
		variables[name] = text
	}
	text, offset := *v.Template, 0
	for _, match := range variablePattern.FindAllStringIndex(text, -1) {
		if err := write(text[offset:match[0]]); err != nil {
			return err
		}
		if err := write(variables[text[match[0]+2:match[1]-1]]); err != nil {
			return err
		}
		offset = match[1]
	}
	return write(text[offset:])
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

// displayText 在展示转换之后拼接数组，不复刻 Python 容器的 repr 文本。
func displayText(value any) (string, error) {
	if values, ok := value.([]any); ok {
		parts := make([]string, len(values))
		for index, item := range values {
			text, err := displayText(item)
			if err != nil {
				return "", err
			}
			parts[index] = text
		}
		return strings.Join(parts, ","), nil
	}
	return scalarText(value)
}

func (r *run) transform(ctx context.Context, t Transform, v any) (any, error) {
	switch t.Type {
	case "display_text":
		if v == nil {
			return nil, nil
		}
		return displayText(v)
	case "kingeye_string":
		return kingeyeText(v), nil
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
		matches, err := t.regex.FindAll(s, 1)
		if err != nil {
			return nil, err
		}
		if len(matches) == 0 {
			return nil, ErrMissing
		}
		if t.Group == 0 {
			return matches[0].Text, nil
		}
		value := matches[0].Groups[t.Group-1]
		if value == nil {
			return nil, ErrMissing
		}
		return value, nil
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
			pattern := c.regex
			if pattern == nil {
				var err error
				pattern, err = textregexp.Compile(rr)
				if err != nil {
					return false, err
				}
			}
			var err error
			match, err = pattern.MatchString(l)
			if err != nil {
				return false, err
			}
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

func (r *run) lookup(ctx context.Context, rule Rule, result *RuntimeResult) (any, int, error) {
	var instance any
	if rule.Identity != nil {
		for _, field := range []string{"model_inst_id", "bk_inst_id", "cw_object_model_inst_id"} {
			if value := rule.Identity.read(r.current, field); identityProvided(value) {
				instance = value
				break
			}
		}
		if rule.Lookup.Where == nil && instance == nil {
			return nil, 0, ErrMissing
		}
	}
	if r.sources.Instances == nil {
		return nil, 0, fmt.Errorf("onemodel datasource unavailable")
	}
	var where onemodel.Filter
	var err error
	if instance != nil {
		text, e := scalarText(instance)
		if e != nil {
			return nil, 0, e
		}
		where = onemodel.Filter{Field: "model_inst_id", Type: onemodel.InstanceAttributeKeyword, Operator: "eq", Value: text}
	} else {
		where, err = r.predicate(ctx, rule.Lookup.Where, r.environment(nil))
	}
	if err != nil {
		return nil, 0, err
	}
	q := onemodel.Query{ModelID: rule.Lookup.ModelID, Where: where, Limit: 1024}
	q.First = rule.Lookup.Expect == "first"
	if q.First {
		q.Limit = 1
	}
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
	if obj, ok := lookup.(map[string]any); ok {
		obj["relations"] = relations
	}
	for _, rel := range rule.Relations {
		// 1. 每个节点只依赖明确的前置结果，失败分支不调用后续查询。
		var found []onemodel.Instance
		var value any
		relationErr := ErrMissing
		if len(roots[rel.From]) > 0 {
			env := r.environment(map[string]any{"lookup": lookup})
			filter, e := r.predicate(ctx, rel.Where, env)
			relationErr = e
			if relationErr == nil {
				q := onemodel.Query{ModelID: rel.ModelID, Where: filter, Limit: 1024, First: rel.Expect == "first"}
				if q.First {
					q.Limit = 1
				} else if rel.Expect == "one" {
					q.Limit = 2
				}
				relationErr = r.externalCall()
				if relationErr == nil {
					found, relationErr = r.sources.Instances.Related(ctx, r.tenant, roots[rel.From], rel.Relation, rel.Direction, q)
				}
				if relationErr == nil {
					value, relationErr = lookupValue(found, rel.Expect)
				}
			}
		}
		// 2. 只暴露成功节点的结果；下一条独立链仍从主实例开始。
		if relationErr == nil {
			relations[rel.ID], roots[rel.ID] = value, found
		}
		result.Trace = append(result.Trace, Trace{RuleID: rule.ID, OperationID: rel.ID, Status: stateFor(relationErr), Code: errorCode(relationErr), Error: relationErr, Matches: len(found)})
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
	if expect == "one" || expect == "first" {
		if expect == "one" && len(items) != 1 {
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
