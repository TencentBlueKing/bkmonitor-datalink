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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/kingeye"
	"linkd/internal/eventsource"
	"linkd/internal/jsonpath"
	"linkd/internal/store"
)

type eventWindowFilter struct {
	Value any    `json:"value"`
	Match string `json:"match"`
}

type eventWindowRequest struct {
	Tenant   string                       `json:"bk_tenant_id"`
	Source   string                       `json:"event_source_id"`
	From     time.Time                    `json:"received_from"`
	To       time.Time                    `json:"received_to"`
	After    string                       `json:"after"`
	Limit    int                          `json:"limit"`
	Severity string                       `json:"severity"`
	Filters  map[string]eventWindowFilter `json:"filters"`
}

func (q eventWindowRequest) valid() bool {
	if domain.ValidateIdentityPart("tenant", q.Tenant, 64) != nil || domain.ValidateIdentityPart("source", q.Source, 32) != nil || q.From.IsZero() || q.To.Before(q.From) || q.To.Sub(q.From) > time.Hour || q.Limit < 0 || q.Limit > 200 || len(q.After) > 65536 || len(q.Filters) > 64 {
		return false
	}
	for _, filter := range q.Filters {
		if filter.Match != "" && filter.Match != "equals" && filter.Match != "contains" {
			return false
		}
	}
	return true
}

type eventWindowReader interface {
	ListEventsBySource(context.Context, string, string, store.EventBySourceRequest) (store.EventPage, error)
}

type eventWindowDiagnostic struct {
	Processor  string `json:"processor,omitempty"`
	Code       string `json:"code"`
	Dependency string `json:"dependency,omitempty"`
}

// eventWindowStep 保留处理阶段及其逐次生效变化，展示范围由调用方决定。
type eventWindowStep struct {
	Processor string                  `json:"processor"`
	Status    domain.EnrichStatus     `json:"status"`
	Rules     map[string]string       `json:"rules"`
	Changes   []eventWindowStepChange `json:"changes"`
}

type eventWindowStepChange struct {
	enrichFieldChange
	RuleID      string `json:"rule_id,omitempty"`
	OperationID string `json:"operation_id,omitempty"`
}

type eventWindowItem struct {
	enrichRecord
	SourceID        string                   `json:"event_source_id"`
	Version         int64                    `json:"event_source_version"`
	ReceivedAt      time.Time                `json:"received_at"`
	Action          domain.EventAction       `json:"action"`
	ProcessingState domain.EventProcessState `json:"processing_state"`
	Alarm           map[string]any           `json:"alarm"`
	Original        map[string]any           `json:"original,omitempty"`
	Diagnostics     []eventWindowDiagnostic  `json:"diagnostics"`
	Unavailable     bool                     `json:"unavailable"`
	Steps           *[]eventWindowStep       `json:"steps,omitempty"`
}

func (a *API) queryEventEnrichWindow(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var q eventWindowRequest
	if decode(r, &q) != nil || !q.valid() {
		http.Error(w, "invalid event window request", http.StatusBadRequest)
		return
	}
	service := a.EnrichRecords
	if service == nil {
		http.Error(w, "event window unavailable", http.StatusServiceUnavailable)
		return
	}
	reader, ok := service.reader.(eventWindowReader)
	if !ok {
		http.Error(w, "event window unavailable", http.StatusServiceUnavailable)
		return
	}
	select {
	case service.slots <- struct{}{}:
		defer func() { <-service.slots }()
	default:
		http.Error(w, "event window busy", http.StatusTooManyRequests)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if q.Limit == 0 {
		q.Limit = 50
	}
	// 1. 按原始接收时间取完整 Event 页，尚未生成 Alert 的事件也属于观察范围。
	page, err := reader.ListEventsBySource(ctx, q.Tenant, q.Source, store.EventBySourceRequest{ReceivedFrom: q.From, ReceivedTo: q.To, Page: store.PageRequest{Cursor: q.After, Limit: q.Limit}})
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, store.ErrInvalidCursor) || errors.Is(err, store.ErrInvalidArgument) {
			status = http.StatusBadRequest
		}
		http.Error(w, "event window query failed", status)
		return
	}
	items := []eventWindowItem{}
	releases := map[int64]eventsource.Release{}
	// 2. 同一页复用版本读取；每个等级使用它自己的已提交丰富结果。
	for _, stored := range page.Events {
		event := stored.Event
		release, found := releases[event.EnrichSourceReleaseVersion()]
		if !found && event.EnrichStatus != domain.EnrichStatusPending {
			release, _ = service.releases.GetRelease(ctx, event.EventSourceID, event.EnrichSourceReleaseVersion())
			releases[event.EnrichSourceReleaseVersion()] = release
		}
		for _, evaluation := range event.Evaluations {
			item := projectEventWindowItem(stored, evaluation, release, false)
			if matchEventWindowFilters(item, q.Filters) {
				items = append(items, item)
			}
		}
	}
	// 3. 空筛选页仍返回原游标，调用方可继续向后读，不能误报整个窗口无数据。
	output(w, map[string]any{"items": items, "next": page.NextCursor, "scanned": len(page.Events)})
}

func (a *API) queryEventEnrichDetail(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	var q eventWindowRequest
	if decode(r, &q) != nil || !q.valid() || q.Severity == "" || len(q.Severity) > 64 || len(r.PathValue("id")) == 0 || len(r.PathValue("id")) > domain.EntityIDMaxBytes {
		http.Error(w, "invalid event detail request", http.StatusBadRequest)
		return
	}
	service := a.EnrichRecords
	if service == nil {
		http.Error(w, "event detail unavailable", http.StatusServiceUnavailable)
		return
	}
	select {
	case service.slots <- struct{}{}:
		defer func() { <-service.slots }()
	default:
		http.Error(w, "event detail busy", http.StatusTooManyRequests)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	stored, err := service.reader.GetEvent(ctx, q.Tenant, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "event not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "event query failed", http.StatusBadGateway)
		return
	}
	event := stored.Event
	if event.BKTenantID != q.Tenant || event.EventSourceID != q.Source || event.ReceivedAt.Before(q.From) || event.ReceivedAt.After(q.To) {
		http.Error(w, "event not in window", http.StatusNotFound)
		return
	}
	for _, evaluation := range event.Evaluations {
		if evaluation.Severity != q.Severity {
			continue
		}
		release := eventsource.Release{}
		if event.EnrichStatus != domain.EnrichStatusPending {
			release, _ = service.releases.GetRelease(ctx, event.EventSourceID, event.EnrichSourceReleaseVersion())
		}
		output(w, projectEventWindowItem(stored, evaluation, release, true))
		return
	}
	http.Error(w, "event severity not found", http.StatusNotFound)
}

func projectEventWindowItem(stored store.StoredEvent, evaluation domain.EventEvaluation, release eventsource.Release, detail bool) eventWindowItem {
	event := stored.Event
	item := eventWindowItem{enrichRecord: enrichRecord{EventID: event.EventID, Severity: evaluation.Severity, Status: event.EnrichStatus, Enrichments: []enrichMeasure{}}, SourceID: event.EventSourceID, Version: event.EventSourceVersion, ReceivedAt: event.ReceivedAt, Action: evaluation.Action, ProcessingState: stored.Processing.State, Diagnostics: []eventWindowDiagnostic{}}
	original, err := domain.EventDocument(event, evaluation)
	if err != nil {
		item.Unavailable = true
		item.Diagnostics = append(item.Diagnostics, eventWindowDiagnostic{Code: "event_unavailable"})
		return item
	}
	item.Alarm = eventWindowAlarm(original, evaluation.Severity)
	if detail {
		item.Original = eventWindowAlarm(original, evaluation.Severity)
	}
	if event.EnrichStatus == domain.EnrichStatusPending {
		if stored.Processing.ReasonCode != "" {
			item.Diagnostics = append(item.Diagnostics, eventWindowDiagnostic{Code: stored.Processing.ReasonCode})
		}
		return item
	}
	record, err := projectEventEnrichment(event, release, evaluation.Severity, true)
	if err != nil {
		item.Unavailable = true
		item.Diagnostics = append(item.Diagnostics, eventWindowDiagnostic{Code: "enrich_history_unavailable"})
		return item
	}
	item.enrichRecord = record
	if !detail {
		item.FieldChanges = nil
	}
	saved, _ := event.ForSeverity(evaluation.Severity)
	// 整体状态包含内置处理器失败，不能只以用户配置的 fields/cmdb 状态代替。
	item.Status = saved.Status
	payload, err := enrich.DecodePayload(saved.Data)
	if err != nil {
		item.Unavailable = true
		item.Diagnostics = append(item.Diagnostics, eventWindowDiagnostic{Code: "enrich_payload_unavailable"})
		return item
	}
	current := original
	fieldMeasures := map[string][]string{}
	// 1. 展示元信息来自事件自己的发布版本，净变化为零的字段也保留历史名称。
	rules, labels, err := eventWindowMetadata(release, event.BKTenantID)
	if err != nil {
		item.Unavailable = true
		item.Diagnostics = append(item.Diagnostics, eventWindowDiagnostic{Code: "enrich_history_unavailable"})
		return item
	}
	steps := []eventWindowStep{}
	completeSteps := true

	// 2. 沿已保存顺序应用赋值；只计算中间展示值，不执行规则或依赖查询。
	for _, entry := range payload.Processors {
		for kind, envelope := range entry {
			step := eventWindowStep{Processor: kind, Status: envelope.Status, Rules: rules[kind], Changes: []eventWindowStepChange{}}
			if step.Rules == nil {
				step.Rules = map[string]string{}
			}
			if kind != "fields" && kind != "cmdb" {
				names := map[string]string{"strategy": "策略丰富", "resource": "资源丰富", "monitor-resource": "监控资源丰富", "k8s": "Kubernetes 丰富", "apm": "APM 丰富", "log": "日志丰富", "display": "展示信息丰富", "metric": "指标丰富", "source": "告警源丰富", "cmdb-access": "对象权限丰富"}
				name := names[kind]
				if name == "" {
					name = kind
				}
				item.Enrichments = append(item.Enrichments, enrichMeasure{ID: kind, Type: "builtin", Name: name, Status: envelope.Status})
			}
			for _, diagnostic := range envelope.Diagnostics {
				item.Diagnostics = append(item.Diagnostics, eventWindowDiagnostic{Processor: kind, Code: string(diagnostic.Code), Dependency: diagnostic.Dependency})
			}
			if envelope.Status != domain.EnrichStatusSucceeded && envelope.Status != domain.EnrichStatusPartial {
				steps = append(steps, step)
				continue
			}
			if envelope.Patches == nil && len(envelope.Value) > 0 {
				completeSteps = false
			}
			for _, patch := range envelope.Patches {
				target, parseErr := jsonpath.ParseTarget(patch.Path)
				if parseErr != nil {
					continue
				}
				field := enrichFieldName(target)
				measure := kind
				if kind == "fields" || kind == "cmdb" {
					measure = kind + ":" + patch.RuleID
				}
				if !strings.HasPrefix(patch.RuleID, "kac_custom_catalog") {
					fieldMeasures[field] = append(fieldMeasures[field], measure)
				}
				if !detail {
					continue
				}
				before := eventWindowAlarm(current, evaluation.Severity)
				targetBefore, targetBeforePresent := target.Get(current)
				current, err = domain.ApplyEnrichPatches(current, []domain.EnrichPatch{patch})
				if err != nil {
					item.Unavailable = true
					item.Diagnostics = append(item.Diagnostics, eventWindowDiagnostic{Processor: kind, Code: "enrich_patch_unavailable"})
					return item
				}
				changes := eventWindowChanges(before, eventWindowAlarm(current, evaluation.Severity), labels, nil)
				// 目录冻结可能晚于字段赋值；同名 label 遮盖 extra_data 时仍保留真实赋值。
				if len(target.Parts()) == 2 && !strings.HasPrefix(field, "__kac_") {
					targetAfter, targetAfterPresent := target.Get(current)
					beforeJSON, _ := json.Marshal(targetBefore)
					afterJSON, _ := json.Marshal(targetAfter)
					found := false
					for _, change := range changes {
						found = found || change.Field == field
					}
					if !found && (targetBeforePresent != targetAfterPresent || !bytes.Equal(beforeJSON, afterJSON)) {
						label := labels[field]
						if label == "" {
							label = field
						}
						changes = append(changes, enrichFieldChange{Field: field, Display: label, BeforePresent: targetBeforePresent, Before: beforeJSON, AfterPresent: targetAfterPresent, After: afterJSON, Measures: []string{}})
					}
				}
				for _, change := range changes {
					step.Changes = append(step.Changes, eventWindowStepChange{enrichFieldChange: change, RuleID: patch.RuleID, OperationID: patch.OperationID})
				}
			}
			if !detail {
				current, err = domain.ApplyEnrichPatches(current, envelope.Patches)
				if err != nil {
					item.Unavailable = true
					item.Diagnostics = append(item.Diagnostics, eventWindowDiagnostic{Processor: kind, Code: "enrich_patch_unavailable"})
					return item
				}
			}
			steps = append(steps, step)
		}
	}
	// 3. 原始值、最终值及汇总结果保持兼容，详情额外提供完整步骤。
	item.Alarm = eventWindowAlarm(current, evaluation.Severity)
	changes := eventWindowChanges(eventWindowAlarm(original, evaluation.Severity), item.Alarm, labels, fieldMeasures)
	item.ChangedFieldCount = len(changes)
	item.EnrichmentCount = len(item.Enrichments)
	if detail {
		item.FieldChanges = changes
		if completeSteps {
			item.Steps = &steps
		} else {
			item.Unavailable = true
			item.Diagnostics = append(item.Diagnostics, eventWindowDiagnostic{Code: "enrich_steps_unavailable"})
		}
	}
	return item
}

func eventWindowMetadata(release eventsource.Release, tenants ...string) (map[string]map[string]string, map[string]string, error) {
	rules := map[string]map[string]string{}
	labels := map[string]string{}
	for _, processor := range release.Spec.Enrich.Processors {
		if processor.Type != "fields" && processor.Type != "cmdb" {
			continue
		}
		var metadata struct {
			Rules []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"rules"`
			FieldLabels       map[string]string            `json:"field_labels"`
			TenantFieldLabels map[string]map[string]string `json:"tenant_field_labels"`
		}
		encoded, err := json.Marshal(processor.Config)
		if err != nil {
			return nil, nil, err
		}
		if err := json.Unmarshal(encoded, &metadata); err != nil {
			return nil, nil, err
		}
		rules[processor.Type] = map[string]string{}
		for _, rule := range metadata.Rules {
			rules[processor.Type][rule.ID] = rule.Name
		}
		fieldLabels := metadata.FieldLabels
		if len(tenants) > 0 && metadata.TenantFieldLabels[tenants[0]] != nil {
			fieldLabels = metadata.TenantFieldLabels[tenants[0]]
		}
		for path, label := range fieldLabels {
			target, err := jsonpath.ParseTarget(path)
			if err != nil {
				return nil, nil, err
			}
			labels[enrichFieldName(target)] = label
		}
	}
	return rules, labels, nil
}

func eventWindowAlarm(event map[string]any, severity string) map[string]any {
	result := map[string]any{}
	extra, _ := event["extra_data"].(map[string]any)
	for key, value := range extra {
		if !strings.HasPrefix(key, "__kac_") {
			result[key] = value
		}
	}
	labels, _ := event["labels"].(map[string]any)
	for key, value := range labels {
		result[key] = value
	}
	// 已登记自定义字段沿用正式投影的 extra_data 优先级。
	if catalog, ok := extra["__kac_custom_fields"].([]any); ok {
		for _, entry := range catalog {
			if key, ok := entry.(string); ok && !kingeye.IsReservedAlarmField(key) {
				if value, exists := extra[key]; exists {
					result[key] = value
				}
			}
		}
	}
	for _, key := range []string{"object", "item", "meta_info", "strategy_id", "dimension_info"} {
		if value, ok := extra[key]; ok {
			result[key] = value
		}
	}
	result["name"], result["content"], result["level"] = event["title"], event["content"], severity
	result["bk_tenant_id"] = event["bk_tenant_id"]
	if severity == "critical" {
		result["level"] = "fatal"
	}
	if severity == "info" {
		result["level"] = "remind"
	}
	if _, ok := result["object"]; !ok {
		result["object"] = event["subject_name"]
		if result["object"] == "" {
			result["object"] = event["subject_id"]
		}
	}
	if _, ok := result["item"]; !ok {
		result["item"] = result["display_name"]
		if result["item"] == nil || result["item"] == "" {
			result["item"] = result["strategy_name"]
		}
	}
	if _, ok := result["meta_info"]; !ok {
		result["meta_info"] = event["source_event_id"]
	}
	if _, ok := result["strategy_id"]; !ok {
		result["strategy_id"] = result["monitor_template_id"]
	}
	if _, ok := result["dimension_info"]; !ok {
		result["dimension_info"] = result["dimension_text"]
	}
	return result
}

func matchEventWindowFilters(item eventWindowItem, filters map[string]eventWindowFilter) bool {
	for field, filter := range filters {
		target := item.Alarm[field]
		if field == "enrich_method_name" {
			names := []string{}
			for _, measure := range item.Enrichments {
				names = append(names, measure.Name)
			}
			target = strings.Join(names, ";")
		}
		if field == "enrich_status" {
			target = string(item.Status)
		}
		if filter.Value == nil {
			continue
		}
		values, ok := filter.Value.([]any)
		if !ok {
			values = []any{filter.Value}
		}
		text := ""
		if target != nil {
			text = fmt.Sprint(target)
		}
		matched := false
		for _, value := range values {
			if filter.Match == "equals" {
				matched = matched || text == fmt.Sprint(value)
			} else {
				matched = matched || strings.Contains(text, fmt.Sprint(value))
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

// eventWindowChanges 比较最终展示字段，保留内置丰富和自定义规则的真实前后值。
func eventWindowChanges(original, effective map[string]any, labels map[string]string, measures map[string][]string) []enrichFieldChange {
	keys := map[string]bool{}
	for key := range original {
		keys[key] = true
	}
	for key := range effective {
		keys[key] = true
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	changes := []enrichFieldChange{}
	for _, key := range ordered {
		before, beforePresent := original[key]
		after, afterPresent := effective[key]
		beforeJSON, _ := json.Marshal(before)
		afterJSON, _ := json.Marshal(after)
		if beforePresent == afterPresent && bytes.Equal(beforeJSON, afterJSON) {
			continue
		}
		display := labels[key]
		if display == "" {
			display = key
		}
		names := []string{}
		for _, id := range measures[key] {
			found := false
			for _, old := range names {
				found = found || old == id
			}
			if !found {
				names = append(names, id)
			}
		}
		changes = append(changes, enrichFieldChange{Field: key, Display: display, BeforePresent: beforePresent, Before: beforeJSON, AfterPresent: afterPresent, After: afterJSON, Measures: names})
	}
	return changes
}
