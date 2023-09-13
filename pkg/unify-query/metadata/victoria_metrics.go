// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metadata

import (
	"fmt"

	"github.com/prometheus/prometheus/model/labels"
)

type VmExpand struct {
	ResultTableGroup      map[string][]string
	MetricAliasMapping    map[string]string
	MetricFilterCondition map[string]string
	LabelsMatcher         map[string][]*labels.Matcher
}

// MetricResultTableGroup 合并 resultTable 和 metric
func (e *VmExpand) MetricResultTableGroup() (map[string][]string, error) {
	if len(e.ResultTableGroup) == 0 {
		return nil, fmt.Errorf("vm query result table is empty")
	}
	metricResultTableGroup := make(map[string][]string, len(e.ResultTableGroup))
	for name, rtg := range e.ResultTableGroup {
		if metric, ok := e.MetricAliasMapping[name]; ok {
			metricResultTableGroup[metric] = rtg
		} else {
			return nil, fmt.Errorf("metric is not found: %s in %+v", name, e.MetricAliasMapping)
		}
	}
	return metricResultTableGroup, nil
}
