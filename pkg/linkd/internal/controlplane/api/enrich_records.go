// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/custom"
	"linkd/internal/eventsource"
	"linkd/internal/jsonpath"
	"linkd/internal/store"
)

// EnrichRecordReader 只读取已经保存的事实，不能触发丰富或写入。
type EnrichRecordReader interface {
	GetAlert(context.Context, string, string) (store.StoredAlert, error)
	GetEvent(context.Context, string, string) (store.StoredEvent, error)
}

// EnrichReleaseReader 按准确来源发布版本提供冻结配置。
type EnrichReleaseReader interface {
	GetRelease(context.Context, string, int64) (eventsource.Release, error)
}

// EnrichRecords 限制只读查询并发；连接和来源服务仍由装配方管理。
type EnrichRecords struct {
	reader   EnrichRecordReader
	releases EnrichReleaseReader
	slots    chan struct{}
}

// NewEnrichRecords 注入现有事实与发布读取，不初始化 schema。
func NewEnrichRecords(reader EnrichRecordReader, releases EnrichReleaseReader) *EnrichRecords {
	return &EnrichRecords{reader: reader, releases: releases, slots: make(chan struct{}, 2)}
}

type enrichRecordRequest struct {
	Tenant   string   `json:"bk_tenant_id"`
	EventIDs []string `json:"event_ids"`
	Detail   bool     `json:"detail"`
}

type enrichMeasure struct {
	ID     string              `json:"id"`
	Type   string              `json:"type"`
	Name   string              `json:"name"`
	Status domain.EnrichStatus `json:"status"`
	Code   string              `json:"code,omitempty"`
}

type enrichFieldChange struct {
	Field         string          `json:"field"`
	Display       string          `json:"display"`
	BeforePresent bool            `json:"before_present"`
	Before        json.RawMessage `json:"before"`
	AfterPresent  bool            `json:"after_present"`
	After         json.RawMessage `json:"after"`
	Measures      []string        `json:"measures"`
	path          string
}

type enrichRecord struct {
	AlertID           string              `json:"alert_id"`
	EventID           string              `json:"event_id"`
	Severity          string              `json:"severity"`
	EnrichedAt        time.Time           `json:"enriched_at"`
	Status            domain.EnrichStatus `json:"status"`
	EnrichmentCount   int                 `json:"enrichment_count"`
	ChangedFieldCount int                 `json:"changed_field_count"`
	Enrichments       []enrichMeasure     `json:"enrichments"`
	FieldChanges      []enrichFieldChange `json:"field_changes,omitempty"`
}

func (a *API) queryEnrichRecords(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	var request enrichRecordRequest
	if decode(r, &request) != nil || domain.ValidateIdentityPart("tenant", request.Tenant, 64) != nil || len(request.EventIDs) == 0 || len(request.EventIDs) > 20 || len(r.PathValue("id")) == 0 || len(r.PathValue("id")) > domain.EntityIDMaxBytes {
		http.Error(w, "invalid enrich record request", http.StatusBadRequest)
		return
	}
	for _, id := range request.EventIDs {
		if strings.TrimSpace(id) == "" || len(id) > domain.EntityIDMaxBytes {
			http.Error(w, "invalid event identity", http.StatusBadRequest)
			return
		}
	}
	service := a.EnrichRecords
	if service == nil || service.reader == nil || service.releases == nil {
		http.Error(w, "enrich query unavailable", http.StatusServiceUnavailable)
		return
	}
	select {
	case service.slots <- struct{}{}:
		defer func() { <-service.slots }()
	default:
		http.Error(w, "enrich query busy", http.StatusTooManyRequests)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	stored, err := service.reader.GetAlert(ctx, request.Tenant, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "alert not found", http.StatusNotFound)
		return
	}
	alert := stored.Alert
	if err != nil || alert.BKTenantID != request.Tenant || alert.AlertID != r.PathValue("id") {
		http.Error(w, "alert query failed", http.StatusBadGateway)
		return
	}
	items := []enrichRecord{}
	unavailable := []string{}
	seen := map[string]bool{}
	for _, id := range request.EventIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		// 先验证事实关联，再读取发布；失败项明确列出，不能拼接其他租户或其他等级。
		event, readErr := service.reader.GetEvent(ctx, request.Tenant, id)
		if readErr != nil || event.Event.EventID != id || event.Event.BKTenantID != request.Tenant || event.Event.EventSourceID != alert.EventSourceID || !slices.Contains(event.Event.RelatedAlertIDs, alert.AlertID) {
			unavailable = append(unavailable, id)
			continue
		}
		release, readErr := service.releases.GetRelease(ctx, event.Event.EventSourceID, event.Event.EnrichSourceReleaseVersion())
		if readErr != nil {
			unavailable = append(unavailable, id)
			continue
		}
		record, readErr := projectEnrichRecord(alert, event.Event, release, request.Detail)
		if readErr != nil {
			unavailable = append(unavailable, id)
			continue
		}
		if record.EnrichmentCount > 0 {
			items = append(items, record)
		}
	}
	if ctx.Err() != nil {
		http.Error(w, "enrich query deadline exceeded", http.StatusGatewayTimeout)
		return
	}
	payload, err := json.Marshal(struct {
		Items       []enrichRecord `json:"items"`
		Unavailable []string       `json:"unavailable"`
	}{items, unavailable})
	if err != nil || len(payload) > 1<<20 {
		http.Error(w, "enrich response exceeds budget", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(payload)
}

func projectEnrichRecord(alert domain.Alert, event domain.Event, release eventsource.Release, detail bool) (enrichRecord, error) {
	severity := alert.Severity
	// 全等级关闭是明确保留标记；只有该 Event 含此唯一关闭评估才取它的丰富结果。
	if len(event.Evaluations) == 1 && event.Evaluations[0].Severity == domain.SeverityAll && event.Evaluations[0].Action == domain.EventActionClosed {
		severity = domain.SeverityAll
	}
	result, err := projectEventEnrichment(event, release, severity, detail)
	result.AlertID = alert.AlertID
	return result, err
}

// projectEventEnrichment 从已提交的逐等级补丁读取事实，也用于无 Alert 的来源调试。
func projectEventEnrichment(event domain.Event, release eventsource.Release, severity string, detail bool) (enrichRecord, error) {
	result := enrichRecord{EventID: event.EventID, Severity: severity, Enrichments: []enrichMeasure{}}
	encoded, err := json.Marshal(release.Spec.Enrich)
	if err != nil {
		return result, err
	}
	digest := sha256.Sum256(encoded)
	if release.ID != event.EventSourceID || release.Version != event.EnrichSourceReleaseVersion() || release.Spec.EventSourceID != event.EventSourceID || release.Deleted || (release.Spec.RelatedTenantID != "" && release.Spec.RelatedTenantID != event.BKTenantID) || hex.EncodeToString(digest[:]) != event.EnrichConfigDigest || event.EnrichedAt == nil {
		return result, fmt.Errorf("enrich release identity mismatch")
	}
	evaluation, found := event.ForSeverity(severity)
	if !found {
		return result, fmt.Errorf("selected enrich evaluation missing")
	}
	result.Severity, result.EnrichedAt = severity, *event.EnrichedAt
	payload, err := enrich.DecodePayload(evaluation.Data)
	if err != nil {
		return result, err
	}
	var selected domain.EventEvaluation
	for _, e := range event.Evaluations {
		if e.Severity == severity {
			selected = e
			break
		}
	}
	current, err := domain.EventDocument(event, selected)
	if err != nil {
		return result, err
	}
	configs := map[string]custom.Config{}
	for _, processor := range release.Spec.Enrich.Processors {
		if processor.Type != "cmdb" && processor.Type != "fields" {
			continue
		}
		data, err := json.Marshal(processor.Config)
		if err != nil {
			return result, err
		}
		var config custom.Config
		if err := json.Unmarshal(data, &config); err != nil {
			return result, err
		}
		labels := map[string]string{}
		for path, label := range config.FieldLabels {
			target, err := jsonpath.ParseTarget(path)
			if err != nil {
				return result, err
			}
			labels[target.String()] = label
		}
		config.FieldLabels = labels
		configs[processor.Type] = config
	}
	changes := map[string]*enrichFieldChange{}
	order := []string{}
	statuses := []domain.EnrichStatus{}
	for _, entry := range payload.Processors {
		for kind, envelope := range entry {
			config, business := configs[kind]
			if business && envelope.Patches == nil && (envelope.Status == domain.EnrichStatusSucceeded || envelope.Status == domain.EnrichStatusPartial) {
				return result, fmt.Errorf("historical enrich has no committed patch evidence")
			}
			names := map[string]string{}
			for _, rule := range config.Rules {
				names[rule.ID] = rule.Name
			}
			if business {
				statuses = append(statuses, envelope.Status)
				for _, trace := range envelope.Trace {
					if trace.OperationID != "" || strings.HasPrefix(trace.RuleID, "kac_custom_catalog") {
						continue
					}
					if _, exists := names[trace.RuleID]; !exists {
						continue
					}
					result.Enrichments = append(result.Enrichments, enrichMeasure{ID: kind + ":" + trace.RuleID, Type: kind, Name: names[trace.RuleID], Status: trace.Status, Code: trace.Code})
				}
			}
			// 仅成功或部分成功的已提交补丁参与重放；规则失败和回滚不会有生效字段。
			if envelope.Status != domain.EnrichStatusSucceeded && envelope.Status != domain.EnrichStatusPartial {
				continue
			}
			for _, patch := range envelope.Patches {
				target, err := jsonpath.ParseTarget(patch.Path)
				if err != nil {
					return result, err
				}
				if business && !strings.HasPrefix(patch.RuleID, "kac_custom_catalog") && !strings.Contains(patch.Path, "__kac_") {
					change := changes[target.String()]
					if change == nil {
						before, present := target.Get(current)
						value, err := json.Marshal(before)
						if err != nil {
							return result, err
						}
						field := enrichFieldName(target)
						label := config.FieldLabels[target.String()]
						if label == "" {
							label = field
						}
						change = &enrichFieldChange{Field: field, Display: label, BeforePresent: present, Before: value, path: target.String(), Measures: []string{}}
						changes[target.String()] = change
						order = append(order, target.String())
					}
					measure := kind + ":" + patch.RuleID
					if !slices.Contains(change.Measures, measure) {
						change.Measures = append(change.Measures, measure)
					}
				}
				current, err = domain.ApplyEnrichPatches(current, []domain.EnrichPatch{patch})
				if err != nil {
					return result, err
				}
			}
		}
	}
	for _, path := range order {
		change := changes[path]
		target, _ := jsonpath.ParseTarget(path)
		after, present := target.Get(current)
		value, err := json.Marshal(after)
		if err != nil {
			return result, err
		}
		if change.BeforePresent == present && bytes.Equal(change.Before, value) {
			continue
		}
		change.After, change.AfterPresent = value, present
		result.ChangedFieldCount++
		if detail {
			result.FieldChanges = append(result.FieldChanges, *change)
		}
	}
	result.EnrichmentCount = len(result.Enrichments)
	result.Status = domain.AggregateEnrichStatus(statuses)
	return result, nil
}

func enrichFieldName(target jsonpath.Target) string {
	parts := target.Parts()
	if len(parts) == 1 {
		if parts[0] == "title" {
			return "name"
		}
		if parts[0] == "subject_name" {
			return "object"
		}
	}
	if len(parts) == 2 {
		if key, ok := parts[1].(string); ok {
			return key
		}
	}
	return target.String()
}
