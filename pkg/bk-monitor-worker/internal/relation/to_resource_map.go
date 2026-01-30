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
	"fmt"
	"sort"

	"github.com/spf13/cast"
)

// ConvertToResourceMapToMetrics 将 ToResourceMap 转换为 Prometheus 关系指标
// 支持动态资源类型和属性，生成格式为 {resource_type}_with_{topology_level}_relation
func ConvertToResourceMapToMetrics(
	toResourceMap map[string]map[string]map[string]any,
	topologyID string,
	topologyLevel string,
	bizID int,
) []Metric {
	if toResourceMap == nil || len(toResourceMap) == 0 {
		return []Metric{}
	}

	metrics := make([]Metric, 0)

	// 遍历拓扑层级（set, host, module 等）
	for level, resourceTypes := range toResourceMap {
		// 只处理匹配当前拓扑层级的数据
		if level != topologyLevel {
			continue
		}

		// 收集资源类型并排序，保证输出顺序一致
		resourceTypeKeys := make([]string, 0, len(resourceTypes))
		for rt := range resourceTypes {
			resourceTypeKeys = append(resourceTypeKeys, rt)
		}
		sort.Strings(resourceTypeKeys)

		// 遍历资源类型（app_version, service_name 等）
		for _, resourceType := range resourceTypeKeys {
			attributes := resourceTypes[resourceType]

			// 生成指标名: {resource_type}_with_{topology_level}_relation
			metricName := fmt.Sprintf("%s_with_%s_relation", resourceType, level)

			// 构建标签：资源属性 + 拓扑ID + biz_id
			labels := make([]Label, 0, len(attributes)+2)

			// 添加资源属性作为标签（排序保证一致）
			attrKeys := make([]string, 0, len(attributes))
			for k := range attributes {
				attrKeys = append(attrKeys, k)
			}
			sort.Strings(attrKeys)

			for _, k := range attrKeys {
				v := attributes[k]
				strValue := cast.ToString(v)
				if strValue == "" {
					// 跳过空值
					continue
				}
				labels = append(labels, Label{
					Name:  k,
					Value: strValue,
				})
			}

			// 添加 bk_biz_id
			labels = append(labels, Label{
				Name:  "bk_biz_id",
				Value: fmt.Sprintf("%d", bizID),
			})

			// 添加拓扑层级 ID（如 bk_host_id, bk_set_id）
			topologyIDLabel := fmt.Sprintf("bk_%s_id", level)
			labels = append(labels, Label{
				Name:  topologyIDLabel,
				Value: topologyID,
			})

			// 标签排序保证一致性
			sort.Slice(labels, func(i, j int) bool {
				return labels[i].Name < labels[j].Name
			})

			metrics = append(metrics, Metric{
				Name:   metricName,
				Labels: labels,
			})
		}
	}

	return metrics
}
