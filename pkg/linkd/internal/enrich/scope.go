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
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"

	"linkd/internal/domain"
	"linkd/internal/enrich/custom"
	"linkd/internal/enrich/models"
	"linkd/internal/onemodel"
)

// ErrInvalidDataSourceResponse 表示外部请求成功后返回的数据违反 Reader 契约。
var ErrInvalidDataSourceResponse = onemodel.ErrInvalidDataSourceResponse

// InstanceAttributeType 是 SDK 的类型化属性槽。
type InstanceAttributeType = onemodel.InstanceAttributeType

const (
	InstanceAttributeKeyword  = onemodel.InstanceAttributeKeyword
	InstanceAttributeLong     = onemodel.InstanceAttributeLong
	InstanceAttributeDouble   = onemodel.InstanceAttributeDouble
	InstanceAttributeBoolean  = onemodel.InstanceAttributeBoolean
	InstanceAttributeDatetime = onemodel.InstanceAttributeDatetime
	InstanceAttributeIP       = onemodel.InstanceAttributeIP
)

// InstanceAttributeFilter 是 SDK 属性条件。
type InstanceAttributeFilter = onemodel.InstanceAttributeFilter

// InstanceQuery 是 SDK 单实例查询。
type InstanceQuery = onemodel.InstanceQuery

// Instance 是 SDK 返回的统一实例。
type Instance = onemodel.Instance

// Sources 聚合 Enrichment 使用的窄只读数据源。
type Sources struct {
	// CMDB 与 Display 为自定义规则提供只读能力。
	CMDB            onemodel.Reader
	Display         custom.DisplayReader
	CWStrategy      CWStrategyReader
	Business        BusinessReader
	Metric          MetricReader
	Model           ModelReader
	OneModel        OneModelReader
	AlarmSource     AlarmSourceReader
	LogTheme        LogThemeReader
	CloudResource   CloudResourceReader
	K8s             K8sReader
	APMApplication  APMApplicationReader
	CollectConfig   CollectConfigReader
	CollectTopology CollectTopologyReader
	Uptime          UptimeReader
	UptimeNode      UptimeNodeReader
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

// CloudResourceReader 按租户、云平台、资源类型和资源实例读取云资源。
type CloudResourceReader interface {
	GetCloudResource(ctx context.Context, tenantID, cloudID, resourceType, instanceID string) (models.CloudResource, bool, error)
}

// LogThemeReader 按租户和日志主题 ID 读取 KLC 日志主题。
type LogThemeReader interface {
	GetLogTheme(ctx context.Context, tenantID string, themeID int64) (models.LogTheme, bool, error)
}

// AlarmSourceReader 按租户和告警源 ID 读取告警源名称。
type AlarmSourceReader interface {
	GetAlarmSourceName(ctx context.Context, tenantID, sourceID string) (string, bool, error)
}

// CollectConfigReader 按租户和采集任务身份读取声明式采集配置。
type CollectConfigReader interface {
	GetCollectConfig(ctx context.Context, tenantID, taskID string) (models.CollectConfig, bool, error)
}

// UptimeReader 按租户和任务身份读取拨测任务。
type UptimeReader interface {
	GetUptimeTask(ctx context.Context, tenantID, taskID string) (models.UptimeTask, bool, error)
}

// UptimeNodeReader 按租户和节点身份读取拨测节点。
type UptimeNodeReader interface {
	GetUptimeNode(ctx context.Context, tenantID, nodeID string) (models.UptimeNode, bool, error)
}

// CollectTopologyReader 读取采集对象关联主机及其业务拓扑。
type CollectTopologyReader interface {
	FindRelatedHost(ctx context.Context, tenantID, modelCode, instanceID, hostRelatedField string) (Instance, bool, error)
	FindHostTopology(ctx context.Context, tenantID, hostID string) (models.ResourceTopology, bool, error)
}

// APMApplicationReader 按租户和应用名称读取 APM 应用候选项。
type APMApplicationReader interface {
	FindAPMApplications(ctx context.Context, tenantID, name string) ([]models.APMApplication, error)
}

// K8sReader 按租户、对象模型和 K8s 维度读取统一实例身份。
// 生产实现由 OneModelReader 适配，查询统一实例存储的 kingeye_all_instance。
type K8sReader interface {
	FindK8sInstance(ctx context.Context, tenantID, modelCode string, dimensions domain.DimensionMap) (Instance, bool, error)
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

type logThemeResult struct {
	value models.LogTheme
	found bool
	err   error
}

// Model 是统一实例文档中携带的模型元数据投影。
type Model struct {
	TenantID  string
	ModelID   string
	ModelCode string
	Fields    map[string]any
}

// ModelReader 按模型代码读取统一实例文档携带的模型元数据。
type ModelReader interface {
	GetModelByCode(ctx context.Context, tenantID, modelCode string) (Model, bool, error)
}

type modelResult struct {
	value Model
	found bool
	err   error
}

type metricResult struct {
	value models.MetricMetadata
	found bool
	err   error
}

type collectConfigResult struct {
	value models.CollectConfig
	found bool
	err   error
}

type uptimeTaskResult struct {
	value models.UptimeTask
	found bool
	err   error
}

type uptimeNodeResult struct {
	value models.UptimeNode
	found bool
	err   error
}

type topologyResult struct {
	value models.ResourceTopology
	found bool
	err   error
}

type instanceResult struct {
	value Instance
	found bool
	err   error
}

// Scope 保存单次 Enrich 调用的只读 Alert 快照和请求内数据。
func (s *Scope) CloudResource(ctx context.Context, cloudID, resourceType, instanceID string) (models.CloudResource, bool, error) {
	if s.sources.CloudResource == nil {
		return models.CloudResource{}, false, fmt.Errorf("cloud resource reader is unavailable")
	}
	return s.sources.CloudResource.GetCloudResource(ctx, s.alert.BKTenantID, cloudID, resourceType, instanceID)
}

type Scope struct {
	original      domain.Alert
	alert         domain.Alert
	sources       Sources
	enrichContext *EnrichContext

	strategyOnce    sync.Once
	strategy        strategyResult
	businessOnce    sync.Once
	business        businessResult
	alarmSourceOnce sync.Once
	alarmSource     alarmSourceResult
	logThemes       map[int64]logThemeResult
	instances       map[string]instanceResult
	models          map[string]modelResult
	metrics         map[models.MetricLibraryQuery]metricResult
	collectConfigs  map[string]collectConfigResult
	uptimeTasks     map[string]uptimeTaskResult
	uptimeNodes     map[string]uptimeNodeResult
	relatedHosts    map[string]instanceResult
	topologies      map[string]topologyResult

	scenarioMu sync.Mutex
	scenarios  map[string]scenarioResult
}

// NewScope 从已规范化 Alert 创建隔离的调用上下文。
type scenarioResult struct {
	value any
	err   error
}

func NewScope(alert domain.Alert, sources Sources) (*Scope, error) {
	return newScope(alert, sources, false)
}

func newScope(alert domain.Alert, sources Sources, preview bool) (*Scope, error) {
	normalized := alert.Clone()
	var err error
	if !preview {
		normalized, err = alert.Normalize()
	}
	if err != nil {
		return nil, fmt.Errorf("create enrich scope: %w", err)
	}
	return &Scope{
		alert: normalized.Clone(), original: normalized.Clone(), sources: sources, enrichContext: &EnrichContext{},
		instances: make(map[string]instanceResult), models: make(map[string]modelResult),
		metrics: make(map[models.MetricLibraryQuery]metricResult), collectConfigs: make(map[string]collectConfigResult),
		uptimeTasks: make(map[string]uptimeTaskResult), uptimeNodes: make(map[string]uptimeNodeResult),
		relatedHosts: make(map[string]instanceResult), topologies: make(map[string]topologyResult), logThemes: make(map[int64]logThemeResult),
		scenarios: make(map[string]scenarioResult),
	}, nil
}

// Context 返回本次调用共享的类型化丰富上下文。
// 各 Processor 只能写入与其输出分组对应的 ResourceContext。
func (s *Scope) Context() *EnrichContext {
	return s.enrichContext
}

// Alert 为内置场景返回原始事实副本，保持其查询身份不受前序展示结果影响。
// 自定义规则通过 EffectiveAlert 显式读取前序补丁，两个视图都不共享可变字段。
func (s *Scope) Alert() domain.Alert {
	return s.original.Clone()
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
	query.TenantID = s.alert.BKTenantID
	if result, exists := s.metrics[query]; exists {
		return result.value, result.found, result.err
	}
	result := metricResult{}
	if s.sources.Metric == nil {
		result.err = fmt.Errorf("metric reader is unavailable")
	} else {
		result.value, result.found, result.err = s.sources.Metric.FindMetricLibrary(ctx, query)
	}
	if ctx.Err() == nil {
		s.metrics[query] = result
	}
	return result.value, result.found, result.err
}

// APMApplications 按名称读取租户内 APM 应用候选项。
func (s *Scope) APMApplications(ctx context.Context, name string) ([]models.APMApplication, error) {
	if s.sources.APMApplication == nil {
		return nil, fmt.Errorf("apm application reader is unavailable")
	}
	return s.sources.APMApplication.FindAPMApplications(ctx, s.alert.BKTenantID, name)
}

// K8sInstance 读取 K8s 对象模型对应的统一实例。
func (s *Scope) K8sInstance(ctx context.Context, modelCode string, dimensions domain.DimensionMap) (Instance, bool, error) {
	if s.sources.K8s == nil {
		return Instance{}, false, fmt.Errorf("k8s reader is unavailable")
	}
	return s.sources.K8s.FindK8sInstance(ctx, s.alert.BKTenantID, modelCode, dimensions)
}

// LogTheme 惰性读取并复用本次调用的日志主题结果。
func (s *Scope) LogTheme(ctx context.Context, themeID int64) (models.LogTheme, bool, error) {
	if result, exists := s.logThemes[themeID]; exists {
		return result.value, result.found, result.err
	}
	result := logThemeResult{}
	if s.sources.LogTheme == nil {
		result.err = fmt.Errorf("log theme reader is unavailable")
	} else {
		result.value, result.found, result.err = s.sources.LogTheme.GetLogTheme(ctx, s.alert.BKTenantID, themeID)
	}
	if ctx.Err() == nil {
		s.logThemes[themeID] = result
	}
	return result.value, result.found, result.err
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

// Scenario 在一次丰富调用内缓存场景解析结果，供 Resource 与 Display 共享。
func (s *Scope) Scenario(key string, load func() (any, error)) (any, error) {
	s.scenarioMu.Lock()
	defer s.scenarioMu.Unlock()
	if result, exists := s.scenarios[key]; exists {
		return result.value, result.err
	}
	value, err := load()
	// 父 Context 取消意味着当前解析未完成；同一 Scope 的后续调用必须能重试，
	// 否则 Resource 的取消会污染 Display 或重试的场景结果。
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		s.scenarios[key] = scenarioResult{value: value, err: err}
	}
	return value, err
}

// CollectConfig 读取采集任务配置。
func (s *Scope) CollectConfig(ctx context.Context, taskID string) (models.CollectConfig, bool, error) {
	if result, exists := s.collectConfigs[taskID]; exists {
		return result.value, result.found, result.err
	}
	result := collectConfigResult{}
	if s.sources.CollectConfig == nil {
		result.err = fmt.Errorf("collect config reader is unavailable")
	} else {
		result.value, result.found, result.err = s.sources.CollectConfig.GetCollectConfig(ctx, s.alert.BKTenantID, taskID)
	}
	if ctx.Err() == nil {
		s.collectConfigs[taskID] = result
	}
	return result.value, result.found, result.err
}

// UptimeTask 读取拨测任务。
func (s *Scope) UptimeTask(ctx context.Context, taskID string) (models.UptimeTask, bool, error) {
	if result, exists := s.uptimeTasks[taskID]; exists {
		return result.value, result.found, result.err
	}
	result := uptimeTaskResult{}
	if s.sources.Uptime == nil {
		result.err = fmt.Errorf("uptime reader is unavailable")
	} else {
		result.value, result.found, result.err = s.sources.Uptime.GetUptimeTask(ctx, s.alert.BKTenantID, taskID)
	}
	if ctx.Err() == nil {
		s.uptimeTasks[taskID] = result
	}
	return result.value, result.found, result.err
}

// UptimeNode 读取拨测节点。
func (s *Scope) UptimeNode(ctx context.Context, nodeID string) (models.UptimeNode, bool, error) {
	if result, exists := s.uptimeNodes[nodeID]; exists {
		return result.value, result.found, result.err
	}
	result := uptimeNodeResult{}
	if s.sources.UptimeNode == nil {
		result.err = fmt.Errorf("uptime node reader is unavailable")
	} else {
		result.value, result.found, result.err = s.sources.UptimeNode.GetUptimeNode(ctx, s.alert.BKTenantID, nodeID)
	}
	if ctx.Err() == nil {
		s.uptimeNodes[nodeID] = result
	}
	return result.value, result.found, result.err
}

// ModelByCode 读取统一实例协议中的模型元数据。
// 当前 OneModelReader 可选实现 ModelReader；统一实例 alias 只保证 model_id 时返回代码级模型身份。
func (s *Scope) ModelByCode(ctx context.Context, modelCode string) (Model, bool, error) {
	if result, exists := s.models[modelCode]; exists {
		return result.value, result.found, result.err
	}
	result := modelResult{}
	if s.sources.Model == nil {
		result.err = fmt.Errorf("onemodel model reader is unavailable")
	} else {
		result.value, result.found, result.err = s.sources.Model.GetModelByCode(ctx, s.alert.BKTenantID, modelCode)
	}
	if ctx.Err() == nil {
		s.models[modelCode] = result
	}
	return result.value, result.found, result.err
}

// RelatedHost 读取采集对象关联的主机实例。
func (s *Scope) RelatedHost(ctx context.Context, modelCode, instanceID, relation string) (Instance, bool, error) {
	key := modelCode + "\x00" + instanceID + "\x00" + relation
	if result, exists := s.relatedHosts[key]; exists {
		return result.value, result.found, result.err
	}
	result := instanceResult{}
	if s.sources.CollectTopology == nil {
		result.err = fmt.Errorf("collect topology reader is unavailable")
	} else {
		result.value, result.found, result.err = s.sources.CollectTopology.FindRelatedHost(
			ctx, s.alert.BKTenantID, modelCode, instanceID, relation,
		)
	}
	if ctx.Err() == nil {
		s.relatedHosts[key] = result
	}
	return result.value, result.found, result.err
}

// HostTopology 读取主机业务拓扑。
func (s *Scope) HostTopology(ctx context.Context, hostID string) (models.ResourceTopology, bool, error) {
	if result, exists := s.topologies[hostID]; exists {
		return result.value, result.found, result.err
	}
	result := topologyResult{}
	if s.sources.CollectTopology == nil {
		result.err = fmt.Errorf("collect topology reader is unavailable")
	} else {
		result.value, result.found, result.err = s.sources.CollectTopology.FindHostTopology(ctx, s.alert.BKTenantID, hostID)
	}
	if ctx.Err() == nil {
		s.topologies[hostID] = result
	}
	return result.value, result.found, result.err
}

func instanceQueryKey(query InstanceQuery) (string, error) {
	filters := append([]InstanceAttributeFilter(nil), query.AttributeFilters...)
	sort.Slice(filters, func(i, j int) bool {
		if filters[i].Field != filters[j].Field {
			return filters[i].Field < filters[j].Field
		}
		if filters[i].Type != filters[j].Type {
			return filters[i].Type < filters[j].Type
		}
		left, _ := json.Marshal(filters[i].Value)
		right, _ := json.Marshal(filters[j].Value)
		return string(left) < string(right)
	})
	payload := struct {
		ModelCode  string                    `json:"model_code"`
		InstanceID string                    `json:"instance_id"`
		Filters    []InstanceAttributeFilter `json:"filters"`
	}{ModelCode: query.ModelCode, InstanceID: query.InstanceID, Filters: filters}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode instance query key: %w", err)
	}
	return string(encoded), nil
}

// Instance 按稳定查询身份复用本次调用定位到的 OneModel 实例。
func (s *Scope) Instance(ctx context.Context, query InstanceQuery) (Instance, bool, error) {
	key, err := instanceQueryKey(query)
	if err != nil {
		return Instance{}, false, err
	}
	if result, exists := s.instances[key]; exists {
		return result.value, result.found, result.err
	}
	result := instanceResult{}
	if s.sources.OneModel == nil {
		result.err = fmt.Errorf("onemodel reader is unavailable")
	} else {
		result.value, result.found, result.err = s.sources.OneModel.FindInstance(ctx, s.alert.BKTenantID, query)
	}
	if ctx.Err() == nil {
		s.instances[key] = result
	}
	return result.value, result.found, result.err
}

// OriginalAlert 返回本次执行前的来源事实，不包含前序补丁。
func (s *Scope) OriginalAlert() domain.Alert { return s.original.Clone() }

// RuleSources 返回规则引擎的显式只读端口。
func (s *Scope) RuleSources() custom.Sources {
	return custom.Sources{Instances: s.sources.CMDB, Display: s.sources.Display}
}

func (s *Scope) applyPatches(patches []domain.EnrichPatch) error {
	document, err := domain.AlertDocument(s.alert)
	if err != nil {
		return err
	}
	document, err = domain.ApplyEnrichPatches(document, patches)
	if err != nil {
		return err
	}
	data, err := json.Marshal(document)
	if err != nil {
		return err
	}
	var alert domain.Alert
	if err := json.Unmarshal(data, &alert); err != nil {
		return err
	}
	alert.Enrich = s.alert.Enrich
	alert.EnrichStatus = s.alert.EnrichStatus
	s.alert = alert
	return nil
}

// EffectiveAlert 返回应用本轮前序补丁后的隔离副本，供自定义规则顺序取值。
func (s *Scope) EffectiveAlert() domain.Alert { return s.alert.Clone() }
