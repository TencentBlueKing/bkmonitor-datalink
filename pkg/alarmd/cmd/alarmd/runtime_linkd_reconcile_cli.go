// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obchannel"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
)

// linkdReconcileTimeout bounds the drawer's Linkd read: far inside the front
// read's 30-second request timeout, so a Linkd that does not answer is said
// as such rather than as the page's own timeout.
const linkdReconcileTimeout = 10 * time.Second

// linkdReconcileReader is what the drawer reads through: the Console's
// binding, for the event source, and its reconciliation of one strategy.
type linkdReconcileReader interface {
	Binding(context.Context) (openalerts.TargetBinding, error)
	Reconcile(context.Context, openalerts.StrategyKey) (openalerts.Reconciliation, error)
}

// linkdReconcileAnswer is the drawer's Linkd block: counts in the server's
// words, and the link keys -- Linkd's alert ids and the event source -- for
// the page to link with. No fingerprint and no alert content.
type linkdReconcileAnswer struct {
	StrategyID string `json:"strategy_id"`
	// Answered is false when Linkd did not answer within the bound; Counts
	// is then absent and the page shows unknown, never zero.
	Answered      bool                  `json:"answered"`
	ReadAt        time.Time             `json:"read_at"`
	Counts        *linkdReconcileCounts `json:"counts,omitempty"`
	LinkdAlertIDs []string              `json:"linkd_alert_ids"`
	// AlertIDsCut says the id list stopped at its share of the response
	// budget; the counts are whole.
	AlertIDsCut   bool   `json:"alert_ids_cut,omitempty"`
	EventSourceID string `json:"event_source_id,omitempty"`
	Line          string `json:"line"`
}

// linkdReconcileCounts compares alarmd's open set with Linkd's active alerts
// for one strategy, by fingerprint.
type linkdReconcileCounts struct {
	AlarmdOpen  int `json:"alarmd_open"`
	LinkdActive int `json:"linkd_active"`
	Both        int `json:"both"`
	OnlyAlarmd  int `json:"only_alarmd"`
	OnlyLinkd   int `json:"only_linkd"`
}

// linkdReconcileNotAnswered is the page's words for a Linkd past the bound.
const linkdReconcileNotAnswered = "Linkd 未应答"

// cliLinkdReconcileOperation compares one strategy's open alerts with Linkd's,
// through the Console this process already holds, within linkdReconcileTimeout.
// Reads are not gated: one costs a Console reconciliation (1.3-2.3 s measured),
// nothing against the link's own load, and the channel refuses no read for
// another being in flight (10-16 ruling).
func cliLinkdReconcileOperation(console linkdReconcileReader) obchannel.Operation {
	return obchannel.Operation{ID: "strategy.linkd_reconcile",
		Summary: "策略详情的“与 Linkd 对账”：按策略 ID 与租户（strategy.board 行里的 tenant，原样传回）按指纹比较 alarmd 未恢复告警与 Linkd 活跃告警，服务端写好计数——alarmd 未恢复、Linkd 活跃、两边都有、只在 alarmd、只在 Linkd；" +
			"链接键只给 Linkd 那边有的告警 ID 与事件源 ID，不给指纹与告警内容。" +
			"Linkd 10 秒内没应答答 answered=false、不给计数（页面显示未知，不是 0）。",
		EvidenceScope: "linkd_alerts", ExecutionTimeout: linkdReconcileTimeout + 2*time.Second,
		Fields: map[string]obchannel.Field{
			"strategy_id": {Type: "string", Description: "策略 ID。", MinLength: 1, MaxLength: 20, Pattern: "^[1-9][0-9]*$", Source: "strategy.board 的行"},
			"tenant":      {Type: "string", Description: "策略所属租户，原样取 strategy.board 行或 strategy.view 结论里的 tenant；没有默认值，空的 tenant 表示未知，不能对账。", MinLength: 1, MaxLength: 256, Pattern: "^[^\\x00-\\x1f\\x7f]+$", Source: "strategy.board rows[].tenant 或 strategy.view conclusion.tenant"},
		},
		Required:     []string{"strategy_id", "tenant"},
		Examples:     []obchannel.Params{{"strategy_id": "77", "tenant": "system"}},
		OutputSchema: obchannel.SchemaOf(linkdReconcileAnswer{}),
		Limits: map[string]any{"redis_commands": 0, "console_calls": 2,
			"timeout_ms": linkdReconcileTimeout.Milliseconds()},
		Availability: func() obchannel.Availability {
			if console == nil {
				return obchannel.Availability{Reason: "linkd_console_not_configured: this process reads no linkd Console"}
			}
			return obchannel.Availability{Available: true}
		},
		Run: func(ctx context.Context, p obchannel.Params) obchannel.Outcome {
			// The ids take at most half the response budget: the rest is the
			// envelope and the counts, which are whole either way.
			return readLinkdReconcile(ctx, console, p, time.Now, linkdReconcileTimeout, obchannel.MaxResponseBytes()/2)
		}}
}

func readLinkdReconcile(ctx context.Context, console linkdReconcileReader, p obchannel.Params, now func() time.Time, bound time.Duration, idBudget int) obchannel.Outcome {
	tenant := p.String("tenant")
	id := p.String("strategy_id")
	answer := linkdReconcileAnswer{StrategyID: id, ReadAt: now().UTC(), LinkdAlertIDs: []string{}}
	readCtx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	notAnswered := func() obchannel.Outcome {
		answer.Line = linkdReconcileNotAnswered
		return obchannel.Outcome{Value: answer, Summary: answer.Line, Complete: false,
			Limitations: []string{"Linkd did not answer within " + bound.String() + "; the counts are unknown, not zero."}}
	}
	binding, err := console.Binding(readCtx)
	if errors.Is(err, context.DeadlineExceeded) || readCtx.Err() != nil && err != nil {
		return notAnswered()
	}
	if err != nil {
		return linkdAlertsFailure(err)
	}
	answer.EventSourceID = binding.EventSourceID
	result, err := console.Reconcile(readCtx, openalerts.StrategyKey{TenantID: tenant, StrategyID: id})
	if errors.Is(err, context.DeadlineExceeded) || readCtx.Err() != nil && err != nil {
		return notAnswered()
	}
	if err != nil {
		return linkdAlertsFailure(err)
	}
	answer.Answered = true
	both, onlyLinkd, onlyAlarmd := len(result.Members), len(result.Missing), len(result.Suppressed)
	answer.Counts = &linkdReconcileCounts{AlarmdOpen: both + onlyAlarmd, LinkdActive: both + onlyLinkd, Both: both, OnlyAlarmd: onlyAlarmd, OnlyLinkd: onlyLinkd}
	spent := 0
	seen := map[string]bool{}
	for _, alert := range result.Alerts {
		if alert.AlertID == "" || seen[alert.AlertID] {
			continue
		}
		if spent += len(alert.AlertID) + 3; idBudget > 0 && spent > idBudget {
			answer.AlertIDsCut = true
			break
		}
		seen[alert.AlertID] = true
		answer.LinkdAlertIDs = append(answer.LinkdAlertIDs, alert.AlertID)
	}
	counts := answer.Counts
	answer.Line = fmt.Sprintf("alarmd 未恢复 %d、Linkd 活跃 %d；两边都有 %d、只在 alarmd %d、只在 Linkd %d",
		counts.AlarmdOpen, counts.LinkdActive, counts.Both, counts.OnlyAlarmd, counts.OnlyLinkd)
	if result.Truncated {
		answer.Line += "（Linkd 回答不完整或超过 10000 行 / 5000 条活跃告警，只数到已收到的部分）"
	}
	out := obchannel.Outcome{Value: answer, Summary: answer.Line, Complete: !answer.AlertIDsCut && !result.Truncated}
	if result.Truncated {
		out.Limitations = append(out.Limitations, "Linkd's answer was incomplete or over 10000 rows / 5000 active alerts: the counts are of what was received, a lower bound.")
	} else if answer.AlertIDsCut {
		out.Limitations = append(out.Limitations, "Linkd alert ids stop at their share of the response budget; the counts are whole.")
	}
	return out
}
