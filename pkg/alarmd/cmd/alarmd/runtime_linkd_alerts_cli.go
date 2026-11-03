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
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdmanage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obchannel"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/redact"
)

// linkdAlertsTimeout bounds one linkd.alerts read: one search.
const linkdAlertsTimeout = 20 * time.Second

// linkdAlertsAnswer is linkd.alerts' answer: the link's native alerts the
// search found, after the channel's redaction, and the next page's cursor.
type linkdAlertsAnswer struct {
	Mode          string             `json:"mode"`
	TenantID      string             `json:"tenant_id"`
	EventSourceID string             `json:"event_source_id"`
	Alerts        []linkdAlertRecord `json:"alerts"`
	NextCursor    string             `json:"next_cursor,omitempty"`
}

// linkdAlertRecord is one of the link's alerts: what a reader compares,
// lifted from the record, then the record itself.
type linkdAlertRecord struct {
	AlertID         string          `json:"alert_id"`
	Status          string          `json:"status,omitempty"`
	Fingerprint     string          `json:"fingerprint,omitempty"`
	Severity        string          `json:"severity,omitempty"`
	StrategyID      string          `json:"strategy_id,omitempty"`
	StrategyVersion string          `json:"strategy_version,omitempty"`
	Record          json.RawMessage `json:"record"`
}

// linkdSearcher is the link's search (linkdmanage.Client).
type linkdSearcher interface {
	Search(ctx context.Context, tenantID string, query json.RawMessage, after string, limit int) (linkdmanage.SearchPage, error)
}

// linkdSearchOf is the client as linkd.alerts reads it: none when the
// process holds none, never an interface around a nil client.
func linkdSearchOf(client *linkdmanage.Client) linkdSearcher {
	if client == nil {
		return nil
	}
	return client
}

// cliLinkdAlertsOperation reads the link's alerts through its search, with
// the management token this process already signs, scoped to the tenant
// and this deployment's event source. Nil leaves the operation listed and
// unavailable with its reason.
func cliLinkdAlertsOperation(search linkdSearcher, source string) obchannel.Operation {
	text := func(description string, max int) obchannel.Field {
		return obchannel.Field{Type: "string", Description: description, MinLength: 1, MaxLength: max, Pattern: "^[^\\x00-\\x1f\\x7f]+$"}
	}
	minLimit, maxLimit := int64(1), int64(linkdmanage.MaxLimit)
	return obchannel.Operation{ID: "linkd.alerts",
		Summary: "经 linkd 控制面的告警检索（POST /api/v1/alerts/search）只读本部署事件源的告警，三选一问法：按策略（strategy_id）读这条策略的活动告警；" +
			"按告警（alert_id）或按指纹（fingerprint）读任何状态的告警记录（active/recovered/closed、策略标签与版本）。按策略读的是活动告警，空不等于已关闭。" +
			"不含生命周期流水与事件。",
		EvidenceScope: "linkd_alerts", ExecutionTimeout: linkdAlertsTimeout,
		Fields: map[string]obchannel.Field{
			"strategy_id": {Type: "string", Description: "按策略问：策略 ID。", MinLength: 1, MaxLength: 20, Pattern: "^[1-9][0-9]*$", Source: "strategy.get"},
			"alert_id":    text("按告警问：linkd 的告警 ID。", 1024),
			"fingerprint": {Type: "string", Description: "按指纹问：本部署发出的告警指纹（32 位小写十六进制）。", MinLength: 32, MaxLength: 32, Pattern: "^[0-9a-f]{32}$"},
			"tenant":      text("租户，默认 system。", 64),
			"limit":       {Type: "integer", Description: "每页条数，1..500，默认 100。", Minimum: &minLimit, Maximum: &maxLimit},
			"cursor":      text("上一页的 next_cursor。", 160),
		},
		OutputSchema: obchannel.SchemaOf(linkdAlertsAnswer{}),
		Limits:       map[string]any{"redis_commands": 0, "linkd_searches": 1, "redaction_contract": redact.Contract},
		Availability: func() obchannel.Availability {
			if search == nil {
				return obchannel.Availability{Reason: "linkd_control_plane_not_configured: this process names no linkd control plane"}
			}
			return obchannel.Availability{Available: true}
		},
		Validate: func(p obchannel.Params) error {
			asked := 0
			for _, key := range []string{"strategy_id", "alert_id", "fingerprint"} {
				if p.String(key) != "" {
					asked++
				}
			}
			if asked != 1 {
				return errors.New("name exactly one of strategy_id, alert_id or fingerprint")
			}
			return nil
		},
		Run: func(ctx context.Context, p obchannel.Params) obchannel.Outcome {
			return readLinkdAlerts(ctx, search, source, p)
		}}
}

func readLinkdAlerts(ctx context.Context, search linkdSearcher, source string, p obchannel.Params) obchannel.Outcome {
	tenant := p.String("tenant")
	if tenant == "" {
		tenant = "system"
	}
	type object = map[string]any
	clauses := []any{object{"term": object{"event_source_id": source}}}
	answer := linkdAlertsAnswer{TenantID: tenant, EventSourceID: source}
	switch {
	case p.String("strategy_id") != "":
		answer.Mode = "strategy"
		clauses = append(clauses, object{"term": object{"labels.strategy_id": p.String("strategy_id")}}, object{"term": object{"status": "active"}})
	case p.String("alert_id") != "":
		answer.Mode = "alert"
		clauses = append(clauses, object{"term": object{"alert_id": p.String("alert_id")}})
	default:
		answer.Mode = "fingerprint"
		clauses = append(clauses, object{"term": object{"fingerprint": p.String("fingerprint")}})
	}
	query, _ := json.Marshal(object{"bool": object{"filter": clauses}})
	page, err := search.Search(ctx, tenant, query, p.String("cursor"), p.Int("limit", 100))
	if err != nil {
		return linkdAlertsFailure(err)
	}
	scrub := redact.New()
	answer.Alerts, answer.NextCursor = []linkdAlertRecord{}, page.Next
	for _, alert := range page.Items {
		raw, _ := json.Marshal(alert)
		record := linkdAlertRecord{AlertID: alert.AlertID, Status: alert.Status, Fingerprint: alert.Fingerprint, Severity: alert.Severity,
			Record: scrub.JSON(raw)}
		record.StrategyID, _ = alert.Label("strategy_id")
		record.StrategyVersion, _ = alert.Label("strategy_version")
		answer.Alerts = append(answer.Alerts, record)
	}
	limitations := []string{"Records are the link's as its search returned them, after the channel's redaction; the search sees an alert only after the store's refresh."}
	if answer.Mode == "strategy" {
		limitations = append(limitations, "Active alerts only: a recovered or closed alert is not listed. Read it by alert_id or fingerprint.")
	}
	return obchannel.Outcome{Value: answer, Complete: answer.NextCursor == "", Limitations: limitations}
}

// linkdAlertsFailure names a failed search by the link's status; a refusal
// carries the link's own words.
func linkdAlertsFailure(err error) obchannel.Outcome {
	status := linkdmanage.Status(err)
	if status == 0 {
		return obchannel.Outcome{Error: &obchannel.Failure{Code: "linkd_unreachable", Message: "The linkd search did not answer: " +
			redact.New().Text(err.Error())}}
	}
	return obchannel.Outcome{Error: &obchannel.Failure{Code: "linkd_http_" + strconv.Itoa(status),
		Message: "linkd answered the search with HTTP " + strconv.Itoa(status) + ": " + redact.New().Text(err.Error())}}
}
