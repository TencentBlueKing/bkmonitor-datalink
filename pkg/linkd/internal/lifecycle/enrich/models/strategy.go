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
	"time"

	"linkd/internal/domain"
)

// BkStrategyHistory 对应 alarm_strategy_history 中的一条蓝鲸监控策略历史记录。
// Content 保留完整快照；Snapshot 只提供当前丰富流程需要的类型化读取视图。
type BkStrategyHistory struct {
	ID         int64
	StrategyID int64
	Content    domain.JSONObject
}

// Snapshot 将完整平台策略快照解码为当前丰富流程使用的类型化视图。
func (h BkStrategyHistory) Snapshot() (BkStrategySnapshot, error) {
	data, err := json.Marshal(h.Content)
	if err != nil {
		return BkStrategySnapshot{}, fmt.Errorf("marshal bk strategy history content: %w", err)
	}
	var snapshot BkStrategySnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return BkStrategySnapshot{}, fmt.Errorf("decode bk strategy history content: %w", err)
	}
	return snapshot, nil
}

// BkStrategySnapshot 是 alarm_strategy_history.content 的类型化读取视图。
type BkStrategySnapshot struct {
	ID        int64            `json:"id"`
	BKBizID   int64            `json:"bk_biz_id"`
	Name      string           `json:"name"`
	Source    string           `json:"source"`
	Scenario  string           `json:"scenario"`
	Type      string           `json:"type"`
	IsEnabled bool             `json:"is_enabled"`
	IsInvalid bool             `json:"is_invalid"`
	Items     []BkStrategyItem `json:"items"`
}

// BkStrategyItem 是蓝鲸监控策略快照中的单个监控项。
type BkStrategyItem struct {
	ID           int64                   `json:"id"`
	Name         string                  `json:"name"`
	Expression   string                  `json:"expression"`
	Functions    json.RawMessage         `json:"functions"`
	QueryConfigs []BkStrategyQueryConfig `json:"query_configs"`
}

// BkStrategyQueryConfig 是蓝鲸监控策略监控项中的单条查询配置。
type BkStrategyQueryConfig struct {
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
func (q *BkStrategyQueryConfig) UnmarshalJSON(data []byte) error {
	type queryConfig BkStrategyQueryConfig
	var typed queryConfig
	if err := json.Unmarshal(data, &typed); err != nil {
		return err
	}
	var raw domain.JSONObject
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*q = BkStrategyQueryConfig(typed)
	q.Raw = raw
	return nil
}

// BkStrategy 对应 alarm_strategy_v2 中的蓝鲸监控当前策略记录。
type BkStrategy struct {
	ID               int64
	Name             string
	BKBizID          int64
	Source           string
	Scenario         string
	Type             string
	IsEnabled        bool
	IsInvalid        bool
	InvalidType      string
	CreateUser       string
	CreateTime       time.Time
	UpdateUser       string
	UpdateTime       time.Time
	App              *string
	Path             *string
	Hash             *string
	Snippet          *string
	Priority         *int64
	PriorityGroupKey *string
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
	TriggerConfig   domain.JSONObject   `json:"trigger_config"`
	NoDataConfig    domain.JSONObject   `json:"no_data_config"`
	RecoveryConfig  domain.JSONObject   `json:"recovery_config"`
	AggregateMethod string              `json:"agg_method"`
	AggregatePeriod json.RawMessage     `json:"agg_interval"`
	AggregateFilter []domain.JSONObject `json:"agg_condition"`
	AggregateBy     []string            `json:"agg_dimension"`
	Functions       json.RawMessage     `json:"functions"`
	QueryConfigs    []domain.JSONObject `json:"query_configs"`
	Connector       string              `json:"connector"`
	Expression      string              `json:"expression"`
	AlarmLevel      *int64              `json:"alarm_level"`
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
