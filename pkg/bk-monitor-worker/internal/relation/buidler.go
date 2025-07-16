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
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/prometheus/prompb"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/remote"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	Set    = "set"
	Module = "module"
	Host   = "host"
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

// MetricsBuilder 关联指标构建器，生成指标缓存以及输出 prometheus 上报指标
type MetricsBuilder struct {
	spaceReport remote.Reporter

	metrics   map[int]map[int]Nodes
	lock      sync.RWMutex
	resources *ResourceExpandInfos
}

func newRelationMetricsBuilder() *MetricsBuilder {
	return &MetricsBuilder{
		resources: NewResourceExpandInfos(),
	}
}

func GetRelationMetricsBuilder() *MetricsBuilder {
	return defaultRelationMetricsBuilder
}

func (b *MetricsBuilder) WithSpaceReport(reporter remote.Reporter) *MetricsBuilder {
	b.spaceReport = reporter
	return b
}

// ClearAllMetrics 清理全部指标
func (b *MetricsBuilder) ClearAllMetrics() {
	b.lock.RLock()
	defer b.lock.RUnlock()
	b.resources.Reset()
}

func (b *MetricsBuilder) ClearResourceWithID(name string, ids ...string) {
	b.lock.RLock()
	defer b.lock.RUnlock()

	b.resources.Delete(name, ids...)
}

type HostS struct {
	BkHostId      int
	TopoLinks     map[string][]map[string]any
	BkHostInnerip string
	BkCloudId     int
	BkBizId       int
}

func (b *MetricsBuilder) toString(s any) string {
	return fmt.Sprintf("%v", s)
}

// BuildMetrics 通过 hosts 构建关联指标，存入缓存
func (b *MetricsBuilder) BuildMetrics(_ context.Context, name string, bkBizID int, hosts []HostS) error {
	nodeMap := make(map[int]Nodes)
	for _, host := range hosts {
		if host.BkHostId == 0 {
			continue
		}

		if len(host.TopoLinks) == 0 {
			// 如果没有 topo 数据，至少需要增加一条路径，用于存放 system、agent、business 等信息
			host.TopoLinks = map[string][]map[string]any{
				"": nil,
			}
		}

		for _, topoLinks := range host.TopoLinks {
			nodes := make(Nodes, 0)

			// 加入 system 节点
			if host.BkHostInnerip != "" {
				nodes = append(nodes, Node{
					Name: RelationSystemNode,
					Labels: map[string]string{
						"bk_cloud_id":  b.toString(host.BkCloudId),
						"bk_target_ip": b.toString(host.BkHostInnerip),
					},
				})
			}

			// 加入 host 节点
			nodes = append(nodes, Node{
				Name: RelationHostNode,
				Labels: map[string]string{
					"host_id": b.toString(host.BkHostId),
				},
			})

			// 加入拓扑节点
			for _, n := range topoLinks {
				key := b.toString(n["bk_obj_id"])
				if key == "" {
					continue
				}
				if key == "biz" {
					continue
				}

				nodes = append(nodes, Node{
					Name: key,
					Labels: map[string]string{
						fmt.Sprintf("%s_id", key): b.toString(n["bk_inst_id"]),
					},
				})
			}

			// 加入业务节点
			if len(topoLinks) > 0 {
				nodes = append(nodes, Node{
					Name: RelationBusinessNode,
					Labels: map[string]string{
						"biz_id": b.toString(host.BkBizId),
					},
				})
			}

			nodeMap[host.BkHostId] = nodes
		}
	}

	if len(nodeMap) > 0 {
		b.lock.Lock()
		b.metrics[bkBizID] = nodeMap
		b.lock.Unlock()
	}

	logger.Infof("[cmdb_relation] set metrics  bkcc__%d: %d", bkBizID, len(nodeMap))
	return nil
}

// String 以 string 格式获取所有指标数据
func (b *MetricsBuilder) String() string {
	var buf bytes.Buffer
	b.lock.RLock()
	defer b.lock.RUnlock()

	metricsMap := make(map[string]struct{})
	for bkBizID, nodeMap := range b.metrics {
		for _, nodes := range nodeMap {
			for _, relationMetric := range nodes.toRelationMetrics() {
				metricsMap[relationMetric.String(bkBizID)] = struct{}{}
			}
		}
	}

	for metric := range metricsMap {
		buf.WriteString(metric)
		buf.WriteString("\n")
	}

	return buf.String()
}

// PushAll 推送全业务数据
func (b *MetricsBuilder) PushAll(ctx context.Context, timestamp time.Time) error {
	if b.spaceReport == nil {
		return fmt.Errorf("space reporter is nil")
	}

	// 提前把 bkBizIDs 取出来，缩小锁区间，控制在单业务下
	bkBizIDs := make([]int, 0, len(b.metrics))
	b.lock.RLock()
	for bizID := range b.metrics {
		bkBizIDs = append(bkBizIDs, bizID)
	}
	b.lock.RUnlock()

	for bkBizID := range bkBizIDs {
		ts := getTsPool()
		metricsMap := make(map[string]struct{})

		b.lock.RLock()
		if nodeMap, ok := b.metrics[bkBizID]; ok {
			for _, nodes := range nodeMap {
				for _, relationMetric := range nodes.toRelationMetrics() {
					d := relationMetric.TimeSeries(bkBizID, timestamp)
					if _, ok = metricsMap[d.String()]; !ok {
						metricsMap[d.String()] = struct{}{}
						ts = append(ts, d)
					}
				}
			}
		}
		b.lock.RUnlock()

		if len(ts) > 0 {
			// 上传业务 timeSeries
			spaceUID := fmt.Sprintf("bkcc__%d", bkBizID)
			if err := b.spaceReport.Do(ctx, spaceUID, ts...); err != nil {
				return err
			}
			logger.Infof("[cmdb_relation] push %s cmdb relation metrics %d", spaceUID, len(ts))
		}

		putTsPool(ts)
	}

	return nil
}
