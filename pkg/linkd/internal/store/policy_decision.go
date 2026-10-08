// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package store

import (
	"encoding/hex"
	"fmt"
	"slices"

	"linkd/internal/domain"
)

// PolicyDecision 保存事件策略诊断；业务终态仍由 Evaluations/State 表达。
// 与 Plan 一起冻结并在最终 CAS 时复制，不能在重投时按当前配置覆盖。
type PolicyDecision struct {
	Merge       *MergeDecision       `json:"merge,omitempty"`
	Shield      *ShieldDecision      `json:"shield,omitempty"`
	Suppression *SuppressionDecision `json:"suppression,omitempty"`
}

// ShieldDecision 记录屏蔽匹配与解除判断；实际关系保存在 Alert.shield。
type ShieldDecision struct {
	Severity string       `json:"severity"`
	Steps    []ShieldStep `json:"steps,omitempty"`
}

// ShieldStep 的 FromBinding 表示使用已建立关系的冻结版本，而不是本 Event 的新策略版本。
type ShieldStep struct {
	Main        *domain.DependencyMain `json:"main,omitempty"`
	Policy      PolicyReleaseRef       `json:"policy"`
	Outcome     string                 `json:"outcome"`
	ReasonCode  string                 `json:"reason_code,omitempty"`
	BindingID   string                 `json:"binding_id,omitempty"`
	FromBinding bool                   `json:"from_binding,omitempty"`
}

func (s *ShieldDecision) Validate() error {
	if s == nil {
		return nil
	}
	if s.Severity == "" || len(s.Severity) > 32 || len(s.Steps) > 272 {
		return fmt.Errorf("invalid shield decision")
	}
	for _, step := range s.Steps {
		if err := step.Policy.Validate(); err != nil {
			return err
		}
		if step.Policy.Kind != "shield" || len(step.ReasonCode) > 80 {
			return fmt.Errorf("invalid shield reference/reason")
		}
		switch step.Outcome {
		case "bound", "retained", "released", "not_matched", "skipped", "main_reserved":
		default:
			return fmt.Errorf("invalid shield outcome")
		}
		if step.BindingID != "" {
			raw, err := hex.DecodeString(step.BindingID)
			if err != nil || len(raw) != 32 {
				return fmt.Errorf("invalid shield binding id")
			}
		}
		if step.Main != nil {
			if err := step.Main.Validate(); err != nil {
				return err
			}
		}
		if (step.Outcome == "main_reserved") != (step.Main != nil) {
			return fmt.Errorf("main reservation result requires reference")
		}
		if step.FromBinding && step.BindingID == "" {
			return fmt.Errorf("existing shield requires binding id")
		}
	}
	return nil
}

// SuppressionDecision 记录新告警门槛，活动 Alert 绕过时不执行任何计数或占位。
type SuppressionDecision struct {
	BypassReason  string                  `json:"bypass_reason,omitempty"`
	ActiveAlertID string                  `json:"active_alert_id,omitempty"`
	Evaluations   []SuppressionEvaluation `json:"evaluations,omitempty"`
}

// SuppressionEvaluation 只记录实际参加新告警候选选择的等级，按严重程度顺序排列。
type SuppressionEvaluation struct {
	Severity       string            `json:"severity"`
	Suppressed     bool              `json:"suppressed"`
	ReasonCode     string            `json:"reason_code,omitempty"`
	RelatedAlertID string            `json:"related_alert_id,omitempty"`
	Steps          []SuppressionStep `json:"steps,omitempty"`
}

// SuppressionStep 不保存原始字段值或后端错误；引用、结果和计数足以诊断及绑定 owner。
type SuppressionStep struct {
	Window            *SuppressionWindow `json:"window,omitempty"`
	Policy            PolicyReleaseRef   `json:"policy"`
	Scheme            string             `json:"scheme"`
	Outcome           string             `json:"outcome"`
	ReasonCode        string             `json:"reason_code,omitempty"`
	Count             int                `json:"count,omitempty"`
	Threshold         int                `json:"threshold,omitempty"`
	DurationSeconds   int64              `json:"duration_seconds,omitempty"`
	EvaluatedAtMillis int64              `json:"evaluated_at_ms,omitempty"`
	CounterID         string             `json:"counter_id,omitempty"`
	Epoch             string             `json:"epoch,omitempty"`
}

// SuppressionWindow 保存候选或关联主的固定窗口引用，不保存 Redis 私有键或原始分组值。
type SuppressionWindow struct {
	WindowID         string `json:"window_id"`
	GroupKey         string `json:"group_key"`
	Epoch            string `json:"epoch"`
	OwnerAlertID     string `json:"owner_alert_id"`
	OwnerEventID     string `json:"owner_event_id"`
	OwnerSourceID    string `json:"owner_source_id"`
	OwnerFingerprint string `json:"owner_fingerprint"`
	StartedAtMillis  int64  `json:"started_at_ms"`
	ExpiresAtMillis  int64  `json:"expires_at_ms"`
}

func (w *SuppressionWindow) validate() error {
	if w == nil {
		return nil
	}
	for _, value := range []string{w.WindowID, w.GroupKey} {
		raw, err := hex.DecodeString(value)
		if err != nil || len(raw) != 32 {
			return fmt.Errorf("invalid aggregation identity")
		}
	}
	if w.Epoch == "" || w.Epoch != w.OwnerEventID || len(w.Epoch) > domain.EntityIDMaxBytes || w.OwnerAlertID == "" || len(w.OwnerAlertID) > domain.EntityIDMaxBytes || w.OwnerFingerprint == "" || len(w.OwnerFingerprint) > 128 || w.StartedAtMillis < 0 || w.StartedAtMillis > 1<<46 || w.ExpiresAtMillis <= w.StartedAtMillis || w.ExpiresAtMillis-w.StartedAtMillis > 30*24*3600*1000 {
		return fmt.Errorf("invalid aggregation window")
	}
	return domain.ValidateIdentityPart("owner source", w.OwnerSourceID, 32)
}

// Clone 隔离每个 evaluation 的诊断切片，供 Repository 与计划重试使用。
func (d *PolicyDecision) Clone() *PolicyDecision {
	if d == nil {
		return nil
	}
	result := *d
	result.Merge = d.Merge.Clone()
	if d.Shield != nil {
		v := *d.Shield
		v.Steps = slices.Clone(v.Steps)
		for i := range v.Steps {
			if v.Steps[i].Main != nil {
				m := *v.Steps[i].Main
				v.Steps[i].Main = &m
			}
		}
		result.Shield = &v
	}
	if d.Suppression != nil {
		suppression := *d.Suppression
		suppression.Evaluations = slices.Clone(d.Suppression.Evaluations)
		for i := range suppression.Evaluations {
			suppression.Evaluations[i].Steps = slices.Clone(suppression.Evaluations[i].Steps)
			for j := range suppression.Evaluations[i].Steps {
				step := &suppression.Evaluations[i].Steps[j]
				if step.Window != nil {
					window := *step.Window
					step.Window = &window
				}
			}
		}
		result.Suppression = &suppression
	}
	return &result
}

// Validate 限制诊断体积，拒绝把故障跳过写成正常抑制或将无计数结果用于 owner 绑定。
func (d *PolicyDecision) Validate() error {
	if d == nil {
		return nil
	}
	if err := d.Merge.Validate(); err != nil {
		return err
	}
	if err := d.Shield.Validate(); err != nil {
		return err
	}
	s := d.Suppression
	if s == nil {
		if d.Shield != nil || d.Merge != nil {
			return nil
		}
		return fmt.Errorf("empty policy decision")
	}
	if s.BypassReason != "" {
		if s.BypassReason != "active_alert" && s.BypassReason != "no_trigger" {
			return fmt.Errorf("invalid suppression bypass")
		}
		if len(s.Evaluations) != 0 || (s.BypassReason == "active_alert") != (s.ActiveAlertID != "") || len(s.ActiveAlertID) > domain.EntityIDMaxBytes {
			return fmt.Errorf("invalid suppression bypass association")
		}
		return nil
	}
	if len(s.Evaluations) == 0 || len(s.Evaluations) > domain.MaxEventEvaluations || s.ActiveAlertID != "" {
		return fmt.Errorf("invalid suppression evaluations")
	}
	seen := map[string]bool{}
	for _, e := range s.Evaluations {
		if e.Severity == "" || seen[e.Severity] || len(e.Steps) > 512 || len(e.RelatedAlertID) > domain.EntityIDMaxBytes {
			return fmt.Errorf("invalid suppression evaluation")
		}
		seen[e.Severity] = true
		if e.Suppressed {
			if e.ReasonCode != "clip_below_threshold" && e.ReasonCode != "aggregation_suppressed" {
				return fmt.Errorf("invalid suppression reason")
			}
			if (e.ReasonCode == "aggregation_suppressed") != (e.RelatedAlertID != "") {
				return fmt.Errorf("invalid suppression owner")
			}
		} else if e.ReasonCode != "" || e.RelatedAlertID != "" {
			return fmt.Errorf("unsuppressed evaluation has suppression result")
		}
		matchedSuppression := false
		for _, step := range e.Steps {
			if err := step.Policy.Validate(); err != nil {
				return err
			}
			if step.Policy.Kind != "suppression" || (step.Scheme != "match" && step.Scheme != "clip" && step.Scheme != "aggregation") {
				return fmt.Errorf("invalid suppression step")
			}
			if step.Outcome != "passed" && step.Outcome != "suppressed" && step.Outcome != "skipped" && step.Outcome != "not_matched" && step.Outcome != "reserved" && step.Outcome != "released" {
				return fmt.Errorf("invalid suppression outcome")
			}
			if err := step.Window.validate(); err != nil {
				return err
			}
			if step.Window != nil {
				if step.Scheme != "aggregation" || (step.Outcome != "reserved" && step.Outcome != "released" && step.Outcome != "suppressed") || step.CounterID != "" {
					return fmt.Errorf("invalid aggregation result")
				}
			} else if step.Outcome == "reserved" || step.Outcome == "released" || (step.Scheme == "aggregation" && step.Outcome == "suppressed") {
				return fmt.Errorf("aggregation result requires window")
			}
			if len(step.ReasonCode) > 80 {
				return fmt.Errorf("invalid suppression diagnostic")
			}
			if step.Outcome == "suppressed" {
				if !e.Suppressed || step.ReasonCode != e.ReasonCode {
					return fmt.Errorf("suppression summary differs from step")
				}
				if step.Scheme == "aggregation" && (step.Window == nil || step.Window.OwnerAlertID != e.RelatedAlertID) {
					return fmt.Errorf("aggregation owner differs from summary")
				}
				matchedSuppression = true
			}
			if step.Scheme == "clip" && (step.Outcome == "passed" || step.Outcome == "suppressed") && step.CounterID == "" {
				return fmt.Errorf("clip outcome requires count")
			}
			if step.CounterID != "" {
				decoded, err := hex.DecodeString(step.CounterID)
				if err != nil || len(decoded) != 32 || step.Scheme != "clip" || (step.Outcome != "passed" && step.Outcome != "suppressed") || step.Count < 1 || step.Count > 10000 || step.Threshold < 1 || step.Threshold > 10000 || step.DurationSeconds < 1 || step.DurationSeconds > 30*24*3600 || step.EvaluatedAtMillis < 0 || step.EvaluatedAtMillis > 1<<46 || step.Epoch == "" || len(step.Epoch) > domain.EntityIDMaxBytes || (step.Outcome == "passed") != (step.Count >= step.Threshold) {
					return fmt.Errorf("invalid clip decision")
				}
			} else if step.Count != 0 || step.Threshold != 0 || step.DurationSeconds != 0 || step.EvaluatedAtMillis != 0 || step.Epoch != "" {
				return fmt.Errorf("clip fields require counter identity")
			}
		}
		if e.Suppressed && !matchedSuppression {
			return fmt.Errorf("suppression summary requires decision step")
		}
	}
	return nil
}

// ValidatePolicyDecision 将策略引用及等级绑定到已冻结的 Event 上下文，禁止跨计划拼接结果。
func ValidatePolicyDecision(event domain.Event, snapshot *PolicyContext, decision *PolicyDecision) error {
	if err := decision.Validate(); err != nil {
		return err
	}
	if decision == nil {
		return nil
	}
	if decision.Merge != nil {
		found := false
		for _, e := range event.Evaluations {
			if e.Severity == decision.Merge.Severity && e.Action == domain.EventActionTriggered {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("merge severity differs from event")
		}
		for _, step := range decision.Merge.Steps {
			if !step.FromWaiting && (snapshot == nil || !slices.Contains(snapshot.Releases, step.Policy)) {
				return fmt.Errorf("merge release differs from context")
			}
		}
	}
	if decision.Shield != nil {
		found := false
		for _, e := range event.Evaluations {
			if e.Severity == decision.Shield.Severity && e.Action == domain.EventActionTriggered {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("shield severity differs from event")
		}
		for _, step := range decision.Shield.Steps {
			if step.Main != nil && (step.Main.EventID != event.EventID || step.Main.EventSourceID != event.EventSourceID || step.Main.Fingerprint != event.Fingerprint || step.Main.Severity != decision.Shield.Severity) {
				return fmt.Errorf("dependency reservation differs from event")
			}

			if !step.FromBinding && (snapshot == nil || !slices.Contains(snapshot.Releases, step.Policy)) {
				return fmt.Errorf("shield release differs from frozen context")
			}
		}
	}
	if decision.Suppression == nil {
		return nil
	}
	for _, e := range decision.Suppression.Evaluations {
		found := false
		for _, evaluation := range event.Evaluations {
			if evaluation.Severity == e.Severity && evaluation.Action == domain.EventActionTriggered {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("suppression evaluation differs from event")
		}
		for _, step := range e.Steps {
			if snapshot == nil || !slices.Contains(snapshot.Releases, step.Policy) {
				return fmt.Errorf("suppression release differs from frozen context")
			}
		}
	}
	return nil
}
