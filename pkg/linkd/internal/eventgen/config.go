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
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"time"
)

// Duration 是配置使用的 Go 时长字符串，例如 10s；JSON 和 YAML 均保留其可读形式。
type Duration string

func (d Duration) Value() time.Duration { v, _ := time.ParseDuration(string(d)); return v }

func (d Duration) validate() error {
	if d == "" {
		return nil
	}
	v, e := time.ParseDuration(string(d))
	if e != nil || v < 0 {
		return fmt.Errorf("invalid duration %q", d)
	}
	return nil
}

// Span 支持固定时长，或在 min/max 之间均匀抽取时长。
type Span struct {
	Fixed Duration `json:"fixed,omitempty" yaml:"fixed,omitempty"`
	Min   Duration `json:"min,omitempty" yaml:"min,omitempty"`
	Max   Duration `json:"max,omitempty" yaml:"max,omitempty"`
}

func (s Span) validate() error {
	for _, d := range []Duration{s.Fixed, s.Min, s.Max} {
		if e := d.validate(); e != nil {
			return e
		}
	}
	if s.Fixed != "" && (s.Min != "" || s.Max != "") {
		return fmt.Errorf("fixed and min/max are mutually exclusive")
	}
	if s.Max.Value() < s.Min.Value() {
		return fmt.Errorf("max must be >= min")
	}
	return nil
}

func (s Span) positive() bool { return s.Fixed.Value() > 0 || s.Min.Value() > 0 }

// Dataset 保留原始 JSON 类型；WeightColumn 指定加权随机使用的数值列。
type Dataset struct {
	Rows         []map[string]any `json:"rows" yaml:"rows"`
	WeightColumn string           `json:"weight_column,omitempty" yaml:"weight_column,omitempty"`
}

// Binding 在一条告警创建时选择完整数据行，Where 的值是可引用前序绑定的 JSON 模板。
type Binding struct {
	Name    string            `json:"name" yaml:"name"`
	Dataset string            `json:"dataset" yaml:"dataset"`
	Mode    string            `json:"mode" yaml:"mode"`
	Loop    bool              `json:"loop" yaml:"loop"`
	Where   map[string]string `json:"where,omitempty" yaml:"where,omitempty"`
}

// Template 描述单条 Kafka 消息；Body 是 JSON 文本模板，Key/Headers 是文本模板。
type Template struct {
	Kind    string            `json:"kind" yaml:"kind"`
	Body    string            `json:"body" yaml:"body"`
	Key     string            `json:"key,omitempty" yaml:"key,omitempty"`
	Headers map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`
}

// RateStep 从场景启动后的 At 开始改变新增告警速率。
type RateStep struct {
	At        Duration `json:"at" yaml:"at"`
	PerSecond float64  `json:"per_second" yaml:"per_second"`
}

// Arrival 控制新增告警，PerSecond 与 Every/Batch 二选一，Count=0 表示不限数量。
type Arrival struct {
	PerSecond float64    `json:"per_second,omitempty" yaml:"per_second,omitempty"`
	Every     Span       `json:"every,omitempty" yaml:"every,omitempty"`
	Batch     int        `json:"batch,omitempty" yaml:"batch,omitempty"`
	Delay     Duration   `json:"delay,omitempty" yaml:"delay,omitempty"`
	Count     uint64     `json:"count,omitempty" yaml:"count,omitempty"`
	Rates     []RateStep `json:"rates,omitempty" yaml:"rates,omitempty"`
}

// Branch 在阶段结束时按权重选取；Stage 为空表示结束该条告警的编排。
type Branch struct {
	Stage  string  `json:"stage" yaml:"stage"`
	Weight float64 `json:"weight" yaml:"weight"`
}

// Stage 在进入时发送，随后每 Every 发送；Terminal 标记本阶段消息已结束来源告警。
type Stage struct {
	Name       string         `json:"name" yaml:"name"`
	Template   string         `json:"template" yaml:"template"`
	Duration   Span           `json:"duration,omitempty" yaml:"duration,omitempty"`
	Every      Span           `json:"every,omitempty" yaml:"every,omitempty"`
	Parameters map[string]any `json:"parameters,omitempty" yaml:"parameters,omitempty"`
	Next       []Branch       `json:"next,omitempty" yaml:"next,omitempty"`
	Terminal   bool           `json:"terminal,omitempty" yaml:"terminal,omitempty"`
}

// Scenario 关联真实来源和一条完整的生命周期；Instance 是创建时求值的 JSON 模板。
type Scenario struct {
	ID               string    `json:"id" yaml:"id"`
	SourceID         string    `json:"event_source_id" yaml:"event_source_id"`
	Bindings         []Binding `json:"bindings,omitempty" yaml:"bindings,omitempty"`
	Instance         string    `json:"instance,omitempty" yaml:"instance,omitempty"`
	Fingerprint      string    `json:"fingerprint,omitempty" yaml:"fingerprint,omitempty"`
	Arrival          Arrival   `json:"arrival" yaml:"arrival"`
	Stages           []Stage   `json:"stages" yaml:"stages"`
	CleanupTemplate  string    `json:"cleanup_template,omitempty" yaml:"cleanup_template,omitempty"`
	DuplicatePercent float64   `json:"duplicate_percent,omitempty" yaml:"duplicate_percent,omitempty"`
	SendDelay        Span      `json:"send_delay,omitempty" yaml:"send_delay,omitempty"`
	OccurredOffset   string    `json:"occurred_offset,omitempty" yaml:"occurred_offset,omitempty"`
}

// Limits 约束进程内资源；达到预算产生背压，MaxMessageBytes 约束单条模板输出。
type Limits struct {
	// SendConcurrency 限制同批不同来源的并行发送，单来源顺序保持不变。
	SendConcurrency int      `json:"send_concurrency" yaml:"send_concurrency"`
	Active          int      `json:"active" yaml:"active"`
	QueueRecords    int      `json:"queue_records" yaml:"queue_records"`
	QueueBytes      int64    `json:"queue_bytes" yaml:"queue_bytes"`
	MaxMessageBytes int      `json:"max_message_bytes" yaml:"max_message_bytes"`
	Batch           int      `json:"batch" yaml:"batch"`
	SendPerSecond   float64  `json:"send_per_second,omitempty" yaml:"send_per_second,omitempty"`
	SendTimeout     Duration `json:"send_timeout" yaml:"send_timeout"`
	CleanupTimeout  Duration `json:"cleanup_timeout" yaml:"cleanup_timeout"`
	Samples         int      `json:"samples" yaml:"samples"`
}

// Config 是一个租户任务的完整配置，没有跨任务引用或隐式继承。
type Config struct {
	Name      string              `json:"name" yaml:"name"`
	TenantID  string              `json:"bk_tenant_id" yaml:"bk_tenant_id"`
	Seed      uint64              `json:"seed" yaml:"seed"`
	StartAt   time.Time           `json:"start_at,omitempty" yaml:"start_at,omitempty"`
	Duration  Duration            `json:"duration,omitempty" yaml:"duration,omitempty"`
	Datasets  map[string]Dataset  `json:"datasets" yaml:"datasets"`
	Templates map[string]Template `json:"templates" yaml:"templates"`
	Scenarios []Scenario          `json:"scenarios" yaml:"scenarios"`
	Limits    Limits              `json:"limits" yaml:"limits"`
}

func (c Config) defaults() Config {
	l := &c.Limits
	if l.SendConcurrency == 0 {
		l.SendConcurrency = 1
	}
	if l.Active == 0 {
		l.Active = 100000
	}
	if l.QueueRecords == 0 {
		l.QueueRecords = 10000
	}
	if l.QueueBytes == 0 {
		l.QueueBytes = 32 << 20
	}
	if l.MaxMessageBytes == 0 {
		l.MaxMessageBytes = 1 << 20
	}
	if l.Batch == 0 {
		l.Batch = 1000
	}
	if l.SendTimeout == "" {
		l.SendTimeout = "10s"
	}
	if l.CleanupTimeout == "" {
		l.CleanupTimeout = "30s"
	}
	if l.Samples == 0 {
		l.Samples = 50
	}
	return c
}

var namePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func validRate(v float64) bool { return v >= 0 && !math.IsNaN(v) && !math.IsInf(v, 0) }

// Validate 检查外部配置的结构和可执行参数，不运行 Cleaner 或查询业务数据。
func (c Config) Validate() error {
	c = c.defaults()
	if c.TenantID == "" || len(c.TenantID) > 64 {
		return fmt.Errorf("bk_tenant_id is required (max 64 bytes)")
	}
	if len(c.Scenarios) == 0 {
		return fmt.Errorf("scenarios is empty")
	}
	if e := c.Duration.validate(); e != nil {
		return e
	}
	l := c.Limits
	if l.SendConcurrency < 1 || l.Active < 1 || l.QueueRecords < 1 || l.QueueBytes < 1 || l.MaxMessageBytes < 1 || int64(l.MaxMessageBytes) > l.QueueBytes || l.Batch < 1 || l.Batch > l.QueueRecords || l.Samples < 0 || l.Samples > 1000 || !validRate(l.SendPerSecond) {
		return fmt.Errorf("invalid resource limits")
	}
	for _, d := range []Duration{l.SendTimeout, l.CleanupTimeout} {
		if e := d.validate(); e != nil {
			return e
		}
		if d.Value() == 0 {
			return fmt.Errorf("timeouts must be positive")
		}
	}
	ids := map[string]bool{}
	for _, s := range c.Scenarios {
		if !namePattern.MatchString(s.ID) || ids[s.ID] || s.SourceID == "" || len(s.Stages) == 0 {
			return fmt.Errorf("scenario requires unique id, event_source_id and stages")
		}
		ids[s.ID] = true
		needed := 1
		if s.DuplicatePercent > 0 {
			needed = 2
		}
		if l.QueueRecords < needed || l.QueueBytes < int64(needed)*int64(l.MaxMessageBytes+512) {
			return fmt.Errorf("queue budget must hold one message and its configured duplicate")
		}
		a := s.Arrival
		if !validRate(a.PerSecond) || a.Batch < 0 || a.PerSecond > 0 && (a.Batch > 0 || a.Every.positive()) {
			return fmt.Errorf("scenario %s: choose rate or periodic arrivals", s.ID)
		}
		if a.PerSecond == 0 && len(a.Rates) == 0 && !a.Every.positive() {
			return fmt.Errorf("scenario %s: arrival rate or interval is required", s.ID)
		}
		if e := a.Delay.validate(); e != nil {
			return e
		}
		if e := a.Every.validate(); e != nil {
			return e
		}
		last := time.Duration(-1)
		for _, r := range a.Rates {
			if e := r.At.validate(); e != nil {
				return e
			}
			if r.At.Value() <= last || !validRate(r.PerSecond) {
				return fmt.Errorf("rate steps must be ordered and nonnegative")
			}
			last = r.At.Value()
		}
		if s.DuplicatePercent < 0 || s.DuplicatePercent > 100 {
			return fmt.Errorf("duplicate_percent must be 0..100")
		}
		if e := s.SendDelay.validate(); e != nil {
			return e
		}
		if s.OccurredOffset != "" {
			if _, e := time.ParseDuration(s.OccurredOffset); e != nil {
				return e
			}
		}
		bindings := map[string]bool{}
		for _, b := range s.Bindings {
			d, ok := c.Datasets[b.Dataset]
			if !ok || len(d.Rows) == 0 || !namePattern.MatchString(b.Name) || bindings[b.Name] {
				return fmt.Errorf("scenario %s: invalid dataset binding %s", s.ID, b.Name)
			}
			bindings[b.Name] = true
			if b.Mode != "random" && b.Mode != "weighted" && b.Mode != "iterate" {
				return fmt.Errorf("binding mode must be random, weighted or iterate")
			}
			if b.Mode == "weighted" {
				for _, row := range d.Rows {
					v, ok := numeric(row[d.WeightColumn])
					if !ok || v <= 0 {
						return fmt.Errorf("weighted rows require positive numeric weights")
					}
				}
			}
		}
		names := map[string]bool{}
		for _, st := range s.Stages {
			if !namePattern.MatchString(st.Name) || names[st.Name] {
				return fmt.Errorf("unique stage name required")
			}
			names[st.Name] = true
			t, ok := c.Templates[st.Template]
			if !ok {
				return fmt.Errorf("template %s not found", st.Template)
			}
			if t.Kind != "standard" && t.Kind != "json" {
				return fmt.Errorf("template kind must be standard or json")
			}
			if t.Kind == "json" && s.CleanupTemplate == "" {
				return fmt.Errorf("custom JSON scenario requires cleanup_template")
			}
			if e := st.Duration.validate(); e != nil {
				return e
			}
			if e := st.Every.validate(); e != nil {
				return e
			}
			if len(st.Next) > 0 && !st.Duration.positive() {
				return fmt.Errorf("branching stages require positive duration")
			}
			if st.Terminal && (len(st.Next) > 0 || st.Every.positive()) {
				return fmt.Errorf("terminal stage cannot repeat or branch")
			}
		}
		for _, st := range s.Stages {
			for _, b := range st.Next {
				if b.Stage != "" && !names[b.Stage] || b.Weight <= 0 || !validRate(b.Weight) {
					return fmt.Errorf("invalid next stage")
				}
			}
		}
		if s.CleanupTemplate != "" {
			if _, ok := c.Templates[s.CleanupTemplate]; !ok {
				return fmt.Errorf("cleanup template not found")
			}
		}
	}
	return nil
}

func numeric(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, e := n.Float64()
		return f, e == nil
	default:
		return 0, false
	}
}
