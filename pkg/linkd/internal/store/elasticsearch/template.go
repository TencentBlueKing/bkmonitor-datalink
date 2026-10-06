// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearchstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

const (
	// currentSchemaVersion 只在需要删除重建索引的不兼容 mapping 变化时递增。
	currentSchemaVersion = 3
	// flattenedIgnoreAbove 为最长四字节 UTF-8 字符预留 Lucene 单 term 上限，超长值仍保留在 _source。
	flattenedIgnoreAbove = 8191
	managedByLinkd       = "linkd"

	entityEvent        = "event"
	entityAlert        = "alert"
	entityAlertHistory = "alert_history"
	entityAlertLog     = "alert_log"
)

type schemaMetadata struct {
	ManagedBy     string `json:"managed_by"`
	Entity        string `json:"entity"`
	Role          string `json:"role,omitempty"`
	SchemaVersion int    `json:"schema_version"`
	BucketDays    int    `json:"bucket_days,omitempty"`
	BucketStart   string `json:"bucket_start,omitempty"`
	BucketEnd     string `json:"bucket_end,omitempty"`
}

// TemplateSpec 定义一个 composable index template 的名称、匹配范围和显式 settings。
type TemplateSpec struct {
	// Name 是 composable index template 名称。
	Name string
	// IndexPatterns 是该模板覆盖的物理索引模式。
	IndexPatterns []string
	// Priority 用于解决多个匹配模板之间的优先级。
	Priority int
	// Settings 是随模板写入的显式索引设置。
	Settings map[string]any
	// Entity 标识模板承载的 Linkd 领域对象。
	Entity string
	// Role 区分同一领域结构的 active、history 和 log 等物理职责。
	Role string
	// BucketDays 非零时声明模板承载固定日数的 UTC 时间桶。
	BucketDays int
}

// SchemaConfig 分别配置 Event、Alert 与 AlertLog 的 index template。
type SchemaConfig struct {
	// Event 配置 Event 索引模板。
	Event TemplateSpec
	// Alert 配置 Alert 索引模板。
	Alert TemplateSpec
	// AlertHistory 配置终态 Alert 时间桶模板。
	AlertHistory TemplateSpec
	// AlertLog 配置 AlertLog 索引模板。
	AlertLog TemplateSpec
}

// Templates 按 Event、Alert、AlertLog 的固定顺序返回全部模板配置。
func (config SchemaConfig) Templates() []TemplateSpec {
	result := make([]TemplateSpec, 0, 4)
	for _, spec := range []TemplateSpec{config.Event, config.Alert, config.AlertHistory, config.AlertLog} {
		if spec.Name != "" {
			result = append(result, spec)
		}
	}
	return result
}

// EnsureSchema 幂等写入 Event、Alert 与 AlertLog 的 strict mapping 模板。
// bucket 索引创建和 alias 原子切换属于 Router 配套的索引生命周期管理，不由本方法猜测。
func (r *Repository) EnsureSchema(ctx context.Context, config SchemaConfig) error {
	for _, spec := range config.Templates() {
		var properties map[string]any
		switch spec.Entity {
		case entityEvent:
			properties = eventProperties()
		case entityAlert, entityAlertHistory:
			properties = alertProperties()
		case entityAlertLog:
			properties = alertLogProperties()
		default:
			return fmt.Errorf("ensure schema for unsupported entity %q", spec.Entity)
		}
		if err := r.putTemplate(ctx, spec, properties); err != nil {
			return fmt.Errorf("ensure %s index template: %w", spec.Entity, err)
		}
	}
	return nil
}

// EnsureIndex 幂等创建一个已经由模板覆盖的物理索引，并验证已有索引的领域 schema。
// 旧 schema 不会被自动删除或迁移，调用方必须先停止写入并显式重建索引。
func (r *Repository) EnsureIndex(ctx context.Context, index, entity string) error {
	if err := validateTarget("index", index); err != nil {
		return err
	}
	if err := validateEntity(entity); err != nil {
		return err
	}
	err := r.performJSON(ctx, http.MethodPut, "/"+index, nil, nil, nil)
	if err != nil {
		var responseErr *responseError
		if !errors.As(err, &responseErr) || responseErr.Type != "resource_already_exists_exception" {
			return fmt.Errorf("ensure elasticsearch index %q: %w", index, err)
		}
	}
	return r.verifyIndexSchema(ctx, index, entity)
}

func (r *Repository) verifyIndexSchema(ctx context.Context, index, entity string) error {
	var response map[string]struct {
		Mappings struct {
			Metadata schemaMetadata `json:"_meta"`
		} `json:"mappings"`
	}
	if err := r.performJSON(ctx, http.MethodGet, "/"+index+"/_mapping", nil, nil, &response); err != nil {
		return fmt.Errorf("verify elasticsearch index %q schema: %w", index, err)
	}
	item, ok := response[index]
	if !ok || len(response) != 1 {
		return fmt.Errorf("verify elasticsearch index %q schema: mapping response does not contain exactly that index", index)
	}
	metadata := item.Mappings.Metadata
	if metadata.ManagedBy != managedByLinkd || metadata.Entity != entity || metadata.SchemaVersion != currentSchemaVersion {
		return fmt.Errorf(
			"elasticsearch index %q has incompatible schema metadata (managed_by=%q, entity=%q, schema_version=%d); stop Linkd, delete the index, and restart to rebuild it",
			index,
			metadata.ManagedBy,
			metadata.Entity,
			metadata.SchemaVersion,
		)
	}
	return nil
}

func (r *Repository) putTemplate(
	ctx context.Context,
	spec TemplateSpec,
	properties map[string]any,
) error {
	if err := validateTemplateSpec(spec); err != nil {
		return err
	}
	settings := spec.Settings
	if settings == nil {
		settings = map[string]any{}
	}
	metadata := schemaMetadata{
		ManagedBy: managedByLinkd, Entity: spec.Entity, Role: spec.Role,
		SchemaVersion: currentSchemaVersion, BucketDays: spec.BucketDays,
	}
	body, err := marshalRequest(map[string]any{
		"index_patterns": spec.IndexPatterns,
		"priority":       spec.Priority,
		"version":        currentSchemaVersion,
		"_meta":          metadata,
		"template": map[string]any{
			"settings": settings,
			"mappings": map[string]any{
				"dynamic":    "strict",
				"_meta":      metadata,
				"properties": properties,
			},
		},
	})
	if err != nil {
		return err
	}
	return r.performJSON(
		ctx,
		http.MethodPut,
		"/_index_template/"+url.PathEscape(spec.Name),
		nil,
		body,
		nil,
	)
}

func validateTemplateSpec(spec TemplateSpec) error {
	if err := validateTarget("template name", spec.Name); err != nil {
		return err
	}
	if len(spec.IndexPatterns) == 0 {
		return fmt.Errorf("template %q index patterns must not be empty", spec.Name)
	}
	for _, pattern := range spec.IndexPatterns {
		if err := validateTarget("template index pattern", pattern); err != nil {
			return err
		}
	}
	if spec.Priority < 0 {
		return fmt.Errorf("template %q priority must not be negative", spec.Name)
	}
	if err := validateEntity(spec.Entity); err != nil {
		return fmt.Errorf("template %q: %w", spec.Name, err)
	}
	return nil
}

func validateEntity(entity string) error {
	if entity != entityEvent && entity != entityAlert && entity != entityAlertHistory && entity != entityAlertLog {
		return fmt.Errorf("elasticsearch schema entity is invalid: %q", entity)
	}
	return nil
}

func eventProperties() map[string]any {
	return map[string]any{
		"bk_tenant_id":     keywordProperty(),
		"event_source_id":  keywordProperty(),
		"related_alert_id": keywordProperty(),
		"event_id":         keywordProperty(),
		"fingerprint":      keywordProperty(),
		"title":            storedStringProperty(),
		"content":          storedStringProperty(),
		"severity":         keywordProperty(),
		"action":           keywordProperty(),
		"action_reason":    storedStringProperty(),
		"condition_key":    keywordProperty(),
		"condition_name":   storedStringProperty(),
		"dimensions":       flattenedProperty(),
		"subject_system":   keywordProperty(),
		"subject_type":     keywordProperty(),
		"subject_id":       keywordProperty(),
		"subject_name":     storedStringProperty(),
		"occurred_at":      dateNanosProperty(),
		"produced_at":      dateNanosProperty(),
		"received_at":      dateNanosProperty(),
		"create_at":        dateNanosProperty(),
		"source_event_id":  keywordProperty(),
		"source_alert_id":  keywordProperty(),
		"source_raw_data":  opaqueObjectProperty(),
		"labels":           flattenedProperty(),
		"extra_data":       opaqueObjectProperty(),
		"processing": strictObjectProperty(map[string]any{
			"state":        keywordProperty(),
			"outcome":      keywordProperty(),
			"reason_code":  keywordProperty(),
			"processed_at": dateNanosProperty(),
		}),
	}
}

func alertProperties() map[string]any {
	return map[string]any{
		"alert_id":         keywordProperty(),
		"bk_tenant_id":     keywordProperty(),
		"event_source_id":  keywordProperty(),
		"fingerprint":      keywordProperty(),
		"title":            storedStringProperty(),
		"content":          storedStringProperty(),
		"severity":         keywordProperty(),
		"condition_key":    keywordProperty(),
		"condition_name":   storedStringProperty(),
		"dimensions":       flattenedProperty(),
		"subject_system":   keywordProperty(),
		"subject_type":     keywordProperty(),
		"subject_id":       keywordProperty(),
		"subject_name":     storedStringProperty(),
		"source_event_id":  keywordProperty(),
		"source_alert_id":  keywordProperty(),
		"labels":           flattenedProperty(),
		"extra_data":       opaqueObjectProperty(),
		"status":           keywordProperty(),
		"latest_event_id":  keywordProperty(),
		"last_occurred_at": dateNanosProperty(),
		"update_at":        dateNanosProperty(),
		"trigger_event_id": keywordProperty(),
		"begin_at":         dateNanosProperty(),
		"create_at":        dateNanosProperty(),
		"end_at":           dateNanosProperty(),
		"end_type":         keywordProperty(),
		"end_reason":       storedStringProperty(),
		"enrich_status":    keywordProperty(),
		"enrich":           opaqueObjectProperty(),
	}
}

func alertLogProperties() map[string]any {
	return map[string]any{
		"log_id":         keywordProperty(),
		"bk_tenant_id":   keywordProperty(),
		"alert_id":       keywordProperty(),
		"operator_kind":  keywordProperty(),
		"operation_kind": keywordProperty(),
		"params":         opaqueObjectProperty(),
		"created_time":   dateNanosProperty(),
	}
}

func keywordProperty() map[string]any {
	return map[string]any{"type": "keyword"}
}

func storedStringProperty() map[string]any {
	return map[string]any{"type": "keyword", "index": false, "doc_values": false}
}

func dateNanosProperty() map[string]any {
	return map[string]any{"type": "date_nanos"}
}

func flattenedProperty() map[string]any {
	return map[string]any{"type": "flattened", "ignore_above": flattenedIgnoreAbove}
}

func opaqueObjectProperty() map[string]any {
	return map[string]any{"type": "object", "enabled": false}
}

func strictObjectProperty(properties map[string]any) map[string]any {
	return map[string]any{"type": "object", "dynamic": "strict", "properties": properties}
}

func marshalRequest(value any) ([]byte, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode elasticsearch request: %w", err)
	}
	return body, nil
}
