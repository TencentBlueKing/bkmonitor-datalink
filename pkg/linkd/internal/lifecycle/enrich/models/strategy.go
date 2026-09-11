// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package models

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"linkd/internal/domain"
)

// StrategyQueryConfig 是鲸眼声明式策略中单条 query_config 的类型化读取视图。
// Raw 保留完整配置，供 metric_query_params 在不回查蓝鲸策略历史的情况下透传扩展字段。
type StrategyQueryConfig struct {
	ID              int64             `json:"id"`
	CustomEventName string            `json:"custom_event_name"`
	AlertName       string            `json:"alert_name"`
	BKStrategyID    int64             `json:"bkmonitor_strategy_id"`
	QueryString     string            `json:"query_string"`
	IndexSetID      int64             `json:"index_set_id"`
	PromQL          string            `json:"promql"`
	TimeField       json.RawMessage   `json:"time_field"`
	ExtendFields    domain.JSONObject `json:"extend_fields"`
	Functions       json.RawMessage   `json:"functions"`
	Unit            string            `json:"unit"`
	Alias           string            `json:"alias"`
	MetricID        string            `json:"metric_id"`
	MetricSource    string            `json:"metric_source"`
	SourceConfig    domain.JSONObject `json:"source_config"`
	AggregateMethod string            `json:"agg_method"`
	AggregatePeriod int64             `json:"agg_interval"`
	MetricField     string            `json:"metric_field"`
	AggregateFilter json.RawMessage   `json:"agg_condition"`
	AggregateBy     []string          `json:"agg_dimension"`
	ResultTableID   string            `json:"result_table_id"`
	DataSourceLabel string            `json:"data_source_label"`
	DataTypeLabel   string            `json:"data_type_label"`
	Raw             domain.JSONObject `json:"-"`
}

// UnmarshalJSON 同时保留完整 query_config，并填充当前丰富流程需要的类型化字段。
func (q *StrategyQueryConfig) UnmarshalJSON(data []byte) error {
	type queryConfig StrategyQueryConfig
	var typed queryConfig
	if err := json.Unmarshal(data, &typed); err != nil {
		return err
	}
	var raw domain.JSONObject
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*q = StrategyQueryConfig(typed)
	q.Raw = raw
	return nil
}

// CWStrategyKind 是鲸眼声明式策略资源的 kind。
type CWStrategyKind string

const (
	// CWStrategyKindStrategy 表示普通声明式策略。
	CWStrategyKindStrategy CWStrategyKind = "Strategy"
	// CWStrategyKindCloud 表示云平台声明式策略。
	CWStrategyKindCloud CWStrategyKind = "StrategyCloud"
)

// CWStrategy 对应 core_v1alpha1_strategy 中的一条鲸眼声明式策略记录。
type CWStrategy struct {
	CreatedAt                time.Time
	CreatedBy                string
	UpdatedAt                time.Time
	UpdatedBy                string
	Active                   bool
	Kind                     CWStrategyKind
	APIVersion               string
	Name                     string
	Namespace                string
	UID                      string
	Annotations              domain.JSONObject
	Spec                     CWStrategySpec
	Status                   CWStrategyStatus
	BKTenantID               *string
	BKBizID                  *int64
	IsDefault                *bool
	DefaultStrategyConfigUID *string
	MonitorTemplateID        *int64
	ConfigID                 *string
	ObjectModelCode          *string
	BKObjectInstID           *string
}

// StrategyItemProjection 返回 Enrich 使用的声明式策略查询投影。
// 当前 KAC 的分类、展示和指标清洗均复用同一份 StrategyConfig；Linkd 同样以
// spec.strategy_item 为唯一策略内容来源，不再回查蓝鲸策略当前表或历史表。
func (s CWStrategy) StrategyItemProjection() (StrategyItemProjection, error) {
	if s.BKBizID == nil || *s.BKBizID <= 0 {
		return StrategyItemProjection{}, fmt.Errorf("kingeye strategy must contain a positive bk_biz_id")
	}
	if s.Spec.StrategyItem == nil || len(s.Spec.StrategyItem.QueryConfigs) == 0 {
		return StrategyItemProjection{}, fmt.Errorf("kingeye strategy must contain strategy_item.query_configs")
	}
	queries := make([]StrategyQueryConfig, len(s.Spec.StrategyItem.QueryConfigs))
	copy(queries, s.Spec.StrategyItem.QueryConfigs)
	for index := range queries {
		query := &queries[index]
		if query.ResultTableID == "" {
			query.ResultTableID = s.Spec.TableID
		}
		if query.MetricField == "" {
			query.MetricField = s.Spec.FieldName
		}
		if query.AggregateMethod == "" {
			query.AggregateMethod = s.Spec.StrategyItem.AggregateMethod
		}
		if query.AggregatePeriod == 0 {
			query.AggregatePeriod = aggregatePeriodSeconds(s.Spec.StrategyItem.AggregatePeriod)
		}
		if query.AggregateBy == nil {
			query.AggregateBy = append([]string(nil), s.Spec.StrategyItem.AggregateBy...)
		}
		if len(query.AggregateFilter) == 0 {
			query.AggregateFilter = marshalStrategyConditions(s.Spec.StrategyItem.AggregateFilter)
		}
		if len(query.Functions) == 0 {
			query.Functions = append(json.RawMessage(nil), s.Spec.StrategyItem.Functions...)
		}
		if query.DataSourceLabel == "" {
			query.DataSourceLabel = inferDataSourceLabel(s, *query)
		}
		if query.DataTypeLabel == "" {
			query.DataTypeLabel = inferDataTypeLabel(s)
		}
	}
	item := s.Spec.StrategyItem
	name := s.Spec.ItemName
	if name == "" {
		name = s.Spec.Name
	}
	return StrategyItemProjection{
		BKBizID: *s.BKBizID, Name: name, Expression: item.Expression,
		Functions:    append(json.RawMessage(nil), item.Functions...),
		QueryConfigs: queries,
	}, nil
}

func aggregatePeriodSeconds(raw json.RawMessage) int64 {
	var seconds int64
	if json.Unmarshal(raw, &seconds) == nil {
		return seconds
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return 0
	}
	if strings.HasSuffix(text, "s") {
		seconds, _ = strconv.ParseInt(strings.TrimSuffix(text, "s"), 10, 64)
	}
	return seconds
}

func marshalStrategyConditions(values []domain.JSONObject) json.RawMessage {
	if values == nil {
		return json.RawMessage(`[]`)
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return json.RawMessage(`[]`)
	}
	return encoded
}

func inferDataSourceLabel(strategy CWStrategy, query StrategyQueryConfig) string {
	switch {
	case strategy.Spec.MetricSource == CWMetricSourceKLC:
		return "bk_log_search"
	case query.MetricSource == "kapm" || query.MetricSource == "krum":
		return "custom"
	default:
		return "bk_monitor"
	}
}

func inferDataTypeLabel(strategy CWStrategy) string {
	switch strategy.Spec.MonitorItemType {
	case CWMonitorItemTypeLog, CWMonitorItemTypeLogKeyword:
		return "log"
	default:
		return "time_series"
	}
}

// StrategyItemProjection 是 Processor 共享的最小策略内容视图。
type StrategyItemProjection struct {
	BKBizID      int64
	Name         string
	Expression   string
	Functions    json.RawMessage
	QueryConfigs []StrategyQueryConfig
}

// CWStrategyFieldTag 是 Kingeye BaseStrategySpec.field_tag。
type CWStrategyFieldTag string

const (
	// CWStrategyFieldTagDerivedMetric 表示衍生指标。
	CWStrategyFieldTagDerivedMetric CWStrategyFieldTag = "derived_metric"
)

// CWMonitorItemType 是 Kingeye BaseStrategySpec.monitor_item_type。
type CWMonitorItemType string

const (
	// CWMonitorItemTypeLog 表示日志指标。
	CWMonitorItemTypeLog CWMonitorItemType = "log"
	// CWMonitorItemTypeLogKeyword 表示日志关键字。
	CWMonitorItemTypeLogKeyword CWMonitorItemType = "log_keyword"
)

const (
	// CWStrategyConfigTypeData 表示基于数据策略。
	CWStrategyConfigTypeData = "data"
	// CWMetricSourceKLC 表示日志平台指标来源。
	CWMetricSourceKLC = "klc"
)

// CWStrategySpec 对齐 Kingeye BaseStrategySpec，并保留 CloudStrategySpec 的扩展字段。
type CWStrategySpec struct {
	ConfigType               string                      `json:"config_type"`
	TableID                  string                      `json:"table_id"`
	FieldName                string                      `json:"field_name"`
	ItemName                 string                      `json:"item_name"`
	Description              string                      `json:"description"`
	MonitorItemType          CWMonitorItemType           `json:"monitor_item_type"`
	MetricSource             string                      `json:"metric_source"`
	SourceConfig             domain.JSONObject           `json:"source_config"`
	DataSource               string                      `json:"data_source"`
	FieldTag                 CWStrategyFieldTag          `json:"field_tag"`
	AliasName                string                      `json:"alias_name"`
	Name                     string                      `json:"name"`
	StrategyItem             *CWStrategyItem             `json:"strategy_item"`
	StrategyDetectAlgorithms []CWStrategyDetectAlgorithm `json:"strategy_detect_algorithms"`
	Enable                   *bool                       `json:"enable"`
	Targets                  json.RawMessage             `json:"targets"`
	AlarmAlias               string                      `json:"alarm_alias"`
	ParentStrategyID         *string                     `json:"parent_strategy_id"`
	ChildStrategyIDs         []string                    `json:"child_strategy_ids"`
	CloudID                  *int64                      `json:"cloud_id,omitempty"`
	CloudType                string                      `json:"cloud_type,omitempty"`
	CloudResourceType        string                      `json:"cloud_resource_type,omitempty"`
}

// CWStrategyItem 对齐 Kingeye StrategyItemSpec。
type CWStrategyItem struct {
	TriggerConfig   domain.JSONObject     `json:"trigger_config"`
	NoDataConfig    domain.JSONObject     `json:"no_data_config"`
	RecoveryConfig  domain.JSONObject     `json:"recovery_config"`
	AggregateMethod string                `json:"agg_method"`
	AggregatePeriod json.RawMessage       `json:"agg_interval"`
	AggregateFilter []domain.JSONObject   `json:"agg_condition"`
	AggregateBy     []string              `json:"agg_dimension"`
	Functions       json.RawMessage       `json:"functions"`
	QueryConfigs    []StrategyQueryConfig `json:"query_configs"`
	Connector       string                `json:"connector"`
	Expression      string                `json:"expression"`
	AlarmLevel      *int64                `json:"alarm_level"`
}

// CWStrategyDetectAlgorithm 对齐 Kingeye StrategyDetectAlgorithmSpec。
type CWStrategyDetectAlgorithm struct {
	AlgorithmType   string            `json:"algorithm_type"`
	AlgorithmConfig domain.JSONObject `json:"algorithm_config"`
	Level           json.RawMessage   `json:"level"`
	LevelStatus     string            `json:"level_status"`
}

// CWStrategyStatus 是鲸眼声明式策略 status 的类型化模型。
type CWStrategyStatus struct {
	Enable         *bool  `json:"enable"`
	BKStrategyID   int64  `json:"bk_strategy_id"`
	SyncStatus     string `json:"sync_status"`
	Message        string `json:"message"`
	SyncedSpecHash string `json:"synced_spec_hash"`
	SyncedAt       string `json:"synced_at"`
	DeleteStatus   string `json:"delete_status"`
}
