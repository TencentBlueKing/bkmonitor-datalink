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
	"container/heap"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"sync"
	"time"

	"linkd/internal/config"
	"linkd/internal/logging"
)

// Publisher 返回逐条发送结果，已确认记录不会因同批失败而回滚。
type Publisher interface {
	Publish(context.Context, []Record) []error
	Close()
}

// Sample 保存有界身份样本，不复制完整业务正文。
type Sample struct {
	EventID     string `json:"event_id"`
	Fingerprint string `json:"fingerprint"`
	SourceID    string `json:"event_source_id"`
	ScenarioID  string `json:"scenario_id"`
	Cleanup     bool   `json:"cleanup"`
	Sent        bool   `json:"sent"`
}

// Status 区分实际配置版本、Kafka 确认和生成器运行状态，不推断下游完成。
type Status struct {
	State             string    `json:"state"`
	Revision          int64     `json:"revision"`
	RequestedRevision int64     `json:"requested_revision"`
	Generated         uint64    `json:"generated"`
	Sent              uint64    `json:"sent"`
	Failed            uint64    `json:"failed"`
	Created           uint64    `json:"created"`
	Active            int       `json:"active"`
	Queued            int       `json:"queued"`
	QueueBytes        int64     `json:"queue_bytes"`
	CleanupPending    int       `json:"cleanup_pending"`
	CleanupSent       uint64    `json:"cleanup_sent"`
	EPS               float64   `json:"eps"`
	TargetNewEPS      float64   `json:"target_new_eps"`
	LagSeconds        float64   `json:"lag_seconds"`
	Error             string    `json:"error,omitempty"`
	StartedAt         time.Time `json:"started_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	Samples           []Sample  `json:"samples"`
}

type instance struct {
	cleanupRecord   *Record
	sequence        uint64
	scenario, stage int
	context         RenderContext
	entered, end    time.Duration
	eventSequence   uint64
	closing         bool
}

type due struct {
	at              time.Duration
	kind            int
	scenario        int
	sequence, order uint64
	record          *Record
}

const (
	birth    = 0
	enter    = 1
	repeat   = 2
	advance  = 3
	delivery = 4
)

type dueQueue []due

func (q dueQueue) Len() int { return len(q) }

func (q dueQueue) Less(i, j int) bool {
	if q[i].at != q[j].at {
		return q[i].at < q[j].at
	}
	if q[i].kind != q[j].kind {
		return q[i].kind > q[j].kind
	}
	return q[i].order < q[j].order
}

func (q dueQueue) Swap(i, j int) { q[i], q[j] = q[j], q[i] }

func (q *dueQueue) Push(v any) { *q = append(*q, v.(due)) }

func (q *dueQueue) Pop() any { v := (*q)[len(*q)-1]; *q = (*q)[:len(*q)-1]; return v }

type sendResult struct {
	records []Record
	errors  []error
}

// Engine 的状态由互斥区串行推进；网络发送使用独立协程，控制请求不等待 Kafka。
type Engine struct {
	logger           *slog.Logger
	delayedRecords   int
	delayedBytes     int64
	roundAt          time.Time
	pendingPublisher Publisher
	mu               sync.Mutex
	program          *Program
	pending          *Program
	pendingRevision  int64
	runID            string
	publisher        Publisher
	status           Status
	queue            dueQueue
	active           map[uint64]*instance
	born             []uint64
	birthsDone       []bool
	order, sequence  uint64
	elapsed          time.Duration
	last             time.Time
	paused           bool
	stopping         bool
	force            bool
	nextSend         time.Time
	cleanupStarted   time.Time
	inbox            chan []Record
	results          chan sendResult
	done             chan struct{}
}

// New 创建尚未运行的引擎，start 也是可复现预览的时间基准。
func New(c Config, runID string, revision int64, start time.Time, p Publisher, loggers ...*slog.Logger) (*Engine, error) {
	logger, logErr := logging.New(logging.DefaultConfig(), io.Discard)
	if logErr != nil {
		return nil, logErr
	}
	if len(loggers) > 0 && loggers[0] != nil {
		logger = loggers[0]
	}
	program, e := Compile(c)
	if e != nil {
		return nil, e
	}
	if runID == "" {
		return nil, fmt.Errorf("run_id is required")
	}
	x := &Engine{logger: logger, program: program, runID: runID, publisher: p, active: map[uint64]*instance{}, born: make([]uint64, len(c.Scenarios)), birthsDone: make([]bool, len(c.Scenarios)), last: start, roundAt: start, inbox: make(chan []Record, 1), results: make(chan sendResult, 1), done: make(chan struct{}), status: Status{State: "running", Revision: revision, RequestedRevision: revision, StartedAt: start, Samples: []Sample{}}}
	for i, s := range c.Scenarios {
		x.push(due{kind: birth, scenario: i, at: s.Arrival.Delay.Value()})
	}
	return x, nil
}

func (e *Engine) push(d due) {
	if d.kind == delivery {
		e.delayedRecords++
		e.delayedBytes += recordBytes(*d.record)
	}
	e.order++
	d.order = e.order
	heap.Push(&e.queue, d)
}

// Snapshot 返回并发安全的摘要副本。
func (e *Engine) Snapshot() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.status
	s.Active = len(e.active)
	s.Queued += e.delayedRecords
	s.QueueBytes += e.delayedBytes
	s.Samples = append([]Sample(nil), s.Samples...)
	return s
}

// Apply 在边界编译新配置，速率更新保持身份；结构更新进入收尾后再安装。
func (e *Engine) Apply(c Config, revision int64) error { return e.applyPublisher(c, revision, nil) }

// ApplySources 在本地配置热加载时同时更新来源连接；旧连接在收尾完成后才释放。
func (e *Engine) ApplySources(c Config, revision int64, sources map[string]config.EventSource) error {
	p, err := NewKafkaPublisher(sources, c.Limits)
	if err != nil {
		return err
	}
	if err = e.applyPublisher(c, revision, p); err != nil {
		p.Close()
	}
	return err
}

func (e *Engine) applyPublisher(c Config, revision int64, publisher Publisher) error {
	p, err := Compile(c)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if c.TenantID != e.program.Config.TenantID {
		return fmt.Errorf("cannot change tenant during a run")
	}
	e.status.RequestedRevision = revision
	if RateOnly(e.program.Config, c) && !e.stopping {
		if publisher != nil {
			publisher.Close()
		}
		e.program.Config = p.Config
		for i := range p.scenarios {
			e.program.scenarios[i].config.Arrival = p.scenarios[i].config.Arrival
		}
		e.status.Revision = revision
		for i := range e.queue {
			d := &e.queue[i]
			if d.kind == birth && e.elapsed >= e.program.scenarios[d.scenario].config.Arrival.Delay.Value() {
				d.at = e.elapsed + e.arrivalDelay(d.scenario)
			}
		}
		heap.Init(&e.queue)
		return nil
	}
	if e.pendingPublisher != nil {
		e.pendingPublisher.Close()
	}
	e.pendingPublisher = publisher
	e.pending = p
	e.pendingRevision = revision
	e.beginStop()
	return nil
}

// Control 接受 pause/resume/stop/retry_cleanup/force_stop；停止默认收尾。
func (e *Engine) Control(action string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	switch action {
	case "pause":
		if e.stopping {
			return fmt.Errorf("run is stopping")
		}
		e.paused = true
		e.status.State = "paused"
	case "resume":
		if e.stopping {
			return fmt.Errorf("run is stopping")
		}
		e.paused = false
		e.last = time.Now()
		e.status.State = "running"
	case "stop":
		e.pending = nil
		e.beginStop()
	case "retry_cleanup":
		if e.status.State != "cleanup_failed" {
			return fmt.Errorf("cleanup has not failed")
		}
		for _, a := range e.active {
			a.closing = false
		}
		e.status.Error = ""
		e.status.State = "stopping"
		e.cleanupStarted = time.Time{}
	case "force_stop":
		e.force = true
		e.pending = nil
		e.status.State = "stopped"
	default:
		return fmt.Errorf("unsupported action")
	}
	return nil
}

func (e *Engine) beginStop() {
	e.stopping = true
	e.paused = false
	e.status.State = "stopping"
	e.cleanupStarted = time.Time{}
	filtered := dueQueue{}
	for _, d := range e.queue {
		if d.kind == delivery {
			d.at = min(d.at, e.elapsed)
			filtered = append(filtered, d)
		}
	}
	e.queue = filtered
	heap.Init(&e.queue)
}

func (e *Engine) rate(si int) float64 {
	a := e.program.scenarios[si].config.Arrival
	v := a.PerSecond
	for _, r := range a.Rates {
		if e.elapsed-a.Delay.Value() >= r.At.Value() {
			v = r.PerSecond
		}
	}
	return v
}

func (e *Engine) arrivalDelay(si int) time.Duration {
	a := e.program.scenarios[si].config.Arrival
	if a.Every.positive() {
		return e.program.span(a.Every)
	}
	r := e.rate(si)
	if r <= 0 {
		return time.Second
	}
	return max(time.Nanosecond, time.Duration(float64(time.Second)/r))
}

func (e *Engine) record(a *instance, at time.Time, cleanup bool) (Record, error) {
	if cleanup && a.cleanupRecord != nil {
		return *a.cleanupRecord, nil
	}
	a.eventSequence++
	r, err := e.program.render(a.scenario, a.stage, a.context, a.eventSequence, at, cleanup)
	r.instanceSequence = a.sequence
	r.terminal = cleanup || e.program.scenarios[a.scenario].config.Stages[a.stage].Terminal
	if cleanup && err == nil {
		a.cleanupRecord = &r
	}
	return r, err
}

func (e *Engine) emit(a *instance, at time.Duration) {
	r, err := e.record(a, e.roundAt.Add(at), false)
	if err != nil {
		e.status.Failed++
		e.status.Error = err.Error()
		e.logger.Error("eventgen render failed", "module", "eventgen", "operation", "render", "error", err, "bk_tenant_id", e.program.Config.TenantID, "run_id", e.runID)
		return
	}
	e.push(due{at: at + e.program.span(e.program.scenarios[a.scenario].config.SendDelay), kind: delivery, record: &r})
	if e.program.random.Float64()*100 < e.program.scenarios[a.scenario].config.DuplicatePercent {
		duplicate := r
		duplicate.terminal = false
		e.push(due{at: at + e.program.span(e.program.scenarios[a.scenario].config.SendDelay), kind: delivery, record: &duplicate})
	}
}

func (e *Engine) transition(a *instance, at time.Duration) {
	s := e.program.scenarios[a.scenario]
	st := s.config.Stages[a.stage]
	next := a.stage + 1
	if len(st.Next) > 0 {
		total := 0.0
		for _, n := range st.Next {
			total += n.Weight
		}
		draw := e.program.random.Float64() * total
		for _, n := range st.Next {
			draw -= n.Weight
			if draw < 0 {
				if n.Stage == "" {
					next = len(s.config.Stages)
				} else {
					next = s.stages[n.Stage]
				}
				break
			}
		}
	}
	if next >= len(s.config.Stages) {
		if !a.closing {
			r, err := e.record(a, e.roundAt.Add(at), true)
			if err != nil {
				e.status.Error = err.Error()
				return
			}
			a.closing = true
			e.push(due{at: at, kind: delivery, record: &r})
		}
		return
	}
	a.stage = next
	e.push(due{at: at, kind: enter, sequence: a.sequence})
}

// plan 按到期时间生成有界批次；迟到只记录延迟，不跳过生命周期事件。
func (e *Engine) plan(now time.Time) []Record {
	p := e.program
	limits := p.Config.Limits
	if now.Before(e.last) {
		if !e.stopping {
			return nil
		}
		e.last = now
	}
	delta := now.Sub(e.last)
	e.last = now
	if !e.paused {
		e.elapsed += delta
	} else {
		e.roundAt = e.roundAt.Add(delta)
	}
	e.status.UpdatedAt = now
	e.status.TargetNewEPS = 0
	for i := range p.scenarios {
		e.status.TargetNewEPS += e.rate(i)
	}
	if e.paused || e.force {
		return nil
	}
	if !e.stopping && p.Config.Duration.Value() > 0 && e.elapsed >= p.Config.Duration.Value() {
		e.beginStop()
	}
	if e.status.Queued >= limits.QueueRecords || e.status.QueueBytes+int64(limits.MaxMessageBytes) > limits.QueueBytes {
		return nil
	}
	batch := make([]Record, 0, limits.Batch)
	bytes := int64(0)
	steps := 0
	for e.queue.Len() > 0 && e.queue[0].at <= e.elapsed && len(batch) < limits.Batch && steps < limits.Batch*4 {
		steps++
		d := heap.Pop(&e.queue).(due)
		if d.kind == delivery {
			e.delayedRecords--
			e.delayedBytes -= recordBytes(*d.record)
			r := *d.record
			size := recordBytes(r)
			if e.status.Queued+len(batch) >= limits.QueueRecords || e.status.QueueBytes+bytes+size > limits.QueueBytes {
				e.push(d)
				break
			}
			batch = append(batch, r)
			bytes += size
			e.status.LagSeconds = max(e.status.LagSeconds, (e.elapsed - d.at).Seconds())
			continue
		}

		if e.stopping {
			continue
		}
		// 延迟投递也占用队列预算；满载时把尚未渲染的工作推迟到队列释放后。
		needed := 1
		scenarioIndex := d.scenario
		if d.kind != birth {
			if a := e.active[d.sequence]; a != nil {
				scenarioIndex = a.scenario
			}
		}
		if p.scenarios[scenarioIndex].config.DuplicatePercent > 0 {
			needed = 2
		}
		if e.status.Queued+e.delayedRecords+len(batch)+needed > limits.QueueRecords || e.status.QueueBytes+e.delayedBytes+bytes+int64(needed)*int64(limits.MaxMessageBytes+512) > limits.QueueBytes {
			next := e.elapsed + 10*time.Millisecond
			for _, queued := range e.queue {
				if queued.kind == delivery && queued.at > next {
					next = queued.at
				}
			}
			d.at = next
			e.push(d)
			continue
		}
		if d.kind == birth {
			s := p.scenarios[d.scenario].config
			if s.Arrival.Count > 0 && e.born[d.scenario] >= s.Arrival.Count {
				e.birthsDone[d.scenario] = true
				continue
			}
			if len(e.active) >= limits.Active {
				d.at = e.elapsed + time.Millisecond*10
				e.push(d)
				continue
			}
			if !s.Arrival.Every.positive() && e.rate(d.scenario) == 0 {
				d.at = e.elapsed + time.Second
				e.push(d)
				continue
			}
			count := max(1, s.Arrival.Batch)
			for range count {
				if len(e.active) >= limits.Active || s.Arrival.Count > 0 && e.born[d.scenario] >= s.Arrival.Count {
					break
				}
				e.sequence++
				c, err := p.instance(d.scenario, e.runID, e.sequence, e.roundAt.Add(d.at))
				if errors.Is(err, ErrExhausted) {
					e.birthsDone[d.scenario] = true
					break
				}
				if err != nil {
					e.status.Failed++
					e.status.Error = err.Error()
					break
				}
				a := &instance{sequence: e.sequence, scenario: d.scenario, context: c}
				e.active[a.sequence] = a
				e.born[d.scenario]++
				e.status.Created++
				e.push(due{at: d.at, kind: enter, sequence: a.sequence})
			}
			if !e.birthsDone[d.scenario] {
				e.push(due{at: d.at + e.arrivalDelay(d.scenario), kind: birth, scenario: d.scenario})
			}
			continue
		}
		a := e.active[d.sequence]
		if a == nil {
			continue
		}
		st := p.scenarios[a.scenario].config.Stages[a.stage]
		switch d.kind {
		case enter:
			a.entered = d.at
			a.end = d.at + p.span(st.Duration)
			e.emit(a, d.at)
			if st.Terminal {
				a.closing = true
				continue
			}
			if a.end > d.at {
				e.push(due{at: a.end, kind: advance, sequence: a.sequence})
				if st.Every.positive() {
					next := d.at + p.span(st.Every)
					if next < a.end {
						e.push(due{at: next, kind: repeat, sequence: a.sequence})
					}
				}
			} else {
				e.transition(a, d.at)
			}
		case repeat:
			if d.at >= a.end {
				continue
			}
			e.emit(a, d.at)
			next := d.at + p.span(st.Every)
			if next < a.end {
				e.push(due{at: next, kind: repeat, sequence: a.sequence})
			}
		case advance:
			e.transition(a, d.at)
		}
	}
	if !e.stopping && e.queue.Len() == 0 {
		e.beginStop()
	}
	if e.stopping && e.queue.Len() == 0 && e.status.Queued == 0 && len(batch) == 0 && e.status.State != "cleanup_failed" {
		if e.cleanupStarted.IsZero() {
			e.cleanupStarted = now
		}
		for _, id := range sortedInstanceIDs(e.active) {
			a := e.active[id]
			if a.closing {
				continue
			}
			r, err := e.record(a, now, true)
			if err != nil {
				e.status.Error = err.Error()
				e.status.State = "cleanup_failed"
				break
			}
			a.closing = true
			batch = append(batch, r)
			bytes += recordBytes(r)
			if len(batch) >= limits.Batch || bytes >= limits.QueueBytes/2 {
				break
			}
		}
		if len(batch) == 0 && len(e.active) > 0 {
			e.status.State = "cleanup_failed"
			e.status.Error = "cleanup delivery failed; retry or force stop"
		}
		if len(e.active) == 0 {
			e.finishSwitch(now)
		}
	}
	e.status.Generated += uint64(len(batch))
	e.status.Queued += len(batch)
	e.status.QueueBytes += bytes
	e.status.CleanupPending = 0
	if e.stopping {
		e.status.CleanupPending = len(e.active)
	}
	return batch
}

func sortedInstanceIDs(m map[uint64]*instance) []uint64 {
	ids := make([]uint64, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func (e *Engine) finishSwitch(now time.Time) {
	if e.pending == nil {
		e.status.State = "completed"
		return
	}
	if e.pendingPublisher != nil {
		e.publisher.Close()
		e.publisher = e.pendingPublisher
		e.pendingPublisher = nil
	}
	e.program = e.pending
	e.pending = nil
	e.status.Revision = e.pendingRevision
	e.stopping = false
	e.status.State = "running"
	e.elapsed = 0
	e.roundAt = now
	e.last = now
	e.born = make([]uint64, len(e.program.scenarios))
	e.birthsDone = make([]bool, len(e.program.scenarios))
	for i, s := range e.program.scenarios {
		e.push(due{kind: birth, scenario: i, at: s.config.Arrival.Delay.Value()})
	}
}

func recordBytes(r Record) int64 {
	n := len(r.Body) + len(r.Key) + 128
	for k, v := range r.Headers {
		n += len(k) + len(v)
	}
	return int64(n)
}

func (e *Engine) ack(r sendResult) {
	logged := false
	for i, record := range r.records {
		e.status.Queued--
		e.status.QueueBytes -= recordBytes(record)
		ok := i < len(r.errors) && r.errors[i] == nil
		if ok {
			e.status.Sent++
			if record.Cleanup {
				e.status.CleanupSent++
			}
			if record.terminal {
				delete(e.active, record.instanceSequence)
			}
		} else {
			e.status.Failed++
			e.status.Error = "Kafka delivery failed"
			if i < len(r.errors) && r.errors[i] != nil {
				e.status.Error = fmt.Sprintf("Kafka delivery failed: %v", r.errors[i])
				if !logged {
					logged = true
					e.logger.Error("eventgen publish failed", "module", "eventgen", "operation", "publish", "error", r.errors[i], "bk_tenant_id", e.program.Config.TenantID, "run_id", e.runID, "event_source_id", record.SourceID, "event_id", record.SourceEventID)
				}
			}
		}
		sample := Sample{EventID: record.SourceEventID, Fingerprint: record.Fingerprint, SourceID: record.SourceID, ScenarioID: record.ScenarioID, Cleanup: record.Cleanup, Sent: ok}
		if maxSamples := e.program.Config.Limits.Samples; maxSamples > 0 {
			if len(e.status.Samples) >= maxSamples {
				e.status.Samples = e.status.Samples[1:]
			}
			e.status.Samples = append(e.status.Samples, sample)
		}
	}
	if seconds := time.Since(e.status.StartedAt).Seconds(); seconds > 0 {
		e.status.EPS = float64(e.status.Sent) / seconds
	}
}

// Run 驱动调度和独立发送；取消后使用独立有界上下文执行收尾。
func (e *Engine) Run(ctx context.Context) error {
	defer close(e.done)
	sendCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	senderDone := make(chan struct{})
	go func() {
		defer close(senderDone)
		for {
			select {
			case <-sendCtx.Done():
				return
			case batch := <-e.inbox:
				for {
					e.mu.Lock()
					paused := e.paused
					e.mu.Unlock()
					if !paused {
						break
					}
					if err := waitContext(sendCtx, 10*time.Millisecond); err != nil {
						return
					}
				}
				e.mu.Lock()
				limits := e.program.Config.Limits
				publisher := e.publisher
				e.mu.Unlock()
				if limits.SendPerSecond > 0 {
					wait := time.Until(e.nextSend)
					if wait > 0 {
						if err := waitContext(sendCtx, wait); err != nil {
							return
						}
					}
					e.nextSend = time.Now().Add(time.Duration(float64(time.Second) * float64(len(batch)) / limits.SendPerSecond))
				}
				call, stop := context.WithTimeout(sendCtx, limits.SendTimeout.Value())
				result := publisher.Publish(call, batch)
				stop()
				select {
				case e.results <- sendResult{batch, result}:
				case <-sendCtx.Done():
					return
				}
			}
		}
	}()
	defer func() {
		cancel()
		<-senderDone
		e.publisher.Close()
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.pendingPublisher != nil {
			e.pendingPublisher.Close()
		}
	}()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	parent := ctx.Done()
	var shutdown <-chan time.Time
	var shutdownTimer *time.Timer
	defer func() {
		if shutdownTimer != nil {
			shutdownTimer.Stop()
		}
	}()
	var pending []Record
	for {
		select {
		case <-parent:
			e.mu.Lock()
			e.pending = nil
			e.beginStop()
			timeout := e.program.Config.Limits.CleanupTimeout.Value()
			e.mu.Unlock()
			parent = nil
			shutdownTimer = time.NewTimer(timeout)
			shutdown = shutdownTimer.C
		case <-shutdown:
			e.mu.Lock()
			e.status.State = "interrupted"
			e.status.Error = "shutdown cleanup deadline exceeded"
			e.mu.Unlock()
			return nil
		case r := <-e.results:
			e.mu.Lock()
			e.ack(r)
			e.mu.Unlock()
		case now := <-ticker.C:
			e.mu.Lock()
			if e.force {
				e.mu.Unlock()
				return nil
			}
			if e.status.State == "completed" {
				e.mu.Unlock()
				return nil
			}
			if len(pending) == 0 {
				pending = e.plan(now)
			}
			e.mu.Unlock()
		}
		if len(pending) > 0 {
			select {
			case e.inbox <- pending:
				pending = nil
			default:
			}
		}
	}
}

func waitContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Preview 使用同一调度与渲染器推进虚拟时间，返回有界样本，不发送消息。
func Preview(c Config, start time.Time, count int) ([]Record, error) {
	if count < 1 || count > 100 {
		return nil, fmt.Errorf("preview count must be 1..100")
	}
	if start.IsZero() {
		start = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	e, err := New(c, "preview", 1, start, nil)
	if err != nil {
		return nil, err
	}
	records := []Record{}
	for steps := 0; steps < 10000 && len(records) < count; steps++ {
		if e.queue.Len() == 0 {
			break
		}
		at := start.Add(e.queue[0].at)
		batch := e.plan(at)
		records = append(records, batch...)
		e.ack(sendResult{batch, make([]error, len(batch))})
		if e.status.Error != "" {
			return nil, fmt.Errorf("preview: %s", e.status.Error)
		}
		if e.status.State == "completed" {
			break
		}
	}
	if len(records) > count {
		records = records[:count]
	}
	return records, nil
}
