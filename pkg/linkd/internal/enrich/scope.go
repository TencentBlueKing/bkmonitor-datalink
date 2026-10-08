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
	CMDB        onemodel.Reader
	Display     custom.DisplayReader
	CWStrategy  CWStrategyReader
	StrategySet StrategySetReader
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

// CWStrategyReader 按全租户唯一的关联 ID 读取鲸眼声明式策略。
type CWStrategyReader interface {
	GetByBKStrategyID(ctx context.Context, tenantID string, bkStrategyID int64) (models.CWStrategy, bool, error)
}

// StrategySetReader 读取同租户监控模板的当前配置集合，供关联 ConfigID 精确选取。
type StrategySetReader interface {
	GetStrategySet(ctx context.Context, tenantID string, monitorTemplateID int64) (models.StrategySet, bool, error)
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
func (s *Scope) CloudResource(ctx context.Context, cloudID, resourceType, instanceID string) (models.CloudResource, bool, error) {
	if s.sources.CloudResource == nil {
		return models.CloudResource{}, false, fmt.Errorf("cloud resource reader is unavailable")
	}
	key, err := queryCacheKey("cloud", s.event.BKTenantID, cloudID, resourceType, instanceID)
	if err != nil {
		return models.CloudResource{}, false, err
	}
	value, err := s.Scenario(key, func() (any, error) {
		v, found, err := s.sources.CloudResource.GetCloudResource(ctx, s.event.BKTenantID, cloudID, resourceType, instanceID)
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
	original      domain.Event
	event         domain.Event
	evaluation    domain.EventEvaluation
	sources       Sources
	enrichContext *EnrichContext

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
	metrics         map[models.MetricLibraryQuery]metricResult
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
		metrics: make(map[models.MetricLibraryQuery]metricResult), collectConfigs: make(map[string]collectConfigResult),
		apmApplications: make(map[apmApplicationQuery]apmApplicationResult),
		uptimeTasks:     make(map[string]uptimeTaskResult), uptimeNodes: make(map[string]uptimeNodeResult),
		relatedHosts: make(map[string]instanceResult), topologies: make(map[string]topologyResult), logThemes: make(map[int64]logThemeResult),
		scenarios: make(map[string]scenarioResult),
	}
}

func newScope(event domain.Event, evaluation domain.EventEvaluation, sources Sources, preview bool, cache *requestCache) (*Scope, error) {
	normalized := event.Clone()
	// 历史丰富只用于预览对比，不能成为新一轮处理器的来源事实。
	normalized.EventEnrichment = domain.EventEnrichment{EnrichStatus: domain.EnrichStatusPending}
	var err error
	if !preview {
		normalized, err = normalized.Normalize()
	}
	if err != nil {
		return nil, fmt.Errorf("create enrich scope: %w", err)
	}
	return &Scope{event: normalized.Clone(), original: normalized.Clone(), evaluation: evaluation, sources: sources, enrichContext: &EnrichContext{}, requestCache: cache}, nil
}

// Evaluation 返回本轮处理的明确等级和动作，不从 Event 隐式推测严重性。
func (s *Scope) Evaluation() domain.EventEvaluation { return s.evaluation }

// Context 返回本次调用共享的类型化丰富上下文。
// 各 Processor 只能写入与其输出分组对应的 ResourceContext。
func (s *Scope) Context() *EnrichContext {
	return s.enrichContext
}

// Event 为内置场景返回原始事实副本，保持其查询身份不受前序展示结果影响。
// 自定义规则通过 EffectiveEvent 显式读取前序补丁，两个视图都不共享可变字段。
func (s *Scope) Event() domain.Event {
	return s.original.Clone()
}

// DynamicGroupIDs 只使用原始 Event 的租户身份；未配置投影时保持空列表。
func (s *Scope) DynamicGroupIDs(ctx context.Context, modelCode, instanceID string) ([]string, error) {
	if s.sources.DynamicGroup == nil {
		return []string{}, nil
	}
	return s.sources.DynamicGroup.GetDynamicGroupIDs(ctx, s.event.BKTenantID, modelCode, instanceID)
}

// CWStrategyByBKStrategyID 惰性读取并复用按平台策略 ID 关联的鲸眼声明式策略。
func (s *Scope) CWStrategyByBKStrategyID(ctx context.Context, strategyID int64) (models.CWStrategy, bool, error) {
	s.strategyOnce.Do(func() {
		if s.sources.CWStrategy == nil {
			s.strategy.err = fmt.Errorf("strategy config reader is unavailable")
			return
		}
		s.strategy.value, s.strategy.found, s.strategy.err = s.sources.CWStrategy.GetByBKStrategyID(ctx, s.event.BKTenantID, strategyID)
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
			s.event.BKTenantID,
			bizID,
		)
	})
	return s.business.isGlobal, s.business.found, s.business.err
}

// MetricLibrary 惰性读取并复用本次调用的指标库结果。
// MonitorMetric 表已经废弃，指标展示名称统一通过该入口查询 MetricLibrary。
func (s *Scope) MetricLibrary(ctx context.Context, query models.MetricLibraryQuery) (models.MetricMetadata, bool, error) {
	query.TenantID = s.event.BKTenantID
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

// APMApplications 按业务和名称读取租户内 APM 应用候选项，并在单次 Enrich 内复用结果。
func (s *Scope) APMApplications(ctx context.Context, bizID int64, name string) ([]models.APMApplication, error) {
	query := apmApplicationQuery{bizID: bizID, name: name}
	if result, exists := s.apmApplications[query]; exists {
		return append([]models.APMApplication(nil), result.values...), result.err
	}
	result := apmApplicationResult{}
	if s.sources.APMApplication == nil {
		result.err = fmt.Errorf("apm application reader is unavailable")
	} else {
		result.values, result.err = s.sources.APMApplication.FindAPMApplications(ctx, s.event.BKTenantID, bizID, name)
	}
	if ctx.Err() == nil {
		result.values = append([]models.APMApplication(nil), result.values...)
		s.apmApplications[query] = result
	}
	return append([]models.APMApplication(nil), result.values...), result.err
}

// K8sInstance 读取 K8s 对象模型对应的统一实例。
func (s *Scope) K8sInstance(ctx context.Context, modelCode string, dimensions domain.DimensionMap) (Instance, bool, error) {
	if s.sources.K8s == nil {
		return Instance{}, false, fmt.Errorf("k8s reader is unavailable")
	}
	key, err := queryCacheKey("k8s", s.event.BKTenantID, modelCode, dimensions)
	if err != nil {
		return Instance{}, false, err
	}
	value, err := s.Scenario(key, func() (any, error) {
		v, found, err := s.sources.K8s.FindK8sInstance(ctx, s.event.BKTenantID, modelCode, dimensions)
		return instanceResult{value: v, found: found}, err
	})
	if err != nil {
		return Instance{}, false, err
	}
	result := value.(instanceResult)
	return cloneQueryInstances([]Instance{result.value})[0], result.found, nil
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
		result.value, result.found, result.err = s.sources.LogTheme.GetLogTheme(ctx, s.event.BKTenantID, themeID)
	}
	if ctx.Err() == nil {
		s.logThemes[themeID] = result
	}
	return result.value, result.found, result.err
}

// AlarmSource 惰性读取并复用本次调用的告警源。
func (s *Scope) AlarmSource(ctx context.Context) (models.AlarmSource, bool, error) {
	s.alarmSourceOnce.Do(func() {
		if s.sources.AlarmSource == nil {
			s.alarmSource.err = fmt.Errorf("alarm source reader is unavailable")
			return
		}
		s.alarmSource.value, s.alarmSource.found, s.alarmSource.err = s.sources.AlarmSource.GetAlarmSource(
			ctx,
			s.event.BKTenantID,
			s.event.EventSourceID,
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
func (s *Scope) CollectConfig(ctx context.Context, taskID string) (models.CollectConfig, bool, error) {
	if result, exists := s.collectConfigs[taskID]; exists {
		return result.value, result.found, result.err
	}
	result := collectConfigResult{}
	if s.sources.CollectConfig == nil {
		result.err = fmt.Errorf("collect config reader is unavailable")
	} else {
		result.value, result.found, result.err = s.sources.CollectConfig.GetCollectConfig(ctx, s.event.BKTenantID, taskID)
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
		result.value, result.found, result.err = s.sources.Uptime.GetUptimeTask(ctx, s.event.BKTenantID, taskID)
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
		result.value, result.found, result.err = s.sources.UptimeNode.GetUptimeNode(ctx, s.event.BKTenantID, nodeID)
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
		result.value, result.found, result.err = s.sources.Model.GetModelByCode(ctx, s.event.BKTenantID, modelCode)
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
			ctx, s.event.BKTenantID, modelCode, instanceID, relation,
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
		result.value, result.found, result.err = s.sources.CollectTopology.FindHostTopology(ctx, s.event.BKTenantID, hostID)
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
		result.value, result.found, result.err = s.sources.OneModel.FindInstance(ctx, s.event.BKTenantID, query)
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

func (s *Scope) applyPatches(patches []domain.EnrichPatch) error {
	document, err := domain.EventDocument(s.event, s.evaluation)
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
	var event domain.Event
	if err := json.Unmarshal(data, &event); err != nil {
		return err
	}
	event.EventEnrichment = s.event.EventEnrichment.Clone()
	s.event = event
	return nil
}

// EffectiveEvent 返回应用本轮前序补丁后的隔离副本，供自定义规则顺序取值。
func (s *Scope) EffectiveEvent() domain.Event { return s.event.Clone() }
