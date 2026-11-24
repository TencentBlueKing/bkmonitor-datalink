// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package onemodel

import (
	"fmt"
	"strings"
	"unicode"
)

// InstanceRef 是 TargetDescriptorV1 的完整 canonical 身份，实例 ID 保留不透明字符串。
type InstanceRef struct {
	ModelID    string `json:"model_id"`
	InstanceID string `json:"model_inst_id"`
	EntityUID  string `json:"entity_uid"`
}

// TargetSelector 是互斥的静态实例、CMDB 拓扑或 Kingeye 动态分组选择器。
// 目标展开时必须使用调用方租户与业务空间，不能把目录缓存当作成员结果。
type TargetSelector struct {
	Type           string        `json:"type"`
	Instances      []InstanceRef `json:"instances,omitempty"`
	Provider       string        `json:"provider,omitempty"`
	TopologyNodeID string        `json:"topology_node_id,omitempty"`
	BizID          *int64        `json:"bk_biz_id,omitempty"`
	DynamicGroupID string        `json:"dynamic_group_id,omitempty"`
}

// TargetDescriptor 保留 OneModel V1 选择协议，不保存展开后的快照。
type TargetDescriptor struct {
	SchemaVersion int              `json:"schema_version"`
	ModelID       string           `json:"model_id"`
	Selectors     []TargetSelector `json:"selectors"`
}

// Validate 校验 selector 的互斥字段、canonical 身份及完整集合上限，不进行网络查询。
func (d TargetDescriptor) Validate() error {
	if d.SchemaVersion != 1 || !selectionText(d.ModelID, 128) || strings.Contains(d.ModelID, "|") || len(d.Selectors) == 0 || len(d.Selectors) > 64 {
		return fmt.Errorf("invalid target descriptor version/model/selectors")
	}
	explicit := 0
	for i, s := range d.Selectors {
		switch s.Type {
		case "instances":
			if len(s.Instances) == 0 || len(s.Instances) > 2000 || s.Provider != "" || s.TopologyNodeID != "" || s.BizID != nil || s.DynamicGroupID != "" {
				return fmt.Errorf("selectors[%d]: invalid instances selector", i)
			}
			explicit += len(s.Instances)
			for _, item := range s.Instances {
				if item.ModelID != d.ModelID || !selectionText(item.InstanceID, 1024) || item.EntityUID != item.ModelID+"|"+item.InstanceID {
					return fmt.Errorf("selectors[%d]: canonical instance identity mismatch", i)
				}
			}
		case "topo_node":
			if len(s.Instances) != 0 || s.Provider != "cmdb_mainline" || !selectionText(s.TopologyNodeID, 256) || s.DynamicGroupID != "" || (s.BizID != nil && *s.BizID < 1) {
				return fmt.Errorf("selectors[%d]: invalid topology selector", i)
			}
		case "dynamic_group":
			if len(s.Instances) != 0 || s.Provider != "kingeye" || !selectionText(s.DynamicGroupID, 128) || s.TopologyNodeID != "" || s.BizID != nil {
				return fmt.Errorf("selectors[%d]: invalid dynamic group selector", i)
			}
		default:
			return fmt.Errorf("selectors[%d]: unsupported selector type", i)
		}
	}
	if explicit > 10000 {
		return fmt.Errorf("target descriptor exceeds 10000 explicit instances")
	}
	return nil
}

func selectionText(value string, limit int) bool {
	if value == "" || len(value) > limit {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || r == unicode.ReplacementChar {
			return false
		}
	}
	return true
}
