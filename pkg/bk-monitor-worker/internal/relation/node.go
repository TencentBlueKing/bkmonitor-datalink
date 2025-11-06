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
	"strings"
)

type Nodes []Node

type Node struct {
	Name   string
	Labels map[string]string
}

func (n Node) ExpandInfoMetric() Metric {
	name := fmt.Sprintf("%s_info_relation", n.Name)
	labels := make([]Label, 0, len(n.Labels))
	for k, v := range n.Labels {
		labels = append(labels, Label{
			Name:  k,
			Value: v,
		})
	}

	sort.SliceStable(labels, func(i, j int) bool {
		return labels[i].Name < labels[j].Name
	})

	return Metric{
		Name:   name,
		Labels: labels,
	}
}

func (n Node) RelationMetric(nextNode Node) Metric {
	names := []string{n.Name, nextNode.Name}
	sort.Strings(names)

	keys := make([]string, 0)
	values := make(map[string]string)

	for _, labels := range []map[string]string{n.Labels, nextNode.Labels} {
		for k, v := range labels {
			if _, ok := values[k]; ok {
				continue
			}

			keys = append(keys, k)
			values[k] = v
		}
	}
	sort.Strings(keys)

	relationLabels := make([]Label, 0, len(keys))
	for _, k := range keys {
		relationLabels = append(relationLabels,
			Label{
				Name:  k,
				Value: values[k],
			},
		)
	}

	return Metric{
		Name:   fmt.Sprintf("%s_relation", strings.Join(names, "_with_")),
		Labels: relationLabels,
	}
}
