// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cmdbcache

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

var (
	defaultRelationMetricsBuilder = newRelationMetricsBuilder()

	tsPool = sync.Pool{
		New: func() any {
			return make([]prompb.TimeSeries, 0)
		},
	}
	tsMapPool = sync.Pool{New: func() any {
		return make(map[string]struct{})
	}}
)

func getTsMapPool() map[string]struct{} {
	return tsMapPool.Get().(map[string]struct{})
}

func putTsMapPool(tsMap map[string]struct{}) {
	tsMap = make(map[string]struct{})
	tsMapPool.Put(tsMap)
}

func getTsPool() []prompb.TimeSeries {
	return tsPool.Get().([]prompb.TimeSeries)
}

func putTsPool(ts []prompb.TimeSeries) {
	ts = ts[:0]
	tsPool.Put(ts)
}

// RelationMetricsBuilder 关联指标构建器，生成指标缓存以及输出 prometheus 上报指标
type RelationMetricsBuilder struct {
	spaceReport remote.Reporter
	metricsLock sync.RWMutex
	metrics     map[int]map[int]Nodes
}

func newRelationMetricsBuilder() *RelationMetricsBuilder {
	return &RelationMetricsBuilder{
		metrics: make(map[int]map[int]Nodes),
	}
}

func GetRelationMetricsBuilder() *RelationMetricsBuilder {
	return defaultRelationMetricsBuilder
}

func (b *RelationMetricsBuilder) WithSpaceReport(reporter remote.Reporter) *RelationMetricsBuilder {
	b.spaceReport = reporter
	return b
}

func (b *RelationMetricsBuilder) toString(v any) string {
	var val string
	switch v.(type) {
	case int:
		val = fmt.Sprintf("%d", v)
	case string:
		val = v.(string)
	default:
		val = fmt.Sprintf("%v", v)
	}
	return val
}

// ClearAllMetrics 清理全部指标
func (b *RelationMetricsBuilder) ClearAllMetrics() {
	b.metrics = make(map[int]map[int]Nodes)
}

// ClearMetricsWithHostID 清理 host 指标
func (b *RelationMetricsBuilder) ClearMetricsWithHostID(hosts ...*AlarmHostInfo) {
	b.metricsLock.Lock()
	defer b.metricsLock.Unlock()

	for _, host := range hosts {
		if hostMap, ok := b.metrics[host.BkBizId]; ok {
			if _, ok = hostMap[host.BkHostId]; ok {
				delete(hostMap, host.BkHostId)
			}
		}
	}
}

// BuildMetrics 通过 hosts 构建关联指标，存入缓存
func (b *RelationMetricsBuilder) BuildMetrics(_ context.Context, bkBizID int, hosts []*AlarmHostInfo) error {
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
		b.metricsLock.Lock()
		b.metrics[bkBizID] = nodeMap
		b.metricsLock.Unlock()
	}

	logger.Infof("[cmdb_relation] set metrics  bkcc__%d: %d", bkBizID, len(nodeMap))
	return nil
}

// String 以 string 格式获取所有指标数据
func (b *RelationMetricsBuilder) String() string {
	var buf bytes.Buffer
	b.metricsLock.RLock()
	defer b.metricsLock.RUnlock()

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
func (b *RelationMetricsBuilder) PushAll(ctx context.Context, timestamp time.Time) error {
	if b.spaceReport == nil {
		return fmt.Errorf("space reporter is nil")
	}

	// 提前把 bkBizIDs 取出来，缩小锁区间，控制在单业务下
	bkBizIDs := make([]int, 0, len(b.metrics))
	b.metricsLock.RLock()
	for bizID := range b.metrics {
		bkBizIDs = append(bkBizIDs, bizID)
	}
	b.metricsLock.RUnlock()

	for bkBizID := range bkBizIDs {
		ts := getTsPool()
		metricsMap := getTsMapPool()

		b.metricsLock.RLock()
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
		b.metricsLock.RUnlock()

		if len(ts) > 0 {
			// 上传业务 timeSeries
			spaceUID := fmt.Sprintf("bkcc__%d", bkBizID)
			if err := b.spaceReport.Do(ctx, spaceUID, ts...); err != nil {
				return err
			}
			logger.Infof("[cmdb_relation] push %s cmdb relation metrics %d", spaceUID, len(ts))
		}

		putTsPool(ts)
		putTsMapPool(metricsMap)
	}

	return nil
}
