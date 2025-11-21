// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta1

import (
	"context"
	"testing"
	"time"

	"github.com/dominikbraun/graph"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

// BenchmarkGraphQuery_New 测试创建 GraphQuery 的性能
func BenchmarkGraphQuery_New(b *testing.B) {
	b.ReportAllocs() // 报告内存分配
	for i := 0; i < b.N; i++ {
		_ = graph.New(graph.IntHash, graph.Directed())
	}
}

// BenchmarkGraphQuery_BuildSmallGraph 测试构建小规模图的性能
func BenchmarkGraphQuery_BuildSmallGraph(b *testing.B) {
	b.ReportAllocs() // 报告内存分配
	for i := 0; i < b.N; i++ {
		g := graph.New(graph.IntHash, graph.Directed())
		// 构建包含10个节点的小图
		for j := 0; j < 10; j++ {
			_ = g.AddVertex(j)
		}

		// 添加一些边
		for j := 0; j < 9; j++ {
			_ = g.AddEdge(j, j+1)
		}
	}
}

// BenchmarkGraphQuery_BuildMediumGraph 测试构建中等规模图的性能
func BenchmarkGraphQuery_BuildMediumGraph(b *testing.B) {
	b.ReportAllocs() // 报告内存分配
	for i := 0; i < b.N; i++ {
		g := graph.New(graph.IntHash, graph.Directed())
		// 构建包含100个节点的中等图
		for j := 0; j < 100; j++ {
			_ = g.AddVertex(j)
		}

		// 添加边，形成链式结构
		for j := 0; j < 99; j++ {
			_ = g.AddEdge(j, j+1)
		}
	}
}

// BenchmarkGraphQuery_BuildLargeGraph 测试构建大规模图的性能
func BenchmarkGraphQuery_BuildLargeGraph(b *testing.B) {
	b.ReportAllocs() // 报告内存分配
	b.StopTimer()

	// 预先生成节点和边数据，避免在计时中包含数据生成时间
	nodes := make([]int, 1000)
	for i := 0; i < 1000; i++ {
		nodes[i] = i
	}

	b.StartTimer()

	for i := 0; i < b.N; i++ {
		g := graph.New(graph.IntHash, graph.Directed())
		// 构建包含1000个节点的大图
		for _, node := range nodes {
			_ = g.AddVertex(node)
		}

		// 添加边，形成链式结构
		for j := 0; j < 999; j++ {
			_ = g.AddEdge(nodes[i], nodes[i+1])
		}
	}
}

// BenchmarkGraphQuery_Traversal 测试图遍历的性能
func BenchmarkGraphQuery_Traversal(b *testing.B) {
	b.ReportAllocs() // 报告内存分配
	// 先构建一个测试图
	g := graph.New(graph.IntHash, graph.Directed())

	// 构建包含50个节点的图
	nodes := make([]int, 50)
	for i := 0; i < 50; i++ {
		nodes[i] = i
		_ = g.AddVertex(nodes[i])
	}

	// 添加边，形成链式结构
	for i := 0; i < 49; i++ {
		_ = g.AddEdge(nodes[i], nodes[i+1])
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// 测试图的遍历性能
		_, _ = g.Order()
		_, _ = g.Size()
	}
}

// BenchmarkGraphQuery_BuildClusterRelation 测试构建集群关系图的性能
func BenchmarkGraphQuery_BuildClusterRelation(b *testing.B) {
	b.ReportAllocs() // 报告内存分配
	b.StopTimer()

	// 预先生成节点和边数据，避免在计时中包含数据生成时间

	nodes := make([]int, 1000)
	for i := 0; i < 1000; i++ {
		nodes[i] = i
	}

	b.StartTimer()

	for i := 0; i < b.N; i++ {
		g := graph.New(graph.IntHash, graph.Directed())

		// 构建包含1000个节点的大图
		for _, node := range nodes {
			_ = g.AddVertex(node)
		}

		// 添加边，形成链式结构
		for j := 0; j < 999; j++ {
			_ = g.AddEdge(nodes[j], nodes[j+1])
		}
	}
}

func TestTimeGraph_MakeQueryTs(t *testing.T) {
	spaceUID := "test-space"

	testCases := map[string]struct {
		labels    map[string]string
		relations []cmdb.Relation
		expected  []string
	}{
		"test_1": {
			labels: map[string]string{
				"namespace": "blueking",
			},
			relations: []cmdb.Relation{
				{V: []cmdb.Resource{"pod", "container"}},
				{V: []cmdb.Resource{"node", "pod"}},
				{V: []cmdb.Resource{"node", "system"}},
			},
			expected: []string{
				`count by (bcs_cluster_id, container, namespace, pod) (count_over_time(bkmonitor:container_with_pod_relation{namespace="blueking"}[1m]))`,
				`count by (bcs_cluster_id, namespace, node, pod) (count_over_time(bkmonitor:node_with_pod_relation{namespace="blueking"}[1m]))`,
				`count by (bcs_cluster_id, bk_target_ip, node) (count_over_time(bkmonitor:node_with_system_relation[1m]))`,
			},
		},
	}

	start := time.Unix(1763636985, 0)
	end := time.Unix(1763640585, 0)
	step := time.Minute

	ctx := metadata.InitHashID(context.Background())
	tg := NewTimeGraph()

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			queryTsList, err := tg.MakeQueryTsList(ctx, spaceUID, c.labels, start, end, step, c.relations...)
			assert.NoError(t, err)

			for i, queryTs := range queryTsList {
				promql, err := queryTs.ToPromQL(ctx)
				assert.NoError(t, err)
				assert.Equal(t, c.expected[i], promql)
			}
		})
	}
}
