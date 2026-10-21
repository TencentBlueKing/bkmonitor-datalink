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
	"linkd/internal/enrich/description"
	"linkd/internal/enrich/models"
	"linkd/internal/onemodel"
)

// ErrInvalidDataSourceResponse 表示外部请求成功后返回的数据违反 Reader 契约。
var ErrInvalidDataSourceResponse = onemodel.ErrInvalidDataSourceResponse

// ErrAmbiguousMetricLocator 表示无主键的指标目录定位命中多个候选，不能任选其一。
// 它仍属于无效依赖响应；可选反查的调用方可以据此放弃目录补充。
var ErrAmbiguousMetricLocator = fmt.Errorf("%w: metric locator is ambiguous", ErrInvalidDataSourceResponse)

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
	CMDB       onemodel.Reader
	Display    custom.DisplayReader
	CWStrategy CWStrategyReader
	// DescriptionConfiguration 校验触发时发布绑定，不能替代为只读当前策略的 CWStrategy。
	DescriptionConfiguration description.ConfigurationReader
	Business                 BusinessReader
	Metric                   MetricReader
	Model                    ModelReader
	OneModel                 OneModelReader
	AlarmSource              AlarmSourceReader
	LogTheme                 LogThemeReader
	CloudResource            CloudResourceReader
	K8s                      K8sReader
	APMApplication           APMApplicationReader
	CollectConfig            CollectConfigReader
	CollectTopology          CollectTopologyReader
	DynamicGroup             DynamicGroupReader
	Uptime                   UptimeReader
	UptimeNode               UptimeNodeReader
	// Test 仅用于显式启用的测试处理器。
	Test TestSource
}

// DynamicGroupReader 按显式租户和 canonical 模型实例身份读取预先物化的分组归属。
type DynamicGroupReader interface {
	GetDynamicGroupIDs(ctx context.Context, tenantID, modelCode, instanceID string) ([]string, error)
}

// CWStrategyReader 按租户、策略 ID 和事件版本读取不可变历史快照。
type CWStrategyReader interface {
	GetByStrategyID(ctx context.Context, query models.StrategyRevisionQuery) (models.CWStrategy, bool, error)
}

// BusinessReader 读取租户内 BKCC 业务空间的全局属性。
type BusinessReader interface {
	IsGlobalBusiness(ctx context.Context, tenantID string, bizID int64) (bool, bool, error)
}

// MetricReader 只读取 Kingeye 指标目录的元数据，不执行时序取数。
type MetricReader interface {
	FindMetric(ctx context.Context, query models.MetricQuery) (models.MetricMetadata, bool, error)
}

// CloudResourceReader 按租户、云平台、资源类型和资源实例读取云资源。
type CloudResourceReader interface {
	GetCloudResource(ctx context.Context, tenantID, cloudID, resourceType, instanceID string) (models.CloudResource, bool, error)
}

// LogThemeReader 按租户和日志主题 ID 读取 KLC 日志主题。
type LogThemeReader interface {
	GetLogTheme(ctx context.Context, tenantID string, themeID int64) (models.LogTheme, bool, error)
}

// AlarmSourceReader 按租户和 Linkd EventSourceID 读取 KAC 告警源。
type AlarmSourceReader interface {
	GetAlarmSource(ctx context.Context, tenantID, sourceID string) (models.AlarmSource, bool, error)
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

// APMApplicationReader 按租户、业务和应用名称读取 APM 应用候选项。
type APMApplicationReader interface {
	FindAPMApplications(ctx context.Context, tenantID string, bizID int64, name string) ([]models.APMApplication, error)
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
	value models.AlarmSource
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

type apmApplicationQuery struct {
	bizID int64
	name  string
}

type apmApplicationResult struct {
	values []models.APMApplication
	err    error
}

// Scope 保存单次 Enrich 调用的只读 Event 快照和请求内数据。
func (s *Scope) CloudResource(ctx context.Context, cloudID, resourceType, instanceID string) (returned models.CloudResource, found bool, err error) {
	defer func() { s.recordDependencyError("cloud_resource", err) }()
	if s.sources.CloudResource == nil {
		return models.CloudResource{}, false, fmt.Errorf("cloud resource reader is unavailable")
	}
	key, err := queryCacheKey("cloud", s.original.BKTenantID, cloudID, resourceType, instanceID)
	if err != nil {
		return models.CloudResource{}, false, err
	}
	value, err := s.Scenario(key, func() (any, error) {
		v, found, err := s.sources.CloudResource.GetCloudResource(ctx, s.original.BKTenantID, cloudID, resourceType, instanceID)
		return struct {
			Value models.CloudResource
			Found bool
		}{v, found}, err
	})
	if err != nil {
		return models.CloudResource{}, false, err
	}
	result := value.(struct {
		Value models.CloudResource
		Found bool
	})
	return result.Value, result.Found, nil
}

type Scope struct {
	degradedDiagnostics []Diagnostic
	dependencyErrors    []error
	dependencies        []string
	original            *domain.Event
	state               *domain.EnrichEvaluation
	running             bool
	evaluation          domain.EventEvaluation
	sources             Sources
	enrichContext       *EnrichContext

	*requestCache
}

// requestCache 只在一条 Event 的串行 evaluation 之间共享，不跨 Event 或并发链复用。
type requestCache struct {
	strategyOnce    sync.Once
	strategy        strategyResult
	businessOnce    sync.Once
	business        businessResult
	alarmSourceOnce sync.Once
	alarmSource     alarmSourceResult
	logThemes       map[int64]logThemeResult
	instances       map[string]instanceResult
	models          map[string]modelResult
	metrics         map[models.MetricQuery]metricResult
	apmApplications map[apmApplicationQuery]apmApplicationResult
	collectConfigs  map[string]collectConfigResult
	uptimeTasks     map[string]uptimeTaskResult
	uptimeNodes     map[string]uptimeNodeResult
	relatedHosts    map[string]instanceResult
	topologies      map[string]topologyResult

	scenarioMu sync.Mutex
	scenarios  map[string]scenarioResult
}

type scenarioResult struct {
	value any
	err   error
}

// NewScope 创建单 evaluation Event 的隔离上下文；多等级输入必须由 Chain 逐级执行。
func NewScope(event domain.Event, sources Sources) (*Scope, error) {
	if len(event.Evaluations) != 1 {
		return nil, fmt.Errorf("standalone scope requires exactly one evaluation")
	}
	return newScope(event, event.Evaluations[0], sources, false, newRequestCache())
}

func newRequestCache() *requestCache {
	return &requestCache{
		instances: make(map[string]instanceResult), models: make(map[string]modelResult),
		metrics: make(map[models.MetricQuery]metricResult), collectConfigs: make(map[string]collectConfigResult),
		apmApplications: make(map[apmApplicationQuery]apmApplicationResult),
		uptimeTasks:     make(map[string]uptimeTaskResult), uptimeNodes: make(map[string]uptimeNodeResult),
		relatedHosts: make(map[string]instanceResult), topologies: make(map[string]topologyResult), logThemes: make(map[int64]logThemeResult),
		scenarios: make(map[string]scenarioResult),
	}
}

func newScope(event domain.Event, evaluation domain.EventEvaluation, sources Sources, preview bool, cache *requestCache) (*Scope, error) {
	normalized := event
	// 历史丰富只用于预览对比，不能成为新一轮处理器的来源事实。
	normalized.EventEnrichment = domain.EventEnrichment{EnrichStatus: domain.EnrichStatusPending}
	if !preview {
		if err := event.ValidateIdentity(); err != nil {
			return nil, fmt.Errorf("create enrich scope: %w", err)
		}
	}
	owned := normalized.Clone()
	return newOwnedScope(&owned, evaluation, sources, cache), nil
}

func newOwnedScope(event *domain.Event, evaluation domain.EventEvaluation, sources Sources, cache *requestCache) *Scope {
	return &Scope{original: event, evaluation: evaluation, sources: sources, enrichContext: &EnrichContext{}, requestCache: cache}
}

// Evaluation 返回本轮处理的明确等级和动作，不从 Event 隐式推测严重性。
func (s *Scope) Evaluation() domain.EventEvaluation { return s.evaluation }

// Context 返回本次调用共享的类型化丰富上下文。
// 各 Processor 只能写入与其输出分组对应的 ResourceContext。
func (s *Scope) Context() *EnrichContext {
	return s.enrichContext
}

// Event 为内置场景返回原始事实副本，保持其查询身份不受前序展示结果影响。
// 自定义规则通过 EvaluationState 读取原始/当前查询视图；写入只经过受控补丁入口。
func (s *Scope) Event() domain.Event {
	return s.original.Clone()
}

// DynamicGroupIDs 只使用原始 Event 的租户身份；未配置投影时保持空列表。
func (s *Scope) DynamicGroupIDs(ctx context.Context, modelCode, instanceID string) (returned []string, err error) {
	defer func() { s.recordDependencyError("dynamic_group", err) }()
	if s.sources.DynamicGroup == nil {
		return []string{}, nil
	}
	return s.sources.DynamicGroup.GetDynamicGroupIDs(ctx, s.original.BKTenantID, modelCode, instanceID)
}

// CWStrategy 惰性读取并复用当前 Event 指定的策略历史快照。
func (s *Scope) CWStrategy(ctx context.Context) (returned models.CWStrategy, found bool, err error) {
	defer func() { s.recordDependencyError("strategy_publication", err) }()
	s.strategyOnce.Do(func() {
		if s.sources.CWStrategy == nil {
			s.strategy.err = fmt.Errorf("strategy config reader is unavailable")
			return
		}
		ids, diagnostics := ValidateRequiredIDs(*s.original)
		if len(diagnostics) != 0 {
			s.strategy.err = fmt.Errorf("strategy publication identity is invalid")
			return
		}
		s.strategy.value, s.strategy.found, s.strategy.err = s.sources.CWStrategy.GetByStrategyID(ctx, models.StrategyRevisionQuery{TenantID: s.original.BKTenantID, ID: ids.StrategyID, Version: ids.StrategyVersion})
	})
	return s.strategy.value, s.strategy.found, s.strategy.err
}

// IsGlobalBusiness 惰性读取策略所属 BKCC 业务是否为全局业务。
func (s *Scope) IsGlobalBusiness(ctx context.Context, bizID int64) (returned bool, found bool, err error) {
	defer func() { s.recordDependencyError("business", err) }()
	s.businessOnce.Do(func() {
		if s.sources.Business == nil {
			s.business.err = fmt.Errorf("business reader is unavailable")
			return
		}
		s.business.isGlobal, s.business.found, s.business.err = s.sources.Business.IsGlobalBusiness(
			ctx,
			s.original.BKTenantID,
			bizID,
		)
	})
	return s.business.isGlobal, s.business.found, s.business.err
}

// MetricMetadata 按事件租户和业务作用域读取并复用本次调用的指标目录结果。
func (s *Scope) MetricMetadata(ctx context.Context, query models.MetricQuery) (returned models.MetricMetadata, found bool, err error) {
	defer func() { s.recordDependencyError("metric_catalog", err) }()
	if err := ctx.Err(); err != nil {
		return models.MetricMetadata{}, false, err
	}
	query.TenantID = s.original.BKTenantID
	if query.MetricID > 0 {
		// ID 已完整表达身份，物理位置或模型冗余不参与点查与缓存键。
		query = models.MetricQuery{TenantID: query.TenantID, MetricID: query.MetricID}
	} else {
		facts := *s.original
		if s.state != nil {
			facts.Labels = s.state.Labels()
		}
		ids, diagnostics := ValidateRequiredIDs(facts)
		if len(diagnostics) != 0 {
			return models.MetricMetadata{}, false, fmt.Errorf("metric lookup requires valid source business")
		}
		query.SpaceUID = fmt.Sprintf("bkcc__%d", ids.BizID)
	}
	if result, exists := s.metrics[query]; exists {
		return result.value, result.found, result.err
	}
	result := metricResult{}
	if s.sources.Metric == nil {
		result.err = fmt.Errorf("metric reader is unavailable")
	} else {
		result.value, result.found, result.err = s.sources.Metric.FindMetric(ctx, query)
	}
	if ctx.Err() == nil {
		s.metrics[query] = result
	}
	return result.value, result.found, result.err
}

// OptionalMetricMetadata 只在无主键的目录反查多义时放弃补充。
// 原始错误仍由 MetricMetadata 记录，Chain 将当前 Processor 标为 partial 并保留
// metric_catalog 诊断；基础设施错误、取消和显式主键引用仍返回错误。
func (s *Scope) OptionalMetricMetadata(ctx context.Context, query models.MetricQuery) (models.MetricMetadata, bool, error) {
	metadata, found, err := s.MetricMetadata(ctx, query)
	if contextErr := ctx.Err(); contextErr != nil {
		return models.MetricMetadata{}, false, contextErr
	}
	if query.MetricID != 0 || !errors.Is(err, ErrAmbiguousMetricLocator) {
		return metadata, found, err
	}
	for _, diagnostic := range s.degradedDiagnostics {
		if diagnostic.Dependency == "metric_catalog" {
			return models.MetricMetadata{}, false, nil
		}
	}
	s.degradedDiagnostics = append(s.degradedDiagnostics, Diagnostic{Code: DiagnosticCodeDependencyInvalid, Dependency: "metric_catalog"})
	return models.MetricMetadata{}, false, nil
}

// APMApplications 按业务和名称读取租户内 APM 应用候选项，并在单次 Enrich 内复用结果。
func (s *Scope) APMApplications(ctx context.Context, bizID int64, name string) (returned []models.APMApplication, err error) {
	defer func() { s.recordDependencyError("apm_application", err) }()
	query := apmApplicationQuery{bizID: bizID, name: name}
	if result, exists := s.apmApplications[query]; exists {
		return append([]models.APMApplication(nil), result.values...), result.err
	}
	result := apmApplicationResult{}
	if s.sources.APMApplication == nil {
		result.err = fmt.Errorf("apm application reader is unavailable")
	} else {
		result.values, result.err = s.sources.APMApplication.FindAPMApplications(ctx, s.original.BKTenantID, bizID, name)
	}
	if ctx.Err() == nil {
		result.values = append([]models.APMApplication(nil), result.values...)
		s.apmApplications[query] = result
	}
	return append([]models.APMApplication(nil), result.values...), result.err
}

// K8sInstance 读取 K8s 对象模型对应的统一实例。
func (s *Scope) K8sInstance(ctx context.Context, modelCode string, dimensions domain.DimensionMap) (returned Instance, found bool, err error) {
	defer func() { s.recordDependencyError("k8s_instance", err) }()
	if s.sources.K8s == nil {
		return Instance{}, false, fmt.Errorf("k8s reader is unavailable")
	}
	key, err := queryCacheKey("k8s", s.original.BKTenantID, modelCode, dimensions)
	if err != nil {
		return Instance{}, false, err
	}
	value, err := s.Scenario(key, func() (any, error) {
		v, found, err := s.sources.K8s.FindK8sInstance(ctx, s.original.BKTenantID, modelCode, dimensions)
		return instanceResult{value: v, found: found}, err
	})
	if err != nil {
		return Instance{}, false, err
	}
	result := value.(instanceResult)
	return cloneQueryInstances([]Instance{result.value})[0], result.found, nil
}

// LogTheme 惰性读取并复用本次调用的日志主题结果。
func (s *Scope) LogTheme(ctx context.Context, themeID int64) (returned models.LogTheme, found bool, err error) {
	defer func() { s.recordDependencyError("log_theme", err) }()
	if result, exists := s.logThemes[themeID]; exists {
		return result.value, result.found, result.err
	}
	result := logThemeResult{}
	if s.sources.LogTheme == nil {
		result.err = fmt.Errorf("log theme reader is unavailable")
	} else {
		result.value, result.found, result.err = s.sources.LogTheme.GetLogTheme(ctx, s.original.BKTenantID, themeID)
	}
	if ctx.Err() == nil {
		s.logThemes[themeID] = result
	}
	return result.value, result.found, result.err
}

// AlarmSource 惰性读取并复用本次调用的告警源。
func (s *Scope) AlarmSource(ctx context.Context) (returned models.AlarmSource, found bool, err error) {
	defer func() { s.recordDependencyError("alarm_source", err) }()
	s.alarmSourceOnce.Do(func() {
		if s.sources.AlarmSource == nil {
			s.alarmSource.err = fmt.Errorf("alarm source reader is unavailable")
			return
		}
		s.alarmSource.value, s.alarmSource.found, s.alarmSource.err = s.sources.AlarmSource.GetAlarmSource(
			ctx,
			s.original.BKTenantID,
			s.original.EventSourceID,
		)
	})
	return s.alarmSource.value, s.alarmSource.found, s.alarmSource.err
}

// Scenario 在一次丰富调用内缓存场景解析结果，供 Resource 与 Display 共享。
func (s *Scope) Scenario(key string, load func() (any, error)) (any, error) {
	s.scenarioMu.Lock()
	defer s.scenarioMu.Unlock()
	if result, exists := s.scenarios[key]; exists {
		return result.value, result.err
	}
	if len(s.scenarios) >= 512 {
		return nil, fmt.Errorf("event enrich query budget exceeded")
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
func (s *Scope) CollectConfig(ctx context.Context, taskID string) (returned models.CollectConfig, found bool, err error) {
	defer func() { s.recordDependencyError("collect_config", err) }()
	if result, exists := s.collectConfigs[taskID]; exists {
		return result.value, result.found, result.err
	}
	result := collectConfigResult{}
	if s.sources.CollectConfig == nil {
		result.err = fmt.Errorf("collect config reader is unavailable")
	} else {
		result.value, result.found, result.err = s.sources.CollectConfig.GetCollectConfig(ctx, s.original.BKTenantID, taskID)
	}
	if ctx.Err() == nil {
		s.collectConfigs[taskID] = result
	}
	return result.value, result.found, result.err
}

// UptimeTask 读取拨测任务。
func (s *Scope) UptimeTask(ctx context.Context, taskID string) (returned models.UptimeTask, found bool, err error) {
	defer func() { s.recordDependencyError("uptime_task", err) }()
	if result, exists := s.uptimeTasks[taskID]; exists {
		return result.value, result.found, result.err
	}
	result := uptimeTaskResult{}
	if s.sources.Uptime == nil {
		result.err = fmt.Errorf("uptime reader is unavailable")
	} else {
		result.value, result.found, result.err = s.sources.Uptime.GetUptimeTask(ctx, s.original.BKTenantID, taskID)
	}
	if ctx.Err() == nil {
		s.uptimeTasks[taskID] = result
	}
	return result.value, result.found, result.err
}

// UptimeNode 读取拨测节点。
func (s *Scope) UptimeNode(ctx context.Context, nodeID string) (returned models.UptimeNode, found bool, err error) {
	defer func() { s.recordDependencyError("uptime_node", err) }()
	if result, exists := s.uptimeNodes[nodeID]; exists {
		return result.value, result.found, result.err
	}
	result := uptimeNodeResult{}
	if s.sources.UptimeNode == nil {
		result.err = fmt.Errorf("uptime node reader is unavailable")
	} else {
		result.value, result.found, result.err = s.sources.UptimeNode.GetUptimeNode(ctx, s.original.BKTenantID, nodeID)
	}
	if ctx.Err() == nil {
		s.uptimeNodes[nodeID] = result
	}
	return result.value, result.found, result.err
}

// ModelByCode 读取统一实例协议中的模型元数据。
// 当前 OneModelReader 可选实现 ModelReader；统一实例 alias 只保证 model_id 时返回代码级模型身份。
func (s *Scope) ModelByCode(ctx context.Context, modelCode string) (returned Model, found bool, err error) {
	defer func() { s.recordDependencyError("onemodel_model", err) }()
	if result, exists := s.models[modelCode]; exists {
		return result.value, result.found, result.err
	}
	result := modelResult{}
	if s.sources.Model == nil {
		result.err = fmt.Errorf("onemodel model reader is unavailable")
	} else {
		result.value, result.found, result.err = s.sources.Model.GetModelByCode(ctx, s.original.BKTenantID, modelCode)
	}
	if ctx.Err() == nil {
		s.models[modelCode] = result
	}
	return result.value, result.found, result.err
}

// RelatedHost 读取采集对象关联的主机实例。
func (s *Scope) RelatedHost(ctx context.Context, modelCode, instanceID, relation string) (returned Instance, found bool, err error) {
	defer func() { s.recordDependencyError("related_host", err) }()
	key := modelCode + "\x00" + instanceID + "\x00" + relation
	if result, exists := s.relatedHosts[key]; exists {
		return result.value, result.found, result.err
	}
	result := instanceResult{}
	if s.sources.CollectTopology == nil {
		result.err = fmt.Errorf("collect topology reader is unavailable")
	} else {
		result.value, result.found, result.err = s.sources.CollectTopology.FindRelatedHost(
			ctx, s.original.BKTenantID, modelCode, instanceID, relation,
		)
	}
	if ctx.Err() == nil {
		s.relatedHosts[key] = result
	}
	return result.value, result.found, result.err
}

// HostTopology 读取主机业务拓扑。
func (s *Scope) HostTopology(ctx context.Context, hostID string) (returned models.ResourceTopology, found bool, err error) {
	defer func() { s.recordDependencyError("host_topology", err) }()
	if result, exists := s.topologies[hostID]; exists {
		return result.value, result.found, result.err
	}
	result := topologyResult{}
	if s.sources.CollectTopology == nil {
		result.err = fmt.Errorf("collect topology reader is unavailable")
	} else {
		result.value, result.found, result.err = s.sources.CollectTopology.FindHostTopology(ctx, s.original.BKTenantID, hostID)
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
func (s *Scope) Instance(ctx context.Context, query InstanceQuery) (returned Instance, found bool, err error) {
	defer func() { s.recordDependencyError("onemodel_instance", err) }()
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
		result.value, result.found, result.err = s.sources.OneModel.FindInstance(ctx, s.original.BKTenantID, query)
	}
	if ctx.Err() == nil {
		s.instances[key] = result
	}
	return result.value, result.found, result.err
}

// OriginalEvent 返回本次执行前的来源事实，不包含前序补丁。
func (s *Scope) OriginalEvent() domain.Event { return s.original.Clone() }

// RuleSources 返回规则引擎的显式只读端口。
func (s *Scope) RuleSources() custom.Sources {
	result := custom.Sources{}
	if s.sources.CMDB != nil {
		result.Instances = ruleInstanceReader{scope: s, next: s.sources.CMDB}
	}
	if s.sources.Display != nil {
		result.Display = ruleDisplayReader{scope: s, next: s.sources.Display}
	}
	return result
}

// EvaluationState 返回本等级唯一工作对象，首次需要查询或修改时才建立动态视图。
func (s *Scope) EvaluationState() (*domain.EnrichEvaluation, error) {
	if s.state == nil {
		state, err := domain.NewEnrichEvaluation(s.original, s.evaluation)
		if err != nil {
			return nil, err
		}
		s.state = state
	}
	return s.state, nil
}

// Facts 供受控内置 Processor 只读访问原始事实；需要修改的调用方使用 Event 的隔离副本。
func (s *Scope) Facts() domain.Event { return *s.original }

// EffectiveEvent 是要求隔离 Event 的读取边界；处理链内部使用 EvaluationState。
func (s *Scope) EffectiveEvent() domain.Event {
	state, err := s.EvaluationState()
	if err != nil {
		return s.original.Clone()
	}
	event, err := state.EventSnapshot()
	if err != nil {
		return s.original.Clone()
	}
	return event
}
