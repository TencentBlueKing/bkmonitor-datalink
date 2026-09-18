// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package enrich

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"linkd/internal/domain"
	"linkd/internal/lifecycle/enrich/models"
)

// ErrInvalidDataSourceResponse 表示外部请求成功后返回的数据违反 Reader 契约。
var ErrInvalidDataSourceResponse = errors.New("invalid enrich datasource response")

// InstanceAttributeType 是 OneModel attribute_values 使用的固定类型槽。
type InstanceAttributeType string

const (
	InstanceAttributeKeyword  InstanceAttributeType = "keyword"
	InstanceAttributeLong     InstanceAttributeType = "long"
	InstanceAttributeDouble   InstanceAttributeType = "double"
	InstanceAttributeBoolean  InstanceAttributeType = "boolean"
	InstanceAttributeDatetime InstanceAttributeType = "datetime"
	InstanceAttributeIP       InstanceAttributeType = "ip"
)

// InstanceAttributeFilter 描述一个已由调用方确定类型的 OneModel 动态属性过滤条件。
type InstanceAttributeFilter struct {
	Field string
	Type  InstanceAttributeType
	Value any
}

// InstanceQuery 描述 OneModel 实例存储的一次单实例查询。
// InstanceID 使用根字段 model_inst_id；AttributeFilters 使用 nested attribute_values。
type InstanceQuery struct {
	ModelCode        string
	InstanceID       string
	AttributeFilters []InstanceAttributeFilter
}

// Instance 是 OneModel 实例存储返回的统一实例文档。
// Fields 保存根字段，Attributes 保存来源原始属性；业务消费方按根字段优先合并读取。
type Instance struct {
	TenantID   string
	ModelCode  string
	InstanceID string
	Fields     map[string]any
	Attributes map[string]any
}

// Sources 聚合 Enrichment 使用的窄只读数据源。
type Sources struct {
	CWStrategy  CWStrategyReader
	Business    BusinessReader
	Metric      MetricReader
	OneModel    OneModelReader
	AlarmSource AlarmSourceReader
	// Test 仅用于显式启用的测试处理器。
	Test TestSource
}

// CWStrategyReader 按全租户唯一的关联 ID 读取鲸眼声明式策略。
type CWStrategyReader interface {
	GetByBKStrategyID(ctx context.Context, tenantID string, bkStrategyID int64) (models.CWStrategy, bool, error)
}

// BusinessReader 读取租户内 BKCC 业务空间的全局属性。
type BusinessReader interface {
	IsGlobalBusiness(ctx context.Context, tenantID string, bizID int64) (bool, bool, error)
}

// MetricReader 只读取 Kingeye MonitorMetricLibrary；MonitorMetric 表已经废弃。
type MetricReader interface {
	FindMetricLibrary(ctx context.Context, query models.MetricLibraryQuery) (models.MetricMetadata, bool, error)
}

// AlarmSourceReader 按租户和告警源 ID 读取告警源名称。
type AlarmSourceReader interface {
	GetAlarmSourceName(ctx context.Context, tenantID, sourceID string) (string, bool, error)
}

// OneModelReader 按显式租户、模型、实例身份或类型化属性读取统一实例存储。
type OneModelReader interface {
	FindInstance(ctx context.Context, tenantID string, query InstanceQuery) (Instance, bool, error)
}

type strategyResult struct {
	value models.CWStrategy
	found bool
	err   error
}

type businessResult struct {
	isGlobal bool
	found    bool
	err      error
}

type alarmSourceResult struct {
	name  string
	found bool
	err   error
}

type instanceResult struct {
	value Instance
	found bool
	err   error
}

// Scope 保存单次 Enrich 调用的只读 Alert 快照和请求内数据。
type Scope struct {
	alert         domain.Alert
	sources       Sources
	enrichContext *EnrichContext

	strategyOnce    sync.Once
	strategy        strategyResult
	businessOnce    sync.Once
	business        businessResult
	alarmSourceOnce sync.Once
	alarmSource     alarmSourceResult
	instanceOnce    sync.Once
	instance        instanceResult
}

// NewScope 从已规范化 Alert 创建隔离的调用上下文。
func NewScope(alert domain.Alert, sources Sources) (*Scope, error) {
	normalized, err := alert.Normalize()
	if err != nil {
		return nil, fmt.Errorf("create enrich scope: %w", err)
	}
	return &Scope{
		alert: normalized.Clone(), sources: sources, enrichContext: &EnrichContext{},
	}, nil
}

// Context 返回本次调用共享的类型化丰富上下文。
// 各 Processor 只能写入与其输出分组对应的 ResourceContext。
func (s *Scope) Context() *EnrichContext {
	return s.enrichContext
}

// Alert 返回隔离副本，Processor 对动态字段的修改不会影响 Scope 或调用方。
func (s *Scope) Alert() domain.Alert {
	return s.alert.Clone()
}

// CWStrategyByBKStrategyID 惰性读取并复用按平台策略 ID 关联的鲸眼声明式策略。
func (s *Scope) CWStrategyByBKStrategyID(ctx context.Context, strategyID int64) (models.CWStrategy, bool, error) {
	s.strategyOnce.Do(func() {
		if s.sources.CWStrategy == nil {
			s.strategy.err = fmt.Errorf("strategy config reader is unavailable")
			return
		}
		s.strategy.value, s.strategy.found, s.strategy.err = s.sources.CWStrategy.GetByBKStrategyID(ctx, s.alert.BKTenantID, strategyID)
	})
	return s.strategy.value, s.strategy.found, s.strategy.err
}

// IsGlobalBusiness 惰性读取策略所属 BKCC 业务是否为全局业务。
func (s *Scope) IsGlobalBusiness(ctx context.Context, bizID int64) (bool, bool, error) {
	s.businessOnce.Do(func() {
		if s.sources.Business == nil {
			s.business.err = fmt.Errorf("business reader is unavailable")
			return
		}
		s.business.isGlobal, s.business.found, s.business.err = s.sources.Business.IsGlobalBusiness(
			ctx,
			s.alert.BKTenantID,
			bizID,
		)
	})
	return s.business.isGlobal, s.business.found, s.business.err
}

// MetricLibrary 惰性读取并复用本次调用的指标库结果。
// MonitorMetric 表已经废弃，指标展示名称统一通过该入口查询 MetricLibrary。
func (s *Scope) MetricLibrary(ctx context.Context, query models.MetricLibraryQuery) (models.MetricMetadata, bool, error) {
	if s.sources.Metric == nil {
		return models.MetricMetadata{}, false, fmt.Errorf("metric reader is unavailable")
	}
	query.TenantID = s.alert.BKTenantID
	return s.sources.Metric.FindMetricLibrary(ctx, query)
}

// AlarmSourceName 惰性读取并复用本次调用的告警源名称。
func (s *Scope) AlarmSourceName(ctx context.Context) (string, bool, error) {
	s.alarmSourceOnce.Do(func() {
		if s.sources.AlarmSource == nil {
			s.alarmSource.err = fmt.Errorf("alarm source reader is unavailable")
			return
		}
		s.alarmSource.name, s.alarmSource.found, s.alarmSource.err = s.sources.AlarmSource.GetAlarmSourceName(
			ctx,
			s.alert.BKTenantID,
			s.alert.EventSourceID,
		)
	})
	return s.alarmSource.name, s.alarmSource.found, s.alarmSource.err
}

// Instance 惰性读取并复用本次调用定位到的 OneModel 实例。
// 当前 Resource Processor 每次丰富只定位一个实例，因此 Scope 缓存首次查询及其结果。
func (s *Scope) Instance(ctx context.Context, query InstanceQuery) (Instance, bool, error) {
	s.instanceOnce.Do(func() {
		if s.sources.OneModel == nil {
			s.instance.err = fmt.Errorf("onemodel reader is unavailable")
			return
		}
		s.instance.value, s.instance.found, s.instance.err = s.sources.OneModel.FindInstance(
			ctx,
			s.alert.BKTenantID,
			query,
		)
	})
	return s.instance.value, s.instance.found, s.instance.err
}
