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
	"fmt"
	"sort"
	"strings"
	"sync"
)

// RelationMetricsBuilder 关联指标构建器，生成指标缓存以及输出 prometheus 上报指标
type RelationMetricsBuilder struct {
	lock    sync.RWMutex
	metrics map[string]struct{}
}

var (
	defaultRelationMetricsBuilder = &RelationMetricsBuilder{}
)

func GetRelationMetricsBuilder() *RelationMetricsBuilder {
	return defaultRelationMetricsBuilder
}

func (b *RelationMetricsBuilder) toString(v interface{}) string {
	var val string
	switch v.(type) {
	case int:
		val = fmt.Sprintf("%d", v)
	case string:
		val = v.(string)
	default:
		val = fmt.Sprintf("%+v", v)
	}
	return val
}

// BuildMetrics 通过 hosts 构建关联指标，存入缓存
func (b *RelationMetricsBuilder) BuildMetrics(hosts []*AlarmHostInfo) error {
	if len(hosts) == 0 {
		return nil
	}

	localMetrics := make(map[string]struct{})
	for _, host := range hosts {
		if host.BkHostId == 0 {
			continue
		}

		for _, topoLinks := range host.TopoLinks {
			nodes := getRelationNodes()

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

			// 加入 agent 节点
			nodes = append(nodes, Node{
				Name: RelationAgentNode,
				Labels: map[string]string{
					"agent_id": b.toString(host.BkHostId),
				},
			})

			// 加入拓扑节点
			for _, n := range topoLinks {
				key := b.toString(n["bk_obj_id"])
				if key == "" {
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
			nodes = append(nodes, Node{
				Name: RelationBusinessNode,
				Labels: map[string]string{
					"biz_id": b.toString(host.BkBizId),
				},
			})

			// 转换成指标加载到内存
			for _, rm := range nodes.toRelationMetrics() {
				key := rm.String()
				if _, ok := localMetrics[key]; !ok {
					localMetrics[key] = struct{}{}
				}
			}

			putRelationNodes(nodes)
		}
	}
	b.lock.Lock()
	b.metrics = localMetrics
	b.lock.Unlock()

	return nil
}

// String 以 string 格式获取所有指标数据
func (b *RelationMetricsBuilder) String() string {
	var buf bytes.Buffer
	b.lock.RLock()
	defer b.lock.RUnlock()

	for line := range b.metrics {
		buf.WriteString(line)
		buf.WriteString("\n")
	}
	return buf.String()
}

// SortString 以 string 格式排序之后获取所有指标数据
func (b *RelationMetricsBuilder) SortString() string {
	b.lock.RLock()
	defer b.lock.RUnlock()

	metrics := make([]string, 0, len(b.metrics))
	for line := range b.metrics {
		metrics = append(metrics, line)
	}
	sort.Strings(metrics)
	return strings.Join(metrics, "\n")
}
