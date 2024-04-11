// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package dataflow

import (
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
)

// StreamSourceNode 数据源节点
type StreamSourceNode struct {
	BaseNode
	SourceRtId string
}

func NewStreamSourceNode(sourceRtId string) *StreamSourceNode {
	n := &StreamSourceNode{SourceRtId: sourceRtId}
	n.NodeType = "stream_source"
	n.Instance = n
	return n
}

func (n StreamSourceNode) Equal(other map[string]interface{}) bool {
	config := n.Instance.Config()
	if equal, _ := jsonx.CompareObjects(config["from_result_table_ids"], other["from_result_table_ids"]); equal {
		if equal, _ := jsonx.CompareObjects(config["table_name"], other["table_name"]); equal {
			return true
		}
	}
	return false
}

func (n StreamSourceNode) Name() string {
	return fmt.Sprintf("%s(%s)", n.GetNodeType(), n.SourceRtId)
}

func (n StreamSourceNode) OutputTableName() string {
	return n.SourceRtId
}

func (n StreamSourceNode) Config() map[string]interface{} {
	return map[string]interface{}{
		"from_result_table_ids": []string{n.SourceRtId},
		"result_table_id":       n.SourceRtId,
		"name":                  n.Instance.Name(),
	}
}

// RelationSourceNode 关联数据源
type RelationSourceNode struct {
	StreamSourceNode
}

func NewRelationSourceNode(sourceRtId string) *RelationSourceNode {
	n := &RelationSourceNode{StreamSourceNode: *NewStreamSourceNode(sourceRtId)}
	n.NodeType = "redis_kv_source"
	n.Instance = n
	return n
}
