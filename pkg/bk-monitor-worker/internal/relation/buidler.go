// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package relation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/prometheus/prompb"
	"github.com/spf13/cast"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/service"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/remote"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	Set    = "set"
	Module = "module"
	Host   = "host"
	System = "system"
	Biz    = "biz"

	AppVersion = "app_version"
	GitCommit  = "git_commit"

	ExpandInfoColumn = "version_meta"
)

const (
	SetID      = "bk_set_id"
	SetName    = "bk_set_name"
	ModuleID   = "bk_module_id"
	ModuleName = "bk_module_name"
	HostID     = "bk_host_id"
	HostName   = "bk_host_name"
	BizID      = "bk_biz_id"
)

var (
	defaultRelationMetricsBuilder = newRelationMetricsBuilder()

	tsPool = sync.Pool{
		New: func() any {
			return make([]prompb.TimeSeries, 0)
		},
	}
)

func getTsPool() []prompb.TimeSeries {
	return tsPool.Get().([]prompb.TimeSeries)
}

func putTsPool(ts []prompb.TimeSeries) {
	ts = ts[:0]
	tsPool.Put(ts)
}

type MetricsBuilder struct {
	spaceReport remote.Reporter

	// SchemaProvider 提供资源和关系的元数据定义
	schemaProvider SchemaProvider

	lock sync.RWMutex
	// 业务ID -> 资源类型（set、module、host) -> resourceInfo
	resources map[int]map[string]*ResourceInfo
}

type SchemaProvider interface {
	GetResourceDefinition(namespace, resourceType string) (ResourceDefinition, error)
	GetRelationDefinition(namespace, fromResource, toResource string) (RelationDefinition, error)
}

type ResourceDefinition interface {
	GetPrimaryKeys() []string
}

type RelationDefinition interface {
	GetRelationName() string
	GetRequiredFields(fromResourceDef, toResourceDef ResourceDefinition) []string
}

func newRelationMetricsBuilder() *MetricsBuilder {
	return &MetricsBuilder{
		resources: make(map[int]map[string]*ResourceInfo),
	}
}

func GetRelationMetricsBuilder() *MetricsBuilder {
	return defaultRelationMetricsBuilder
}

func (b *MetricsBuilder) WithSpaceReport(reporter remote.Reporter) *MetricsBuilder {
	b.spaceReport = reporter
	return b
}

// WithSchemaProvider 注入 SchemaProvider
func (b *MetricsBuilder) WithSchemaProvider(provider service.SchemaProvider) *MetricsBuilder {
	b.schemaProvider = NewSchemaProviderAdapter(provider)
	return b
}

func (b *MetricsBuilder) getResourceInfo(bizID int, name string) *ResourceInfo {
	if _, ok := b.resources[bizID]; !ok {
		b.resources[bizID] = make(map[string]*ResourceInfo)
	}
	if _, ok := b.resources[bizID][name]; !ok {
		b.resources[bizID][name] = &ResourceInfo{
			Name: name,
		}
	}

	return b.resources[bizID][name]
}

func (b *MetricsBuilder) Debug(bizID string) string {
	b.lock.RLock()
	defer b.lock.RUnlock()

	if len(b.resources) == 0 {
		return ""
	}

	var data map[string]*ResourceInfo
	if bizID != "" {
		id := cast.ToInt(bizID)
		if resources, ok := b.resources[id]; ok {
			data = resources
		}
	}

	if len(data) == 0 {
		for _, r := range b.resources {
			data = r
			break
		}
	}

	out, _ := json.Marshal(data)
	return string(out)
}

// ClearAllMetrics 清理全部指标
func (b *MetricsBuilder) ClearAllMetrics() {
	b.lock.Lock()
	defer b.lock.Unlock()
	logger.Infof("[cmdb_relation] clear_all_metrics")
	b.resources = make(map[int]map[string]*ResourceInfo)
}

func (b *MetricsBuilder) ClearResourceWithID(bizID int, name string, ids ...string) {
	b.lock.Lock()
	defer b.lock.Unlock()

	if bizID == 0 {
		return
	}
	if name == "" {
		return
	}

	resourceInfo := b.getResourceInfo(bizID, name)
	for _, id := range ids {
		resourceInfo.Delete(id)
	}
}

func (b *MetricsBuilder) BuildInfosCache(_ context.Context, bizID int, name string, infos []*Info) error {
	if infos == nil {
		return nil
	}
	b.lock.Lock()
	defer b.lock.Unlock()
	oldInfos := b.getResourceInfo(bizID, name)
	for _, info := range infos {
		oldInfos.Add(info.ID, info)
	}

	return nil
}

func (b *MetricsBuilder) BizIDs() []int {
	b.lock.RLock()
	defer b.lock.RUnlock()

	bizIDs := make([]int, 0, len(b.resources))
	for bizID := range b.resources {
		bizIDs = append(bizIDs, bizID)
	}
	return bizIDs
}

func (b *MetricsBuilder) makeNode(resource string, labels ...map[string]string) Node {
	label := make(map[string]string)
	for _, lb := range labels {
		for k, v := range lb {
			label[k] = v
		}
	}

	return Node{
		Name:   resource,
		Labels: label,
	}
}

func (b *MetricsBuilder) getCMDBMetrics(bizID int) []Metric {
	if b.resources == nil {
		return nil
	}
	if _, ok := b.resources[bizID]; !ok {
		return nil
	}

	metrics := make([]Metric, 0)
	metricCheck := make(map[string]struct{})

	addMetrics := func(m Metric) {
		if _, ok := metricCheck[m.String()]; !ok {
			metrics = append(metrics, m)
			metricCheck[m.String()] = struct{}{}
		}
	}

	// 默认注入业务维度
	bizLabel := map[string]string{
		BizID: fmt.Sprintf("%d", bizID),
	}

	// 资源场景（ resource) -> 资源配置 (resource) -> 资源ID (ID) -> 资源扩展信息 (Expand)
	// 例如：set 资源配置 host 和 module 的资源场景，说明生成 host 扩展维度的时候，如果自己没有单独配置的话，需要继承 set 所配置的扩展信息
	resourceParentExpands := make(map[string]map[string]map[string]map[string]string)

	// 不同业务分开构建，方便拆分数据
	resources := b.resources[bizID]

	// 处理 cmdb 关联数据，set -> module -> host，Expand 需要按序遍历，下层需要继承上层的 Expand
	for _, resource := range []string{Set, Module, Host} {
		if _, ok := resources[resource]; !ok {
			continue
		}

		infos := resources[resource]
		if infos == nil {
			continue
		}

		infos.Range(func(info *Info) {
			// 判断是否对该资源配置扩展
			var (
				expandInfoStatus bool
			)

			// 注入 ExpandInfo 指标
			// info.Expands 里面就是配置的资源场景，expandResource 对应场景资源名
			for expandResource, expand := range info.Expands {
				if info.Resource == "" {
					continue
				}

				// 如果配置资源一致，则为自身资源的 Expand，否则使用继承池里的 Expand
				// 这里的 info.Resource 指该实体的真是归属资源，上面的 resource 表示的是数据维护的资源
				// 例如：host 数据，会同时维护 host 和 system 的资源，所以相关资源实体需要使用 info.Resource
				if expandResource == info.Resource {
					// 构建维度，注入主键和扩展维度
					node := b.makeNode(expandResource, info.Label, bizLabel, expand)
					metric := node.ExpandInfoMetric()

					addMetrics(metric)
					expandInfoStatus = true
				} else {
					// 注入父资源的 Expand
					if _, ok := resourceParentExpands[expandResource]; !ok {
						resourceParentExpands[expandResource] = make(map[string]map[string]map[string]string)
					}
					if _, ok := resourceParentExpands[expandResource][resource]; !ok {
						resourceParentExpands[expandResource][resource] = make(map[string]map[string]string)
					}

					resourceParentExpands[expandResource][resource][info.ID] = expand
				}
			}

			// 根节点
			rootNode := b.makeNode(info.Resource, info.Label)

			// 注入 relation 关联指标
			for _, link := range info.Links {
				sourceNode := rootNode
				for _, item := range link {
					if item.Resource == Biz {
						continue
					}

					nextNode := b.makeNode(item.Resource, bizLabel, item.Label)
					metric := sourceNode.RelationMetric(nextNode)
					addMetrics(metric)
					sourceNode = nextNode

					// 如果没有自身资源下没有匹配到扩展信息，需要从上游找是否有配置需要继承，如果已经配置了则直接退出
					if expandInfoStatus {
						continue
					}

					// 查找上游资源是否配置了扩展信息
					if expand, expandOk := resourceParentExpands[info.Resource][item.Resource][item.ID]; expandOk {
						// 构建维度，注入主键和扩展维度
						node := b.makeNode(info.Resource, info.Label, bizLabel, expand)
						expandMetric := node.ExpandInfoMetric()
						addMetrics(expandMetric)
						expandInfoStatus = true
					}
				}
			}

			// 新增: 处理 RelationConfig
			relationConfigMetrics := b.buildRelationConfigMetrics(bizID, info)
			for _, metric := range relationConfigMetrics {
				addMetrics(metric)
			}
		})
	}

	return metrics
}

func (b *MetricsBuilder) getAllMetrics(bizID int) []Metric {
	cmdbMetrics := b.getCMDBMetrics(bizID)

	return append(cmdbMetrics)
}

// String 以 string 格式获取所有指标数据
func (b *MetricsBuilder) String() string {
	var buf bytes.Buffer

	for _, bkBizID := range b.BizIDs() {

		b.lock.RLock()
		metricList := b.getCMDBMetrics(bkBizID)
		b.lock.RUnlock()

		for _, metric := range metricList {
			buf.WriteString(metric.String())
			buf.WriteString("\n")
		}
	}

	return buf.String()
}

// PushAll 推送全业务数据
func (b *MetricsBuilder) PushAll(ctx context.Context, timestamp time.Time) error {
	if b.spaceReport == nil {
		return fmt.Errorf("space reporter is nil")
	}

	n := time.Now()

	bizs := b.BizIDs()
	pushCount := 0

	for _, bkBizID := range bizs {
		ts := getTsPool()

		b.lock.RLock()
		metrics := b.getCMDBMetrics(bkBizID)
		b.lock.RUnlock()

		for _, metric := range metrics {
			ts = append(ts, metric.TimeSeries(timestamp))
		}

		if len(ts) > 0 {
			// 上传业务 timeSeries
			spaceUID := fmt.Sprintf("bkcc__%d", bkBizID)
			if err := b.spaceReport.Do(ctx, spaceUID, ts...); err != nil {
				return err
			}
			pushCount += len(ts)
		}

		putTsPool(ts)
	}
	logger.Infof("[cmdb_relation] push_all_metrics biz_count: %d ts_count: %d cost: %s", len(bizs), pushCount, time.Since(n))

	return nil
}

// buildRelationConfigMetrics 构建基于 RelationConfig 的关系指标
func (b *MetricsBuilder) buildRelationConfigMetrics(bizID int, info *Info) []Metric {
	if len(info.RelationConfig) == 0 {
		return nil
	}

	if b.schemaProvider == nil {
		logger.Warnf("[relation_config] SchemaProvider not configured")
		return nil
	}

	metrics := make([]Metric, 0)
	namespace := fmt.Sprintf("bkcc__%d", bizID)

	for targetResource, fields := range info.RelationConfig {
		// 1. 查询 RelationDefinition
		relationDef, err := b.schemaProvider.GetRelationDefinition(namespace, targetResource, info.Resource)
		if err != nil {
			logger.Warnf("[relation_config] Relation %s_with_%s not found in SchemaProvider: %v",
				targetResource, info.Resource, err)
			continue
		}

		// 2. 获取两端资源的 ResourceDefinition
		fromResourceDef, err := b.schemaProvider.GetResourceDefinition(namespace, targetResource)
		if err != nil {
			logger.Errorf("[relation_config] Resource definition for %s not found: %v", targetResource, err)
			continue
		}

		toResourceDef, err := b.schemaProvider.GetResourceDefinition(namespace, info.Resource)
		if err != nil {
			logger.Errorf("[relation_config] Resource definition for %s not found: %v", info.Resource, err)
			continue
		}

		// 3. 获取必填字段列表
		requiredFields := relationDef.GetRequiredFields(fromResourceDef, toResourceDef)

		// 4. 字段完整性校验
		labels := make(map[string]string)

		// 添加业务ID
		labels[BizID] = fmt.Sprintf("%d", bizID)

		// 添加目标资源的主键字段（从 info.Label 获取）
		for _, pk := range toResourceDef.GetPrimaryKeys() {
			if val, ok := info.Label[pk]; ok {
				labels[pk] = val
			} else if pk == info.Resource+"_id" && info.ID != "" {
				labels[pk] = info.ID
			}
		}

		// 校验并添加源资源的字段（从 RelationConfig 中获取）
		missingFields := make([]string, 0)
		for _, requiredField := range requiredFields {
			// 跳过已经处理的字段（目标资源的主键和业务ID）
			if _, ok := labels[requiredField]; ok {
				continue
			}

			// 从 RelationConfig 的 fields 中查找
			if val, ok := fields[requiredField]; ok {
				labels[requiredField] = fmt.Sprint(val)
			} else {
				missingFields = append(missingFields, requiredField)
			}
		}

		// 5. 如果有缺失字段，记录错误并跳过
		if len(missingFields) > 0 {
			logger.Errorf("[relation_config] Missing required labels for %s: %v",
				relationDef.GetRelationName(), missingFields)
			continue
		}

		// 6. 生成指标
		// 转换 map[string]string 到 Labels
		labelList := make(Labels, 0, len(labels))
		for k, v := range labels {
			labelList = append(labelList, Label{
				Name:  k,
				Value: v,
			})
		}

		metric := Metric{
			Name:   relationDef.GetRelationName(),
			Labels: labelList,
		}

		metrics = append(metrics, metric)

		logger.Debugf("[relation_config] Generated metric: %s with labels: %v",
			metric.Name, labels)
	}

	return metrics
}
