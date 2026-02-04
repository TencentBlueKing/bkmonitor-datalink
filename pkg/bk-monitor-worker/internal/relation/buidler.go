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

// ExpandFields 表示扩展字段 map
type ExpandFields map[string]string

// ParentExpandsStore 存储父资源的扩展字段配置
// 结构: targetResource -> parentResource -> parentID -> expandFields
// 例如: set 配置了 host 的扩展字段，当 host 构建指标时可以从这里继承
type ParentExpandsStore struct {
	// data: targetResource -> parentResource -> parentID -> expandFields
	data map[string]map[string]map[string]ExpandFields
}

func NewParentExpandsStore() *ParentExpandsStore {
	return &ParentExpandsStore{
		data: make(map[string]map[string]map[string]ExpandFields),
	}
}

// Set 设置父资源的扩展字段
func (s *ParentExpandsStore) Set(targetResource, parentResource, parentID string, fields ExpandFields) {
	if _, ok := s.data[targetResource]; !ok {
		s.data[targetResource] = make(map[string]map[string]ExpandFields)
	}
	if _, ok := s.data[targetResource][parentResource]; !ok {
		s.data[targetResource][parentResource] = make(map[string]ExpandFields)
	}
	s.data[targetResource][parentResource][parentID] = fields
}

// Get 获取父资源的扩展字段
func (s *ParentExpandsStore) Get(targetResource, parentResource, parentID string) (ExpandFields, bool) {
	if targetMap, ok := s.data[targetResource]; ok {
		if parentMap, ok := targetMap[parentResource]; ok {
			if fields, ok := parentMap[parentID]; ok {
				return fields, true
			}
		}
	}
	return nil, false
}

// GetByParents 根据目标资源和父资源列表查找扩展字段
// targetResource: 目标资源类型
// parents: 父资源列表，每个元素包含 Resource 和 ID
// 返回找到的第一个扩展字段
func (s *ParentExpandsStore) GetByParents(targetResource string, parents []Item) (ExpandFields, bool) {
	targetMap, ok := s.data[targetResource]
	if !ok {
		return nil, false
	}

	for _, parent := range parents {
		if parentMap, ok := targetMap[parent.Resource]; ok {
			if fields, ok := parentMap[parent.ID]; ok {
				return fields, true
			}
		}
	}
	return nil, false
}

// GetField 从父资源中获取单个字段值
// targetResource: 目标资源类型
// parents: 父资源列表
// fieldName: 字段名
// 返回字段值和是否存在
func (s *ParentExpandsStore) GetField(targetResource string, parents []Item, fieldName string) (string, bool) {
	targetMap, ok := s.data[targetResource]
	if !ok {
		return "", false
	}

	for _, parent := range parents {
		if parentMap, ok := targetMap[parent.Resource]; ok {
			if fields, ok := parentMap[parent.ID]; ok {
				if val, ok := fields[fieldName]; ok {
					return val, true
				}
			}
		}
	}
	return "", false
}

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
		if _, ok := metricCheck[m.String()]; ok {
			return
		}
		metrics = append(metrics, m)
		metricCheck[m.String()] = struct{}{}
	}

	// 默认注入业务维度
	bizLabel := map[string]string{
		BizID: fmt.Sprintf("%d", bizID),
	}

	// 父资源扩展字段存储
	parentExpands := NewParentExpandsStore()

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
			var expandInfoStatus bool

			// 注入 ExpandInfo 指标
			// info.Expands 里面就是配置的资源场景，expandResource 对应场景资源名
			for expandResource, expand := range info.Expands {
				if info.Resource == "" {
					continue
				}

				// 如果配置资源一致，则为自身资源的 Expand，否则使用继承池里的 Expand
				// 这里的 info.Resource 指该实体的真实归属资源，上面的 resource 表示的是数据维护的资源
				// 例如：host 数据，会同时维护 host 和 system 的资源，所以相关资源实体需要使用 info.Resource
				if expandResource == info.Resource {
					// 构建维度，注入主键和扩展维度
					node := b.makeNode(expandResource, info.Label, bizLabel, expand)
					metric := node.ExpandInfoMetric()

					addMetrics(metric)
					expandInfoStatus = true
				} else {
					// 存储父资源的 Expand，供子资源继承
					parentExpands.Set(expandResource, resource, info.ID, expand)
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

					// 如果已经配置了扩展信息，则跳过继承查找
					if expandInfoStatus {
						continue
					}

					// 查找上游资源是否配置了扩展信息
					if expand, ok := parentExpands.Get(info.Resource, item.Resource, item.ID); ok {
						// 构建维度，注入主键和扩展维度
						node := b.makeNode(info.Resource, info.Label, bizLabel, expand)
						expandMetric := node.ExpandInfoMetric()
						addMetrics(expandMetric)
						expandInfoStatus = true
					}
				}
			}

			// 通过 relationConfig 构建指标
			relationConfigMetrics := b.buildRelationConfigMetrics(bizID, info, parentExpands)
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
func (b *MetricsBuilder) buildRelationConfigMetrics(bizID int, info *Info, parentExpands *ParentExpandsStore) []Metric {
	if len(info.RelationConfig) == 0 {
		return nil
	}

	if b.schemaProvider == nil {
		logger.Warnf("[relation_config] SchemaProvider not configured")
		return nil
	}

	metrics := make([]Metric, 0)
	namespace := fmt.Sprintf("bkcc__%d", bizID)

	// 将 info.Links 展平为 Item 列表，用于查找父资源的扩展字段
	var parentLinks []Item
	for _, link := range info.Links {
		parentLinks = append(parentLinks, link...)
	}

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
		collectedFields := make(map[string]string)
		collectedFields[BizID] = fmt.Sprintf("%d", bizID)

		// 获取自身资源的 expands 字段
		selfExpands := info.Expands[info.Resource]

		// 校验并添加源资源的字段
		missingFields := make([]string, 0)
		for _, requiredField := range requiredFields {
			// 跳过已经处理的字段
			if _, ok := collectedFields[requiredField]; ok {
				continue
			}

			// 从 RelationConfig 的 fields 中查找
			if val, ok := fields[requiredField]; ok {
				collectedFields[requiredField] = fmt.Sprint(val)
				continue
			}

			// 从自身资源的 expands 中查找
			if selfExpands != nil {
				if val, ok := selfExpands[requiredField]; ok {
					collectedFields[requiredField] = val
					continue
				}
			}

			// 从父资源的扩展字段中查找
			if val, ok := parentExpands.GetField(info.Resource, parentLinks, requiredField); ok {
				collectedFields[requiredField] = val
				continue
			}

			missingFields = append(missingFields, requiredField)
		}

		// 5. 如果有缺失字段，记录错误并跳过
		if len(missingFields) > 0 {
			logger.Errorf("[relation_config] Missing required fields for %s: %v",
				relationDef.GetRelationName(), missingFields)
			continue
		}

		// 6. 生成指标
		labelList := make(Labels, 0, len(collectedFields))
		for k, v := range collectedFields {
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

		logger.Debugf("[relation_config] Generated metric: %s with fields: %v",
			metric.Name, collectedFields)
	}

	return metrics
}
