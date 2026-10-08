// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package telemetry

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// ObserveSuppression 记录固定分类的策略结果；不接受租户、策略、Event/Alert ID 作为指标标签。
// 跳过原因按资源/输入/其他三类聚合，具体原因在 Event 裁决和脱敏日志中保留。
func (r *Runtime) ObserveSuppression(ctx context.Context, scheme, outcome, reason string) {
	if r == nil || r.metrics == nil {
		return
	}
	switch scheme {
	case "match", "clip", "aggregation", "cleanup":
	default:
		scheme = "unknown"
	}
	switch outcome {
	case "passed", "suppressed", "skipped", "not_matched", "reserved", "released":
	default:
		outcome = "unknown"
	}
	category := "none"
	switch reason {
	case "":
	case "clip_below_threshold", "aggregation_suppressed":
		category = "policy"
	case "redis_unavailable", "owner_bind_failed", "release_unavailable", "target_reader_unavailable", "business_scope_unavailable", "target_resolution_failed", "aggregation_unavailable", "owner_read_failed", "aggregation_busy", "candidate_release_failed", "owner_registration_lost":
		category = "dependency"
	case "event_view_unavailable", "business_field_unavailable", "business_field_invalid", "instance_field_unavailable", "condition_unavailable", "group_field_unavailable", "invalid_group_value", "target_identity_invalid":
		category = "input"
	default:
		category = "other"
	}
	r.metrics.suppressionDecisions.Add(ctx, 1, metric.WithAttributes(attribute.String("linkd.scheme", scheme), attribute.String("linkd.outcome", outcome), attribute.String("linkd.reason", category)))
}

// ObserveShield 将屏蔽绑定、解除及依赖跳过按固定类别统计，具体定位只在受控日志和 Event 中保存。
func (r *Runtime) ObserveShield(ctx context.Context, outcome, reason string) {
	if r == nil || r.metrics == nil {
		return
	}
	switch outcome {
	case "bound", "retained", "released", "not_matched", "skipped", "main_reserved":
	default:
		outcome = "unknown"
	}
	category := "other"
	switch reason {
	case "":
		category = "none"
	case "policy_inactive", "policy_disabled", "shield_matched", "inactive", "dependency_matched", "dependency_not_matched", "dependency_self", "dependency_main_missing", "dependency_main_ended", "dependency_no_longer_matches", "timer_does_not_rebind":
		category = "policy"
	case "release_unavailable", "binding_event_unavailable", "policy_load_failed", "dependency_main_unavailable", "dependency_child_unavailable", "main_registration_lost", "main_cleanup_failed", "business_scope_unavailable", "target_resolution_failed", "evaluation_unavailable":
		category = "dependency"
	case "event_view_unavailable", "condition_unavailable", "invalid_group_value", "business_field_unavailable", "instance_field_unavailable", "policy_tag_budget", "activation_unavailable":
		category = "input"
	}
	r.metrics.shieldDecisions.Add(ctx, 1, metric.WithAttributes(attribute.String("linkd.outcome", outcome), attribute.String("linkd.reason", category)))
}

// ObserveMerge 聚合入窗诊断；具体窗口和策略身份只在事件结果及受控日志中保存。
func (r *Runtime) ObserveMerge(ctx context.Context, outcome, reason string) {
	if r == nil || r.metrics == nil {
		return
	}
	switch outcome {
	case "joined", "retained", "not_matched", "skipped":
	default:
		outcome = "unknown"
	}
	category := "other"
	switch reason {
	case "":
		category = "none"
	case "inactive", "conditions_not_matched", "merge_waiting", "merge_window_decided":
		category = "policy"
	case "release_unavailable", "evaluation_unavailable", "merge_state_unavailable", "merge_join_unavailable", "merge_window_unavailable", "merge_confirmation_lost", "business_scope_unavailable", "target_resolution_failed":
		category = "dependency"
	case "merge_wait_budget", "event_view_unavailable", "invalid_group_value", "group_field_unavailable", "condition_unavailable":
		category = "input"
	}
	r.metrics.mergeDecisions.Add(ctx, 1, metric.WithAttributes(attribute.String("linkd.outcome", outcome), attribute.String("linkd.reason", category)))
}

// ObserveShieldHint 只保存固定的提示运行结果，业务身份留在受控日志与诊断中。
func (r *Runtime) ObserveShieldHint(ctx context.Context, outcome string) {
	if r == nil || r.metrics == nil {
		return
	}
	switch outcome {
	case "published", "no_subscriber", "publish_failed", "subscribed", "invalid", "queued", "coalesced", "dropped", "page_succeeded", "page_failed", "subscription_failed":
	default:
		outcome = "unknown"
	}
	r.metrics.shieldHints.Add(ctx, 1, metric.WithAttributes(attribute.String("linkd.outcome", outcome)))
}

// ObservePolicyMatch 只记录正式执行的单次求值耗时，不采集策略身份。
func (r *Runtime) ObservePolicyMatch(ctx context.Context, kind, outcome string, elapsed time.Duration) {
	if r == nil || r.metrics == nil || elapsed < 0 {
		return
	}
	if kind != "suppression" && kind != "shield" && kind != "merge" {
		kind = "unknown"
	}
	if outcome != "matched" && outcome != "not_matched" && outcome != "unavailable" {
		outcome = "unknown"
	}
	r.metrics.policyMatchDuration.Record(ctx, elapsed.Seconds(), metric.WithAttributes(attribute.String("linkd.policy.kind", kind), attribute.String("linkd.outcome", outcome)))
}

// ObservePolicyDelay 记录到期检查延迟，提前检查不记录，复查可重复观察同一业务对象。
func (r *Runtime) ObservePolicyDelay(ctx context.Context, kind string, elapsed time.Duration) {
	if r == nil || r.metrics == nil || elapsed < 0 {
		return
	}
	if kind != "shield" && kind != "merge" {
		kind = "unknown"
	}
	r.metrics.policyCheckDelay.Record(ctx, elapsed.Seconds(), metric.WithAttributes(attribute.String("linkd.policy.kind", kind)))
}

// ObservePolicySample 将统计本身的故障与业务策略故障分开。
func (r *Runtime) ObservePolicySample(ctx context.Context, outcome string) {
	if r == nil || r.metrics == nil {
		return
	}
	if outcome != "recorded" && outcome != "failed" && outcome != "dropped" {
		outcome = "unknown"
	}
	r.metrics.policySamples.Add(ctx, 1, metric.WithAttributes(attribute.String("linkd.outcome", outcome)))
}

// ObservePolicyStateInit 不把完整缓存丢失臆断成故障恢复；created包括首次、正常过期和完整丢失后的创建。
func (r *Runtime) ObservePolicyStateInit(ctx context.Context, scheme, outcome string) {
	if r == nil || r.metrics == nil {
		return
	}
	if scheme != "clip" && scheme != "aggregation" && scheme != "merge" {
		scheme = "unknown"
	}
	if outcome != "created" && outcome != "repaired" {
		outcome = "unknown"
	}
	r.metrics.policyStateInit.Add(ctx, 1, metric.WithAttributes(attribute.String("linkd.scheme", scheme), attribute.String("linkd.outcome", outcome)))
}
