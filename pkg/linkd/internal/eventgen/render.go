// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package eventgen

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"
)

// ErrExhausted 表示一次性遍历已完成，不属于发送失败。
var ErrExhausted = errors.New("dataset traversal completed")

// Record 是已冻结的投递；重试不能重新渲染 Body、Headers 或 Timestamp。
type Record struct {
	Stage            string `json:"stage"`
	instanceSequence uint64
	terminal         bool
	Key              []byte            `json:"key"`
	Body             json.RawMessage   `json:"body"`
	Headers          map[string]string `json:"headers"`
	Timestamp        time.Time         `json:"timestamp"`
	SourceID         string            `json:"event_source_id"`
	SourceEventID    string            `json:"event_id"`
	Fingerprint      string            `json:"fingerprint"`
	ScenarioID       string            `json:"scenario_id"`
	Cleanup          bool              `json:"cleanup,omitempty"`
}

// RenderContext 为模板提供明确的运行、告警和事件作用域，字段名即模板变量名。
type RenderContext struct {
	RunID         string
	TenantID      string
	ScenarioID    string
	Sequence      uint64
	EventSequence uint64
	Fingerprint   string
	EventID       string
	Stage         string
	ScheduledAt   time.Time
	OccurredAt    time.Time
	ProducedAt    time.Time
	Bindings      map[string]any
	Instance      any
	Parameters    map[string]any
}

type compiledTemplate struct {
	body, key *template.Template
	headers   map[string]*template.Template
	kind      string
}

type rowGroup struct {
	rows    []map[string]any
	weights []float64
}

type compiledScenario struct {
	groups                map[string]map[string]*rowGroup
	columns               map[string][]string
	config                Scenario
	instance, fingerprint *template.Template
	where                 map[string]map[string]*template.Template
	stages                map[string]int
}

// Program 是一次完整配置的编译结果；渲染与随机取值只由调度协程串行调用。
type Program struct {
	Config    Config
	templates map[string]compiledTemplate
	scenarios []compiledScenario
	random    *rand.Rand
	cursors   map[string]int
}

// Compile 在配置边界解析所有模板；不访问 Kafka、Cleaner 或环境接口。
func Compile(c Config) (*Program, error) {
	c = c.defaults()
	if e := c.Validate(); e != nil {
		return nil, e
	}
	//nolint:gosec // G404: 可复现测试数据使用显式种子，不用于凭据或安全身份。
	p := &Program{Config: c, templates: map[string]compiledTemplate{}, random: rand.New(rand.NewPCG(c.Seed, c.Seed^0x9e3779b97f4a7c15)), cursors: map[string]int{}}
	for name, t := range c.Templates {
		ct := compiledTemplate{headers: map[string]*template.Template{}, kind: t.Kind}
		var e error
		ct.body, e = p.parse(name, t.Body)
		if e != nil {
			return nil, e
		}
		ct.key, e = p.parse(name+".key", t.Key)
		if e != nil {
			return nil, e
		}
		for k, v := range t.Headers {
			if k == "bk_tenant_id" || k == "message_id" || k == "order_key" {
				return nil, fmt.Errorf("header %s is managed by eventgen", k)
			}
			ct.headers[k], e = p.parse(name+".header", v)
			if e != nil {
				return nil, e
			}
		}
		p.templates[name] = ct
	}
	for _, s := range c.Scenarios {
		cs := compiledScenario{config: s, groups: map[string]map[string]*rowGroup{}, columns: map[string][]string{}, where: map[string]map[string]*template.Template{}, stages: map[string]int{}}
		var e error
		cs.instance, e = p.parse(s.ID+".instance", s.Instance)
		if e != nil {
			return nil, e
		}
		cs.fingerprint, e = p.parse(s.ID+".fingerprint", s.Fingerprint)
		if e != nil {
			return nil, e
		}

		for _, b := range s.Bindings {
			cs.groups[b.Name] = map[string]*rowGroup{}
			cs.columns[b.Name] = sortedKeys(b.Where)
			// 按实际筛选列建立一次性索引，避免每条事件扫描环境数据表。
			for _, row := range c.Datasets[b.Dataset].Rows {
				values := make([]any, 0, len(cs.columns[b.Name]))
				for _, column := range cs.columns[b.Name] {
					values = append(values, row[column])
				}
				encoded, err := json.Marshal(values)
				if err != nil {
					return nil, fmt.Errorf("dataset contains non-JSON values")
				}
				key := string(encoded)
				group := cs.groups[b.Name][key]
				if group == nil {
					group = &rowGroup{}
					cs.groups[b.Name][key] = group
				}
				group.rows = append(group.rows, row)
				if b.Mode == "weighted" {
					weight, _ := numeric(row[c.Datasets[b.Dataset].WeightColumn])
					if len(group.weights) > 0 {
						weight += group.weights[len(group.weights)-1]
					}
					group.weights = append(group.weights, weight)
				}
			}
			cs.where[b.Name] = map[string]*template.Template{}
			for k, v := range b.Where {
				cs.where[b.Name][k], e = p.parse(s.ID+".where", v)
				if e != nil {
					return nil, e
				}
			}
		}
		for i, st := range s.Stages {
			cs.stages[st.Name] = i
		}
		p.scenarios = append(p.scenarios, cs)
	}
	return p, nil
}

func (p *Program) parse(name, body string) (*template.Template, error) {
	funcs := template.FuncMap{
		"json": func(v any) (string, error) { b, e := json.Marshal(v); return string(b), e },
		"randInt": func(minimum, maximum int64) (int64, error) {
			if maximum <= minimum {
				return 0, fmt.Errorf("randInt requires max > min")
			}
			return minimum + p.random.Int64N(maximum-minimum), nil
		},
		"randFloat": func(a, b float64) (float64, error) {
			if b < a {
				return 0, fmt.Errorf("randFloat requires max >= min")
			}
			return a + p.random.Float64()*(b-a), nil
		},
		"chance": func(percent float64) bool { return p.random.Float64()*100 < percent },
		"pick": func(v ...any) (any, error) {
			if len(v) == 0 {
				return nil, fmt.Errorf("pick requires values")
			}
			return v[p.random.IntN(len(v))], nil
		},
		"weighted": func(v ...any) (any, error) {
			if len(v) == 0 || len(v)%2 != 0 {
				return nil, fmt.Errorf("weighted requires value/weight pairs")
			}
			total := 0.0
			for i := 1; i < len(v); i += 2 {
				w, ok := numeric(v[i])
				if !ok || w <= 0 {
					return nil, fmt.Errorf("positive weight required")
				}
				total += w
			}
			draw := p.random.Float64() * total
			for i := 1; i < len(v); i += 2 {
				w, _ := numeric(v[i])
				draw -= w
				if draw < 0 {
					return v[i-1], nil
				}
			}
			return v[len(v)-2], nil
		},
		"addTime":    func(t time.Time, d string) (time.Time, error) { v, e := time.ParseDuration(d); return t.Add(v), e },
		"formatTime": func(t time.Time, layout string) string { return t.Format(layout) },
	}
	return template.New(name).Option("missingkey=error").Funcs(funcs).Parse(body)
}

type limitedBuffer struct {
	bytes.Buffer
	max int
}

func (b *limitedBuffer) Write(v []byte) (int, error) {
	if len(v) > b.max-b.Len() {
		return 0, fmt.Errorf("template exceeds message budget")
	}
	return b.Buffer.Write(v)
}

func (p *Program) text(t *template.Template, c RenderContext) ([]byte, error) {
	b := limitedBuffer{max: p.Config.Limits.MaxMessageBytes}
	if e := t.Execute(&b, c); e != nil {
		return nil, fmt.Errorf("template %s: render failed", t.Name())
	}
	return b.Bytes(), nil
}

func (p *Program) span(s Span) time.Duration {
	if s.Fixed != "" {
		return s.Fixed.Value()
	}
	lo, hi := s.Min.Value(), s.Max.Value()
	if hi <= lo {
		return lo
	}
	return lo + time.Duration(p.random.Int64N(int64(hi-lo)))
}

func (p *Program) instance(si int, run string, seq uint64, at time.Time) (RenderContext, error) {
	s := p.scenarios[si]
	c := RenderContext{RunID: run, TenantID: p.Config.TenantID, ScenarioID: s.config.ID, Sequence: seq, ScheduledAt: at, OccurredAt: at, ProducedAt: at, Bindings: map[string]any{}, Parameters: map[string]any{}}
	c.Fingerprint = fmt.Sprintf("%s:%s:%d", run, s.config.ID, seq)
	for _, b := range s.config.Bindings {
		criteria := make([]any, 0, len(s.columns[b.Name]))
		for _, column := range s.columns[b.Name] {
			raw, err := p.text(s.where[b.Name][column], c)
			if err != nil {
				return c, err
			}
			var value any
			if err = decodeJSON(raw, &value); err != nil {
				return c, fmt.Errorf("binding %s: where must render JSON", b.Name)
			}
			criteria = append(criteria, value)
		}
		encoded, err := json.Marshal(criteria)
		if err != nil {
			return c, err
		}
		group := s.groups[b.Name][string(encoded)]
		if group == nil || len(group.rows) == 0 {
			return c, fmt.Errorf("binding %s: no matching rows", b.Name)
		}
		rows := group.rows
		index := 0
		switch b.Mode {
		case "iterate":
			key := s.config.ID + ":" + b.Name + ":" + string(encoded)
			index = p.cursors[key]
			if !b.Loop && index >= len(rows) {
				return c, ErrExhausted
			}
			index %= len(rows)
			p.cursors[key]++
		case "weighted":
			draw := p.random.Float64() * group.weights[len(group.weights)-1]
			index = sort.Search(len(group.weights), func(i int) bool { return group.weights[i] > draw })
		default:
			index = p.random.IntN(len(rows))
		}
		c.Bindings[b.Name] = rows[index]
	}
	if s.config.Instance != "" {
		raw, e := p.text(s.instance, c)
		if e != nil {
			return c, e
		}
		if e = decodeJSON(raw, &c.Instance); e != nil {
			return c, fmt.Errorf("instance must render JSON")
		}
	}
	if s.config.Fingerprint != "" {
		raw, e := p.text(s.fingerprint, c)
		if e != nil {
			return c, e
		}
		c.Fingerprint = string(raw)
	}
	return c, nil
}

func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (p *Program) render(si, stage int, c RenderContext, eventSequence uint64, at time.Time, cleanup bool) (Record, error) {
	s := p.scenarios[si].config
	st := s.Stages[stage]
	c.Stage = st.Name
	c.Parameters = st.Parameters
	c.EventSequence = eventSequence
	c.EventID = fmt.Sprintf("%s:%s:%d:%d", c.RunID, s.ID, c.Sequence, eventSequence)
	c.ScheduledAt = at
	c.OccurredAt = at
	c.ProducedAt = at
	if s.OccurredOffset != "" {
		offset, _ := time.ParseDuration(s.OccurredOffset)
		c.OccurredAt = at.Add(offset)
	}
	name := st.Template
	if cleanup && s.CleanupTemplate != "" {
		name = s.CleanupTemplate
	}
	t := p.templates[name]
	var body []byte
	var e error
	if cleanup && s.CleanupTemplate == "" {
		body, e = json.Marshal(map[string]any{"bk_tenant_id": c.TenantID, "fingerprint": c.Fingerprint, "event_id": c.EventID, "occurred_at": at, "produced_at": at, "evaluations": []any{map[string]any{"severity": "__ALL__", "action": "closed", "action_reason": "eventgen_cleanup"}}})
	} else {
		body, e = p.text(t.body, c)
		if e == nil {
			var object map[string]any
			e = decodeJSON(body, &object)
			if e == nil && object == nil {
				e = fmt.Errorf("message must be a JSON object")
			}
			if e == nil && t.kind == "standard" {
				object["bk_tenant_id"] = c.TenantID
				object["fingerprint"] = c.Fingerprint
				object["event_id"] = c.EventID
				if _, ok := object["occurred_at"]; !ok {
					object["occurred_at"] = c.OccurredAt
				}
				if _, ok := object["produced_at"]; !ok {
					object["produced_at"] = at
				}
				body, e = json.Marshal(object)
			}
		}
	}
	if e != nil {
		return Record{}, fmt.Errorf("scenario %s stage %s: JSON rendering failed", s.ID, st.Name)
	}
	if len(body) > p.Config.Limits.MaxMessageBytes {
		return Record{}, fmt.Errorf("message exceeds byte budget")
	}
	key := []byte(c.TenantID + ":" + c.Fingerprint)
	if t.key != nil && t.key.Root.String() != "" {
		key, e = p.text(t.key, c)
		if e != nil {
			return Record{}, e
		}
	}
	headers := map[string]string{"bk_tenant_id": c.TenantID, "message_id": c.EventID, "order_key": c.TenantID + ":" + s.SourceID + ":" + c.Fingerprint}
	for _, k := range sortedKeys(t.headers) {
		v, err := p.text(t.headers[k], c)
		if err != nil {
			return Record{}, err
		}
		headers[k] = string(v)
		if recordBytes(Record{Body: body, Key: key, Headers: headers}) > int64(p.Config.Limits.MaxMessageBytes+512) {
			return Record{}, fmt.Errorf("message headers exceed byte budget")
		}
	}
	return Record{Key: key, Body: body, Headers: headers, Timestamp: at, SourceID: s.SourceID, SourceEventID: c.EventID, Fingerprint: c.Fingerprint, ScenarioID: s.ID, Cleanup: cleanup, Stage: st.Name}, nil
}

// ImportCSV 将有类型声明的 CSV 转成数据表；未声明列按字符串保存。
func ImportCSV(input string, columns map[string]string) (Dataset, error) {
	r := csv.NewReader(strings.NewReader(input))
	header, e := r.Read()
	if e != nil {
		return Dataset{}, e
	}
	d := Dataset{Rows: []map[string]any{}}
	for {
		values, e := r.Read()
		if errors.Is(e, io.EOF) {
			return d, nil
		}
		if e != nil {
			return d, e
		}
		row := map[string]any{}
		for i, v := range values {
			var out any = v
			switch columns[header[i]] {
			case "number":
				out, e = strconv.ParseFloat(v, 64)
			case "boolean":
				out, e = strconv.ParseBool(v)
			case "json":
				e = decodeJSON([]byte(v), &out)
			case "", "string":
			default:
				return d, fmt.Errorf("unknown column type")
			}
			if e != nil {
				return d, fmt.Errorf("invalid CSV value in column %s", header[i])
			}
			row[header[i]] = out
		}
		d.Rows = append(d.Rows, row)
	}
}

// RateOnly 表示更新只改变新增速率或发送限速，可不打断现有告警。
func RateOnly(a, b Config) bool {
	a = a.defaults()
	b = b.defaults()
	a.Limits.SendPerSecond = b.Limits.SendPerSecond
	a.Name = b.Name
	copyScenarios := append([]Scenario(nil), a.Scenarios...)
	a.Scenarios = copyScenarios
	if len(a.Scenarios) != len(b.Scenarios) {
		return false
	}
	for i := range a.Scenarios {
		a.Scenarios[i].Arrival.PerSecond = b.Scenarios[i].Arrival.PerSecond
		a.Scenarios[i].Arrival.Rates = b.Scenarios[i].Arrival.Rates
	}
	return reflect.DeepEqual(a, b)
}

// decodeJSON 保留配置表和模板中的整数精度，避免把主机/模型 ID 转成 float64 后舍入。
func decodeJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}
