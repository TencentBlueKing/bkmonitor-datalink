// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package suppressioncleanup 保存终态抑制清理的独立持久诊断，不重建 Redis 状态，不修改 Event/Alert。
package suppressioncleanup

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"linkd/internal/domain"
	"linkd/internal/policy"
)

// Cause 固定终态清理的来源。Alert 使用终态业务版本，未生成 Alert 的终态 Event 使用 EventID。
type Cause struct {
	// TenantID 明确隔离业务身份和历史读取。
	TenantID string `json:"bk_tenant_id"`
	// SourceID 与 Fingerprint 固定同一个 Lifecycle 相关性作用域。
	SourceID string `json:"event_source_id"`
	// Fingerprint 使用原值，最长 128 字节，不改写为空或重新计算。
	Fingerprint string `json:"fingerprint"`
	// Trigger 仅允许 alert_terminal 或 event_terminal。
	Trigger string `json:"trigger"`
	// AlertID 只在 Alert 终态存在，也是清理所用 owner。
	AlertID string `json:"alert_id,omitempty"`
	// Revision 是发生清理时的终态业务版本，不是存储 CAS。
	Revision int64 `json:"revision,omitempty"`
	// Status 只允许 recovered/closed；Event-only 不伪造 Alert 状态。
	Status domain.AlertStatus `json:"status,omitempty"`
	// EventID 只在未生成 Alert 的终态输入中保存。
	EventID string `json:"event_id,omitempty"`
}

// Validate 拒绝活动告警和缺少稳定业务身份的调用，不从当前时间补造清理身份。
func (c Cause) Validate() error {
	if domain.ValidateIdentityPart("tenant", c.TenantID, 64) != nil || domain.ValidateIdentityPart("source", c.SourceID, 32) != nil || c.Fingerprint == "" || len(c.Fingerprint) > 128 {
		return policy.ErrInvalid
	}
	switch c.Trigger {
	case "alert_terminal":
		if c.AlertID == "" || len(c.AlertID) > domain.EntityIDMaxBytes || c.Revision < 1 || c.Revision >= 1<<53 || (c.Status != domain.AlertStatusClosed && c.Status != domain.AlertStatusRecovered) || c.EventID != "" {
			return policy.ErrInvalid
		}
	case "event_terminal":
		if c.EventID == "" || len(c.EventID) > domain.EntityIDMaxBytes || c.AlertID != "" || c.Revision != 0 || c.Status != "" {
			return policy.ErrInvalid
		}
	default:
		return policy.ErrInvalid
	}
	return nil
}

// Outcome 的 Removed 只在 Redis 确认结果时存在；失败不能被解释为没有清理或计数为零。
type Outcome struct {
	// State 为 confirmed/unavailable/not_applicable，不能只靠计数判断结果。
	State string `json:"state"`
	// Removed 是本轮确认删除的登记引用数，不是受影响 Event 数或完整历史数量。
	Removed *int `json:"removed,omitempty"`
	// Windows 按 ID 递增保存本次确认清理的每个代次，数量必须与 Removed 相同。
	Windows []Window `json:"windows,omitempty"`
}

// Confirmed 表示当前调用确认删除的登记数；不证明历史数据完整或上一轮没有副作用。
func Confirmed(windows []Window) Outcome {
	n := len(windows)
	return Outcome{State: "confirmed", Removed: &n, Windows: append([]Window(nil), windows...)}
}

// Result 分开记录两类清理；Event-only 终态不清理聚合主。
type Result struct {
	// Clip 记录同 tenant/source/fingerprint 下计数清理的确认。
	Clip Outcome `json:"clip"`
	// Aggregation 记录该 owner 在所有来源/策略下登记的清理确认。
	Aggregation Outcome `json:"aggregation"`
}

func (r Result) validate(c Cause) error {
	for i, o := range []Outcome{r.Clip, r.Aggregation} {
		switch o.State {
		case "confirmed":
			if o.Removed == nil || *o.Removed < 0 || *o.Removed > 512 || *o.Removed != len(o.Windows) {
				return policy.ErrInvalid
			}
			kind := "clip"
			if i == 1 {
				kind = "aggregation"
			}
			last := ""
			for _, w := range o.Windows {
				if w.Validate(kind) != nil || w.ID <= last {
					return policy.ErrInvalid
				}
				last = w.ID
			}
		case "unavailable":
			if o.Removed != nil || len(o.Windows) != 0 {
				return policy.ErrInvalid
			}
		case "not_applicable":
			if i != 1 || c.Trigger != "event_terminal" || o.Removed != nil || len(o.Windows) != 0 {
				return policy.ErrInvalid
			}
		default:
			return policy.ErrInvalid
		}
	}
	if c.Trigger == "event_terminal" && r.Aggregation.State != "not_applicable" {
		return policy.ErrInvalid
	}
	return nil
}

// Record 是独立清理记录；completed 仅表示诊断已完成，Redis 失败也可能在 Result 中。
type Record struct {
	// ID 由 Cause 的稳定终态身份确定。
	ID string `json:"id"`
	// Cause 固定首次终态原因，重试必须完全相同。
	Cause Cause `json:"cause"`
	// State 为 pending/completed；completed 不代表两类 Redis 清理均成功。
	State string `json:"state"`
	// StartedAt 是首次写入意图时间，重试不刷新。
	StartedAt time.Time `json:"started_at"`
	// FinishedAt 仅在最终诊断保存时存在。
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	// PreviousUnconfirmed 表示重试前已有未完成记录；不把本次零删除伪装成首次零删除。
	PreviousUnconfirmed bool `json:"previous_unconfirmed"`
	// Result 在 pending 时为空，不能将缺失结果解释为零。
	Result *Result `json:"result,omitempty"`
}

// ID 返回终态业务身份的确定性摘要，不使用 wall clock 或随机值。
func (c Cause) ID() string {
	b, _ := json.Marshal([]any{"suppression-cleanup:v1", c.TenantID, c.Trigger, c.AlertID, c.Revision, c.EventID})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Validate 核对阶段、身份、时间和结果，损坏记录不能当作成功重放。
func (r Record) Validate() error {
	if r.Cause.Validate() != nil || r.ID != r.Cause.ID() || r.StartedAt.IsZero() {
		return policy.ErrInvalid
	}
	if r.State == "pending" {
		if r.Result != nil || r.FinishedAt != nil {
			return policy.ErrInvalid
		}
		return nil
	}
	if r.State != "completed" || r.Result == nil || r.Result.validate(r.Cause) != nil || r.FinishedAt == nil || r.FinishedAt.Before(r.StartedAt) {
		return policy.ErrInvalid
	}
	return nil
}

// Documents 必须提供精确读取、创建/版本 CAS 及 ID 严格递增的租户前缀分页。
type Documents interface {
	Get(context.Context, string, string) (json.RawMessage, string, error)
	Put(context.Context, string, string, string, json.RawMessage) error
	List(context.Context, string, string, string, int) ([]json.RawMessage, error)
}

// Recorder 在调用方持有正式 tenant/source/fingerprint lease 时运行清理，所有步骤共用其取消上下文。
// Redis 与持久记录无跨系统事务；pending/PreviousUnconfirmed 明确保留这个可观察边界。
type Recorder interface {
	Run(context.Context, Cause, func(context.Context) (Result, error)) error
}

// Journal 使用本部署的持久文档保存有界诊断，每条最多 2 MiB；不保存 payload、凭据和原始异常。
type Journal struct {
	docs Documents
	now  func() time.Time
}

// NewJournal 不探测外部依赖，调用者持有并关闭 Documents。
func NewJournal(d Documents, now func() time.Time) (*Journal, error) {
	if d == nil || now == nil {
		return nil, policy.ErrInvalid
	}
	return &Journal{d, now}, nil
}

func prefix(tenant string) string { return base64.RawURLEncoding.EncodeToString([]byte(tenant)) + ":" }

func hashID(id string) bool {
	b, e := hex.DecodeString(id)
	return e == nil && len(b) == 32 && hex.EncodeToString(b) == id
}

// Get 只读取指定租户及清理身份；返回的 CAS token 不参与清理业务身份。
func (j *Journal) Get(ctx context.Context, tenant, id string) (Record, string, error) {
	if domain.ValidateIdentityPart("tenant", tenant, 64) != nil || !hashID(id) {
		return Record{}, "", policy.ErrInvalid
	}
	b, v, e := j.docs.Get(ctx, "suppression_cleanups", prefix(tenant)+id)
	if e != nil {
		return Record{}, "", e
	}
	var r Record
	if len(b) > MaxRecordBytes || json.Unmarshal(b, &r) != nil || r.Validate() != nil || v == "" {
		return Record{}, "", policy.ErrInvalid
	}
	if r.Cause.TenantID != tenant || r.ID != id {
		return Record{}, "", policy.ErrAccess
	}
	return r, v, nil
}

// Run 先保存 intent 再清理；核心记录失败返回重试，普通 Redis 故障由执行器记录 unavailable 后完成。
// 已完成的相同终态重投不再次触碰 Redis。调用者必须持有上述 fingerprint lease，不能并发执行同一 Cause。
func (j *Journal) Run(ctx context.Context, c Cause, run func(context.Context) (Result, error)) error {
	if ctx == nil || c.Validate() != nil || run == nil {
		return policy.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	r, v, e := j.Get(ctx, c.TenantID, c.ID())
	if errors.Is(e, policy.ErrNotFound) {
		r = Record{ID: c.ID(), Cause: c, State: "pending", StartedAt: j.now().Round(0).UTC()}
		v = ""
	} else if e != nil {
		return e
	} else {
		if !reflect.DeepEqual(c, r.Cause) {
			return policy.ErrConflict
		}
		if r.State == "completed" {
			return nil
		}
		r.PreviousUnconfirmed = true
	}
	if r.Validate() != nil {
		return policy.ErrInvalid
	}
	if e = j.save(ctx, r, v); e != nil {
		return e
	}
	// 读取实际 CAS；确认刚保存的 intent 身份，不能拿其他调用的版本完成记录。
	saved, v, e := j.Get(ctx, c.TenantID, r.ID)
	if e != nil {
		return e
	}
	if !reflect.DeepEqual(saved, r) {
		return policy.ErrConflict
	}
	result, e := run(ctx)
	if e != nil {
		return e
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	if result.validate(c) != nil {
		return policy.ErrInvalid
	}
	finished := j.now().Round(0).UTC()
	if finished.Before(r.StartedAt) {
		finished = r.StartedAt
	}
	r.State = "completed"
	r.Result = &result
	r.FinishedAt = &finished
	return j.save(ctx, r, v)
}

func (j *Journal) save(ctx context.Context, r Record, version string) error {
	if r.Validate() != nil {
		return policy.ErrInvalid
	}
	raw, err := json.Marshal(r)
	if err != nil || len(raw) > MaxRecordBytes {
		return policy.ErrInvalid
	}
	return j.docs.Put(ctx, "suppression_cleanups", prefix(r.Cause.TenantID)+r.ID, version, raw)
}

// Page 按稳定身份返回历史；Next 只表示还有未扫描位置，不表示时间排序或事务快照。
type Page struct {
	// Items 只包含请求租户内的独立记录。
	Items []Record `json:"items"`
	// Next 为本页最后扫描的稳定 ID，满页时返回。
	Next string `json:"next"`
}

// List 仅接受一个租户前缀，最多 16 条；空页和首次不存在不能推断从未发生清理。
func (j *Journal) List(ctx context.Context, tenant, after string, limit int) (Page, error) {
	if domain.ValidateIdentityPart("tenant", tenant, 64) != nil || (after != "" && !hashID(after)) || limit < 1 || limit > 16 {
		return Page{}, policy.ErrInvalid
	}
	p := prefix(tenant)
	cursor := ""
	if after != "" {
		cursor = p + after
	}
	rows, e := j.docs.List(ctx, "suppression_cleanups", p, cursor, limit)
	if e != nil {
		return Page{}, e
	}
	if len(rows) > limit {
		return Page{}, policy.ErrInvalid
	}
	page := Page{Items: []Record{}}
	last := after
	for _, raw := range rows {
		var r Record
		if len(raw) > MaxRecordBytes || json.Unmarshal(raw, &r) != nil || r.Validate() != nil || r.ID <= last {
			return Page{}, policy.ErrInvalid
		}
		if r.Cause.TenantID != tenant {
			return Page{}, policy.ErrAccess
		}
		last = r.ID
		page.Items = append(page.Items, r)
	}
	if len(rows) == limit {
		page.Next = last
	}
	return page, nil
}

// IdentityMatches 供管理读取复核明确身份；空筛选不扩大租户范围。
func IdentityMatches(r Record, source, fingerprint, owner, event string) bool {
	return (source == "" || r.Cause.SourceID == source) && (fingerprint == "" || r.Cause.Fingerprint == fingerprint) && (owner == "" || r.Cause.AlertID == owner) && (event == "" || r.Cause.EventID == event)
}

// ValidFilter 防止查询条件越过业务身份预算；fingerprint/实体身份保持原有编码，不做 trim 改写。
func ValidFilter(source, fingerprint, owner, event string) bool {
	return (source == "" || domain.ValidateIdentityPart("source", source, 32) == nil) && len(fingerprint) <= 128 && len(owner) <= domain.EntityIDMaxBytes && len(event) <= domain.EntityIDMaxBytes && !strings.ContainsAny(source, "\r\n")
}
