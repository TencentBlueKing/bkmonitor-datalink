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
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"linkd/internal/config"
	"linkd/internal/eventsource"
)

// ErrNotFound 表示当前租户没有该任务或运行。
var ErrNotFound = errors.New("eventgen object not found")

// ErrConflict 表示编辑版本变化或运行状态不允许该操作。
var ErrConflict = errors.New("eventgen revision or state conflict")

// Documents 是任务与运行摘要的租户前缀查询和单对象条件写入端口。
type Documents interface {
	Get(context.Context, string, string) (json.RawMessage, string, error)
	Put(context.Context, string, string, string, json.RawMessage) error
	List(context.Context, string, string, string, int) ([]json.RawMessage, error)
}

// SourceReader 只读取已经发布的来源，不修改其配置。
type SourceReader interface {
	Get(context.Context, string) (eventsource.Record, error)
	GetRelease(context.Context, string, int64) (eventsource.Release, error)
}

// Plan 保存任务当前配置及编辑版本。
type Plan struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
	Config   Config `json:"config"`
	Deleted  bool   `json:"deleted"`
}

// Command 绑定运行的操作身份；同一 OperationID 不重复执行。
type Command struct {
	OperationID string `json:"operation_id"`
	Action      string `json:"action"`
	Reason      string `json:"reason"`
}

// Run 保存运行摘要，不包含活动告警或随机状态的恢复快照。
type Run struct {
	Operations       map[string]Command `json:"operations,omitempty"`
	ID               string             `json:"id"`
	PlanID           string             `json:"plan_id"`
	TenantID         string             `json:"bk_tenant_id"`
	SessionID        string             `json:"session_id"`
	Command          Command            `json:"command"`
	AppliedOperation string             `json:"applied_operation"`
	Status           Status             `json:"status"`
	UpdatedAt        time.Time          `json:"updated_at"`
}

// Assignment 仅由内部 Worker 鉴权接口返回，Sources 包含发送所需的敏感连接。
type Assignment struct {
	Run     Run                           `json:"run"`
	Plan    Plan                          `json:"plan"`
	Sources map[string]config.EventSource `json:"sources"`
}

// Heartbeat 汇报有限运行摘要；NewSession 明确表示新进程注册，不隐式重放旧启动。
type Heartbeat struct {
	SessionID  string `json:"session_id"`
	NewSession bool   `json:"new_session"`
	Reports    []Run  `json:"reports"`
}

// Service 管理任务和单进程运行会话，数据以 deployment/tenant 范围持久化。
type Service struct {
	active  map[string]Run
	mu      sync.Mutex
	docs    Documents
	sources SourceReader
	session string
	seen    time.Time
}

// NewService 构造管理服务，不执行存储初始化或启动任务。
func NewService(d Documents, s SourceReader) *Service { return &Service{docs: d, sources: s} }

func tenantPrefix(tenant string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(tenant)) + "."
}

func objectKey(tenant, id string) string { return tenantPrefix(tenant) + id }

func (s *Service) get(ctx context.Context, kind, tenant, id string, target any) (string, error) {
	b, v, e := s.docs.Get(ctx, kind, objectKey(tenant, id))
	if e != nil {
		return "", e
	}
	return v, decodeJSON(b, target)
}

func (s *Service) put(ctx context.Context, kind, tenant, id, version string, value any) error {
	b, e := json.Marshal(value)
	if e != nil {
		return e
	}
	return s.docs.Put(ctx, kind, objectKey(tenant, id), version, b)
}

// loadActive 仅在控制面首次管理访问时恢复运行摘要索引；心跳不扫描全部历史。
func (s *Service) loadActive(ctx context.Context) error {
	if s.active != nil {
		return nil
	}
	active := map[string]Run{}
	after := ""
	for {
		rows, err := s.docs.List(ctx, "runs", "", after, 1000)
		if err != nil {
			return err
		}
		for _, b := range rows {
			var r Run
			if err = decodeJSON(b, &r); err != nil {
				return err
			}
			after = objectKey(r.TenantID, r.ID)
			if !terminalState(r.Status.State) {
				active[after] = r
			}
		}
		if len(rows) < 1000 {
			break
		}
	}
	s.active = active
	return nil
}

// GetPlan 按显式租户读取配置。
func (s *Service) GetPlan(ctx context.Context, tenant, id string) (Plan, error) {
	var p Plan
	_, e := s.get(ctx, "records", tenant, id, &p)
	if e == nil && p.Deleted {
		e = ErrNotFound
	}
	return p, e
}

// ListPlans 返回当前租户的任务页，已删除任务不再出现在管理列表。
func (s *Service) ListPlans(ctx context.Context, tenant, after string, limit int) ([]Plan, error) {
	raw, e := s.docs.List(ctx, "records", tenantPrefix(tenant), objectKey(tenant, after), limit)
	if e != nil {
		return nil, e
	}
	result := []Plan{}
	for _, b := range raw {
		var p Plan
		if e = decodeJSON(b, &p); e != nil {
			return nil, e
		}
		if !p.Deleted {
			result = append(result, p)
		}
	}
	return result, nil
}

// ApplyPlan 编译后条件写入，保存成功即可由运行进程下一轮热加载。
func (s *Service) ApplyPlan(ctx context.Context, tenant, id string, revision int64, c Config) (Plan, error) {
	if tenant == "" || tenant != c.TenantID || !namePattern.MatchString(id) {
		return Plan{}, fmt.Errorf("invalid tenant or task identity")
	}
	if _, e := Compile(c); e != nil {
		return Plan{}, e
	}
	if _, e := s.ResolveSources(ctx, c); e != nil {
		return Plan{}, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var old Plan
	token, e := s.get(ctx, "records", tenant, id, &old)
	if e != nil && !errors.Is(e, ErrNotFound) {
		return Plan{}, e
	}
	if revision != old.Revision {
		return Plan{}, ErrConflict
	}
	p := Plan{ID: id, Revision: revision + 1, Config: c}
	return p, s.put(ctx, "records", tenant, id, token, p)
}

// DeletePlan 只删除没有活动运行的配置，保留运行历史。
func (s *Service) DeletePlan(ctx context.Context, tenant, id string, revision int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.loadActive(ctx); e != nil {
		return e
	}
	for _, r := range s.active {
		if r.TenantID == tenant && r.PlanID == id && !terminalState(r.Status.State) {
			return ErrConflict
		}
	}
	var p Plan
	v, e := s.get(ctx, "records", tenant, id, &p)
	if e != nil {
		return e
	}
	if p.Revision != revision {
		return ErrConflict
	}
	p.Deleted = true
	p.Revision++
	return s.put(ctx, "records", tenant, id, v, p)
}

// ResolveSources 从正式已发布配置取得来源，校验租户绑定和 Kafka 目标。
func (s *Service) ResolveSources(ctx context.Context, c Config) (map[string]config.EventSource, error) {
	result := map[string]config.EventSource{}
	for _, scenario := range c.Scenarios {
		if _, ok := result[scenario.SourceID]; ok {
			continue
		}
		record, e := s.sources.Get(ctx, scenario.SourceID)
		if e != nil {
			return nil, e
		}
		if record.Deleted || record.Published == 0 {
			return nil, fmt.Errorf("source %s is not published", scenario.SourceID)
		}
		release, e := s.sources.GetRelease(ctx, scenario.SourceID, record.Published)
		if e != nil {
			return nil, e
		}
		source := release.Spec.WithDefaults()
		if !source.Enabled || source.Storage.Type != config.StorageTypeKafka {
			return nil, fmt.Errorf("source %s must be enabled Kafka", scenario.SourceID)
		}
		if source.RelatedTenantID != "" && source.RelatedTenantID != c.TenantID {
			return nil, fmt.Errorf("source belongs to another tenant")
		}
		result[scenario.SourceID] = source
	}
	return result, nil
}

func terminalState(state string) bool {
	return state == "failed" || state == "completed" || state == "stopped" || state == "interrupted"
}

// GetRun 读取租户范围内的运行摘要。
func (s *Service) GetRun(ctx context.Context, tenant, id string) (Run, error) {
	var r Run
	_, e := s.get(ctx, "runs", tenant, id, &r)
	return r, e
}

// ListRuns 返回租户运行历史，游标是上一页最后一个运行 ID。
func (s *Service) ListRuns(ctx context.Context, tenant, after string, limit int) ([]Run, error) {
	prefix := ""
	key := after
	if tenant != "" {
		prefix = tenantPrefix(tenant)
		key = objectKey(tenant, after)
	}
	raw, e := s.docs.List(ctx, "runs", prefix, key, limit)
	if e != nil {
		return nil, e
	}
	rs := []Run{}
	for _, b := range raw {
		var r Run
		if e = decodeJSON(b, &r); e != nil {
			return nil, e
		}
		rs = append(rs, r)
	}
	return rs, nil
}

// Start 只为当前在线进程创建启动命令，operation_id 同时作为运行 ID 以处理回执不确定。
func (s *Service) Start(ctx context.Context, tenant, planID string, c Command) (Run, error) {
	if c.OperationID == "" || !namePattern.MatchString(c.OperationID) {
		return Run{}, fmt.Errorf("valid operation_id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, e := s.GetRun(ctx, tenant, c.OperationID); e == nil {
		if old.PlanID != planID {
			return Run{}, ErrConflict
		}
		return old, nil
	} else if !errors.Is(e, ErrNotFound) {
		return Run{}, e
	}
	if s.session == "" || time.Since(s.seen) > 15*time.Second {
		return Run{}, fmt.Errorf("eventgen runner is offline")
	}
	if _, e := s.GetPlan(ctx, tenant, planID); e != nil {
		return Run{}, e
	}
	if e := s.loadActive(ctx); e != nil {
		return Run{}, e
	}
	for _, r := range s.active {
		if r.TenantID == tenant && r.PlanID == planID {
			return Run{}, ErrConflict
		}
	}
	if len(s.active) >= 8 {
		return Run{}, fmt.Errorf("runner supports at most 8 concurrent tasks")
	}
	c.Action = "start"
	r := Run{Operations: map[string]Command{c.OperationID: c}, ID: c.OperationID, PlanID: planID, TenantID: tenant, SessionID: s.session, Command: c, Status: Status{State: "starting"}, UpdatedAt: time.Now().UTC()}
	if err := s.put(ctx, "runs", tenant, r.ID, "", r); err != nil {
		return Run{}, err
	}
	s.active[objectKey(tenant, r.ID)] = r
	return r, nil
}

// Control 保存待执行操作；管理请求返回受理状态，实际结果由下一次报告确认。
func (s *Service) Control(ctx context.Context, tenant, id string, c Command) (Run, error) {
	if !namePattern.MatchString(c.OperationID) {
		return Run{}, fmt.Errorf("operation_id required")
	}
	switch c.Action {
	case "pause", "resume", "stop", "retry_cleanup", "force_stop":
	default:
		return Run{}, fmt.Errorf("unsupported action")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var r Run
	v, e := s.get(ctx, "runs", tenant, id, &r)
	if e != nil {
		return r, e
	}
	if previous, ok := r.Operations[c.OperationID]; ok {
		if previous.Action != c.Action {
			return r, ErrConflict
		}
		return r, nil
	}
	if terminalState(r.Status.State) {
		return r, ErrConflict
	}
	r.Command = c
	if r.Operations == nil {
		r.Operations = map[string]Command{}
	}
	r.Operations[c.OperationID] = c
	r.UpdatedAt = time.Now().UTC()
	if err := s.put(ctx, "runs", tenant, id, v, r); err != nil {
		return Run{}, err
	}
	if s.active != nil {
		s.active[objectKey(tenant, id)] = r
	}
	return r, nil
}

// Poll 回报实际状态并读取当前会话的指令；新进程使旧会话运行明确中断。
func (s *Service) Poll(ctx context.Context, h Heartbeat) ([]Assignment, error) {
	if !namePattern.MatchString(h.SessionID) || len(h.Reports) > 8 {
		return nil, fmt.Errorf("invalid heartbeat")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session != "" && s.session != h.SessionID && !h.NewSession {
		return nil, ErrConflict
	}
	s.session = h.SessionID
	s.seen = time.Now()
	if e := s.loadActive(ctx); e != nil {
		return nil, e
	}
	reports := map[string]Run{}
	for _, r := range h.Reports {
		reports[objectKey(r.TenantID, r.ID)] = r
	}
	assignments := []Assignment{}
	for _, key := range sortedKeys(s.active) {
		listed := s.active[key]
		if terminalState(listed.Status.State) {
			continue
		}
		var r Run
		v, e := s.get(ctx, "runs", listed.TenantID, listed.ID, &r)
		if e != nil {
			return nil, e
		}
		if r.SessionID != h.SessionID {
			r.Status.State = "interrupted"
			r.Status.Error = "runner restarted; start a new run explicitly"
		} else if report, ok := reports[objectKey(r.TenantID, r.ID)]; ok {
			r.Status = report.Status
			r.AppliedOperation = report.AppliedOperation
		}
		r.UpdatedAt = time.Now().UTC()
		if e = s.put(ctx, "runs", r.TenantID, r.ID, v, r); e != nil {
			return nil, e
		}
		if terminalState(r.Status.State) {
			delete(s.active, key)
			continue
		}
		s.active[key] = r
		p, e := s.GetPlan(ctx, r.TenantID, r.PlanID)
		if e != nil {
			return nil, e
		}
		sources, e := s.ResolveSources(ctx, p.Config)
		if e != nil {
			return nil, e
		}
		// 生成进程只取得 Kafka 所需配置，不携带 Enrich/Hook 的其他凭据。
		for id, source := range sources {
			sources[id] = config.EventSource{EventSourceID: source.EventSourceID, RelatedTenantID: source.RelatedTenantID, Enabled: source.Enabled, Storage: source.Storage}
		}
		assignments = append(assignments, Assignment{Run: r, Plan: p, Sources: sources})
	}
	return assignments, nil
}
