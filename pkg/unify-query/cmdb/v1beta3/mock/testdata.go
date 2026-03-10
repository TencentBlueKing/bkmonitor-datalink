// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package mock

import (
	"fmt"
)

// PodData Pod 测试数据
type PodData struct {
	ClusterID   string
	Namespace   string
	Pod         string
	PeriodStart int64
	PeriodEnd   int64
}

// GetResourceID 获取资源 ID
func (p *PodData) GetResourceID() string {
	return fmt.Sprintf("pod:⟨bcs_cluster_id=%s,namespace=%s,pod=%s⟩", p.ClusterID, p.Namespace, p.Pod)
}

// NodeData Node 测试数据
type NodeData struct {
	ClusterID   string
	Node        string
	PeriodStart int64
	PeriodEnd   int64
}

// GetResourceID 获取资源 ID
func (n *NodeData) GetResourceID() string {
	return fmt.Sprintf("node:⟨bcs_cluster_id=%s,node=%s⟩", n.ClusterID, n.Node)
}

// EmptyResponse 空响应的 JSON 字符串
const EmptyResponse = `[{"status":"OK","result":null},{"status":"OK","result":[]}]`

// InfoForDBResponse INFO FOR DB 查询的响应
const InfoForDBResponse = `[{"status":"OK","result":null},{"status":"OK","result":{}}]`

// MockData 预定义的 Mock 数据
// key: 完整的 SQL 查询语句（包含 USE NS DB 前缀）
// value: JSON 格式的响应字符串
//
// 使用方式:
//
//	mock.SurrealDB.Set(mock.MockData)
//
// 或者追加自定义数据:
//
//	mock.SurrealDB.Set(map[string]any{
//	    "USE NS default DB test; SELECT * FROM ...": `[{"status":"OK","result":null},{"status":"OK","result":[...]}]`,
//	})
var MockData = map[string]any{
	// ========== 系统查询 ==========

	// INFO FOR DB - 数据库信息查询
	`USE NS default DB test; INFO FOR DB`: InfoForDBResponse,

	// ========== Pod liveness 查询 ==========
	// 基准时间: queryEnd=1736510400000 (2025-01-10 12:00:00 UTC)
	//          queryStart=1736424000000 (2025-01-09 12:00:00 UTC, 24小时前)
	//          tolerance=600000 (10分钟)
	// 实际查询: period_start <= 1736511000000, period_end >= 1736423400000

	// Pod liveness - BCS-K8S-00001, default, nginx-pod-1
	"USE NS default DB test; SELECT * FROM pod_liveness_record \nWHERE pod_id = 'pod:⟨bcs_cluster_id=BCS-K8S-00001,namespace=default,pod=nginx-pod-1⟩' \nAND period_start <= 1736511000000 \nAND period_end >= 1736423400000\nORDER BY period_start ASC\nLIMIT 1000": `[{"status":"OK","result":null},{"status":"OK","result":[{"id":"pod_liveness_record:1","pod_id":"pod:⟨bcs_cluster_id=BCS-K8S-00001,namespace=default,pod=nginx-pod-1⟩","period_start":1736424000000,"period_end":1736510400000}]}]`,

	// Pod liveness - 不存在的 Pod，返回空结果
	"USE NS default DB test; SELECT * FROM pod_liveness_record \nWHERE pod_id = 'pod:⟨bcs_cluster_id=BCS-K8S-00001,namespace=default,pod=non-existent-pod⟩' \nAND period_start <= 1736511000000 \nAND period_end >= 1736423400000\nORDER BY period_start ASC\nLIMIT 1000": EmptyResponse,

	// ========== Node liveness 查询 ==========

	// Node liveness - BCS-K8S-00001, node-1
	"USE NS default DB test; SELECT * FROM node_liveness_record \nWHERE node_id = 'node:⟨bcs_cluster_id=BCS-K8S-00001,node=node-1⟩' \nAND period_start <= 1736511000000 \nAND period_end >= 1736423400000\nORDER BY period_start ASC\nLIMIT 1000": `[{"status":"OK","result":null},{"status":"OK","result":[{"id":"node_liveness_record:1","node_id":"node:⟨bcs_cluster_id=BCS-K8S-00001,node=node-1⟩","period_start":1736424000000,"period_end":1736510400000}]}]`,

	// ========== 关联关系查询 ==========

	// Pod -> Node 关联关系 (node_with_pod)
	"USE NS default DB test; SELECT \n    id AS relation_id,\n    out AS from_id,\n    in AS to_id,\n    period_start,\n    period_end,\n    is_active\nFROM node_with_pod \nWHERE out = 'pod:⟨bcs_cluster_id=BCS-K8S-00001,namespace=default,pod=nginx-pod-1⟩' \nAND period_start <= 1736511000000 \nAND period_end >= 1736423400000\nORDER BY period_start ASC\nLIMIT 1000": `[{"status":"OK","result":null},{"status":"OK","result":[{"relation_id":"node_with_pod:1","from_id":"pod:⟨bcs_cluster_id=BCS-K8S-00001,namespace=default,pod=nginx-pod-1⟩","to_id":"node:⟨bcs_cluster_id=BCS-K8S-00001,node=node-1⟩","period_start":1736424000000,"period_end":1736510400000,"is_active":true}]}]`,
}
