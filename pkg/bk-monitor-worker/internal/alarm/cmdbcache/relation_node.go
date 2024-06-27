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
	"fmt"
	"sort"
	"strings"
	"sync"
)

const (
	RelationAgentNode    = "agent"
	RelationSystemNode   = "system"
	RelationBusinessNode = "business"
)

var relationNodesPool = sync.Pool{
	New: func() any {
		return Nodes{}
	},
}

func getRelationNodes() Nodes {
	return relationNodesPool.Get().(Nodes)
}

func putRelationNodes(nodes Nodes) {
	nodes = nodes[:0]
	relationNodesPool.Put(nodes)
}

type Nodes []Node

type Node struct {
	Name   string
	Labels map[string]string
}

func (ns Nodes) toRelationMetrics() []RelationMetric {
	// 关联节点必须要 2 个以上
	if len(ns) < 2 {
		return nil
	}
	relationMetrics := make([]RelationMetric, 0, len(ns)-1)
	for i := 0; i < len(ns)-1; i++ {
		relationMetrics = append(relationMetrics,
			ns[i].RelationMetric(ns[i+1]),
		)
	}
	return relationMetrics
}

func (n Node) RelationMetric(nextNode Node) RelationMetric {
	names := []string{n.Name, nextNode.Name}
	sort.Strings(names)

	totalNum := len(n.Labels) + len(nextNode.Labels)
	keys := make([]string, 0, totalNum)
	values := make(map[string]string, totalNum)

	for _, labels := range []map[string]string{n.Labels, nextNode.Labels} {
		for k, v := range labels {
			keys = append(keys, k)
			values[k] = v
		}
	}
	sort.Strings(keys)

	relationLabels := make([]RelationLabel, 0, len(keys))
	for _, k := range keys {
		relationLabels = append(relationLabels,
			RelationLabel{
				Name:  k,
				Value: values[k],
			},
		)
	}

	return RelationMetric{
		Name:   fmt.Sprintf("%s_relation", strings.Join(names, "_with_")),
		Labels: relationLabels,
	}
}
