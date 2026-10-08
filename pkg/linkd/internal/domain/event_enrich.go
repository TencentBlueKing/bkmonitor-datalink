// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package domain

import (
	"cmp"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"time"
)

// EvaluationEnrich 保存某一标准等级的有序处理器结果；同一 Event 的等级唯一。
// Data 使用与 Alert 相同的 processors envelope，创建 Alert 时直接复制对应结果。
type EvaluationEnrich struct {
	Severity string       `json:"severity"`
	Status   EnrichStatus `json:"status"`
	Data     JSONObject   `json:"data"`
}

// EventEnrichData 保留全部 evaluation 的结果，避免将多等级 Event 压成一个等级。
type EventEnrichData struct {
	Evaluations []EvaluationEnrich `json:"evaluations"`
}

// EventEnrichment 是一次 Event 丰富的完整提交。完成后，包括失败结果在内均不可覆盖。
// EnrichedAt 是提交时间，不参与业务身份；ConfigDigest 标识实际执行的不可变配置。
type EventEnrichment struct {
	EnrichStatus       EnrichStatus    `json:"enrich_status"`
	Enrich             EventEnrichData `json:"enrich"`
	EnrichedAt         *time.Time      `json:"enriched_at,omitempty"`
	EnrichConfigDigest string          `json:"enrich_config_digest,omitempty"`
}

// Clone 返回不共享结果、时间指针和 JSON 字节的副本。
func (e EventEnrichment) Clone() EventEnrichment {
	e.Enrich.Evaluations = slices.Clone(e.Enrich.Evaluations)
	for i := range e.Enrich.Evaluations {
		e.Enrich.Evaluations[i].Data = e.Enrich.Evaluations[i].Data.Clone()
	}
	if e.EnrichedAt != nil {
		at := *e.EnrichedAt
		e.EnrichedAt = &at
	}
	return e
}

// Normalize 规范化构造阶段的 pending 零值，并验证所有等级均有且仅有一个完成结果。
func (e EventEnrichment) Normalize(evaluations []EventEvaluation) (EventEnrichment, error) {
	if len(e.Enrich.Evaluations) > MaxEventEvaluations {
		return EventEnrichment{}, fmt.Errorf("event enrich exceeds evaluation limit")
	}
	e = e.Clone()
	if e.EnrichStatus == "" {
		e.EnrichStatus = EnrichStatusPending
	}
	if e.EnrichedAt != nil {
		at := normalizeTime(*e.EnrichedAt)
		e.EnrichedAt = &at
	}
	slices.SortFunc(e.Enrich.Evaluations, func(a, b EvaluationEnrich) int { return cmp.Compare(a.Severity, b.Severity) })
	for i := range e.Enrich.Evaluations {
		data, err := e.Enrich.Evaluations[i].Data.Normalize()
		if err != nil {
			return EventEnrichment{}, fmt.Errorf("event enrich data: %w", err)
		}
		e.Enrich.Evaluations[i].Data = data
	}
	return e, e.Validate(evaluations)
}

// Validate 校验完成性、聚合状态和整条 Event 的载荷预算。
func (e EventEnrichment) Validate(evaluations []EventEvaluation) error {
	if e.EnrichStatus == "" || e.EnrichStatus == EnrichStatusPending {
		if len(e.Enrich.Evaluations) != 0 || e.EnrichedAt != nil || e.EnrichConfigDigest != "" {
			return fmt.Errorf("pending event enrich must be empty")
		}
		return nil
	}
	if !e.EnrichStatus.Valid() || e.EnrichedAt == nil || e.EnrichedAt.IsZero() || len(e.EnrichConfigDigest) == 0 || len(e.EnrichConfigDigest) > 128 {
		return fmt.Errorf("completed event enrich requires valid status, enriched_at and config digest")
	}
	if len(e.Enrich.Evaluations) != len(evaluations) || len(evaluations) == 0 || len(evaluations) > MaxEventEvaluations {
		return fmt.Errorf("event enrich must cover every evaluation")
	}
	expected := make(map[string]bool, len(evaluations))
	for _, evaluation := range evaluations {
		expected[evaluation.Severity] = true
	}
	statuses := make([]EnrichStatus, 0, len(evaluations))
	for _, result := range e.Enrich.Evaluations {
		if !expected[result.Severity] {
			return fmt.Errorf("event enrich contains unexpected or duplicate severity")
		}
		delete(expected, result.Severity)
		if result.Status == EnrichStatusPending || !result.Status.Valid() {
			return fmt.Errorf("event enrich evaluation must be complete")
		}
		if _, err := result.Data.Normalize(); err != nil {
			return err
		}
		if err := ValidateEnrichPayload(result.Status, result.Data); err != nil {
			return err
		}
		statuses = append(statuses, result.Status)
	}
	if e.EnrichStatus != AggregateEnrichStatus(statuses) {
		return fmt.Errorf("event enrich status differs from evaluation aggregate")
	}
	encoded, err := json.Marshal(e.Enrich)
	if err != nil {
		return err
	}
	if len(encoded) > 1<<20 {
		return fmt.Errorf("event enrich exceeds 1 MiB")
	}
	return nil
}

// AggregateEnrichStatus 汇总已完成步骤或等级；空链成功，全部跳过为 skipped。
func AggregateEnrichStatus(statuses []EnrichStatus) EnrichStatus {
	succeeded, failed, skipped := 0, 0, 0
	for _, status := range statuses {
		switch status {
		case EnrichStatusSucceeded:
			succeeded++
		case EnrichStatusFailed:
			failed++
		case EnrichStatusSkipped:
			skipped++
		}
	}
	switch {
	case len(statuses) == 0:
		return EnrichStatusSucceeded
	case skipped == len(statuses):
		return EnrichStatusSkipped
	case succeeded == len(statuses)-skipped:
		return EnrichStatusSucceeded
	case failed == len(statuses)-skipped:
		return EnrichStatusFailed
	default:
		return EnrichStatusPartial
	}
}

// ForSeverity 返回指定 evaluation 的隔离结果；缺失时不能任选其他等级兜底。
func (e EventEnrichment) ForSeverity(severity string) (EvaluationEnrich, bool) {
	for _, result := range e.Enrich.Evaluations {
		if result.Severity == severity {
			result.Data = result.Data.Clone()
			return result, true
		}
	}
	return EvaluationEnrich{}, false
}

// WithEnrichment 只追加完整丰富结果；相同结果可幂等核对，不允许重算覆盖。
func (e Event) WithEnrichment(result EventEnrichment) (Event, error) {
	normalized, err := result.Normalize(e.Evaluations)
	if err != nil {
		return Event{}, err
	}
	if normalized.EnrichStatus == EnrichStatusPending {
		return Event{}, fmt.Errorf("event enrich result must be complete")
	}
	if e.EnrichStatus != "" && e.EnrichStatus != EnrichStatusPending && !reflect.DeepEqual(e.EventEnrichment, normalized) {
		return Event{}, fmt.Errorf("event enrich is immutable after completion")
	}
	e = e.Clone()
	e.EventEnrichment = normalized
	return e.Normalize()
}
