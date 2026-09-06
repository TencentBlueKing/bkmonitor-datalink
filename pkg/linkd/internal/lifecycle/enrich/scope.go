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
	"fmt"
	"sync"

	"linkd/internal/domain"
	"linkd/internal/lifecycle/enrich/models"
)

// InstanceQuery 描述 OneModel 实例存储的一次单实例查询。
// Filters 使用 ES 文档的扁平字段名；Client 会强制追加租户和模型过滤。
type InstanceQuery struct {
	ModelCode string
	Filters   map[string]any
}

// Instance 是 OneModel 实例存储返回的扁平文档。
type Instance struct {
	TenantID   string
	ModelCode  string
	InstanceID string
	Fields     map[string]any
}

// Sources 聚合 Enrichment 使用的窄只读数据源。
type Sources struct {
	BKStrategy  BKStrategyReader
	CWStrategy  CWStrategyReader
	Metric      MetricReader
	OneModel    OneModelReader
	AlarmSource AlarmSourceReader
}

// BKStrategyReader 读取全租户唯一 ID 标识的蓝鲸监控策略与指定历史快照。
type BKStrategyReader interface {
	GetStrategyHistory(ctx context.Context, strategyID, historyID int64) (models.BkStrategyHistory, bool, error)
	GetStrategy(ctx context.Context, strategyID int64) (models.BkStrategy, bool, error)
}

// CWStrategyReader 按全租户唯一的关联 ID 读取鲸眼声明式策略。
type CWStrategyReader interface {
	GetByBKStrategyID(ctx context.Context, bkStrategyID int64) (models.CWStrategy, bool, error)
	GetByMonitorTemplateID(ctx context.Context, monitorTemplateID int64) (models.CWStrategy, bool, error)
}

// MetricReader 只读取 Kingeye MonitorMetricLibrary；MonitorMetric 表已经废弃。
type MetricReader interface {
	FindMetricLibrary(ctx context.Context, query models.MetricLibraryQuery) (models.MetricMetadata, bool, error)
}

// AlarmSourceReader 按租户和告警源 ID 读取告警源名称。
type AlarmSourceReader interface {
	GetAlarmSourceName(ctx context.Context, tenantID, sourceID string) (string, bool, error)
}

// OneModelReader 按显式租户、模型和业务字段读取统一实例存储。
type OneModelReader interface {
	FindInstance(ctx context.Context, tenantID string, query InstanceQuery) (Instance, bool, error)
}

type platformResult struct {
	value models.BkStrategyHistory
	found bool
	err   error
}

type strategyResult struct {
	value models.CWStrategy
	found bool
	err   error
}

type bkStrategyResult struct {
	value models.BkStrategy
	found bool
	err   error
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

	platformOnce        sync.Once
	platform            platformResult
	currentStrategyOnce sync.Once
	currentStrategy     bkStrategyResult
	strategyOnce        sync.Once
	strategy            strategyResult
	alarmSourceOnce     sync.Once
	alarmSource         alarmSourceResult
	instanceOnce        sync.Once
	instance            instanceResult
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

// BKStrategyHistory 惰性读取并复用本次调用的蓝鲸监控策略历史结果。
func (s *Scope) BKStrategyHistory(ctx context.Context, strategyID, historyID int64) (models.BkStrategyHistory, bool, error) {
	s.platformOnce.Do(func() {
		if s.sources.BKStrategy == nil {
			s.platform.err = fmt.Errorf("bk strategy history reader is unavailable")
			return
		}
		s.platform.value, s.platform.found, s.platform.err = s.sources.BKStrategy.GetStrategyHistory(ctx, strategyID, historyID)
	})
	return s.platform.value, s.platform.found, s.platform.err
}

// BKStrategy 惰性读取并复用本次调用的蓝鲸监控当前策略。
func (s *Scope) BKStrategy(ctx context.Context, strategyID int64) (models.BkStrategy, bool, error) {
	s.currentStrategyOnce.Do(func() {
		if s.sources.BKStrategy == nil {
			s.currentStrategy.err = fmt.Errorf("bk strategy reader is unavailable")
			return
		}
		s.currentStrategy.value, s.currentStrategy.found, s.currentStrategy.err = s.sources.BKStrategy.GetStrategy(ctx, strategyID)
	})
	return s.currentStrategy.value, s.currentStrategy.found, s.currentStrategy.err
}

// CWStrategyByBKStrategyID 惰性读取并复用按平台策略 ID 关联的鲸眼声明式策略。
func (s *Scope) CWStrategyByBKStrategyID(ctx context.Context, strategyID int64) (models.CWStrategy, bool, error) {
	s.strategyOnce.Do(func() {
		if s.sources.CWStrategy == nil {
			s.strategy.err = fmt.Errorf("strategy config reader is unavailable")
			return
		}
		s.strategy.value, s.strategy.found, s.strategy.err = s.sources.CWStrategy.GetByBKStrategyID(ctx, strategyID)
	})
	return s.strategy.value, s.strategy.found, s.strategy.err
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
