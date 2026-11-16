// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cmdb

import (
	"github.com/prometheus/prometheus/model/labels"
)

// matcherAliases 把采集侧的标签名映射到资源主键名，例如 kube-state-metrics 用 job_name 表示 Job，
// 因为 job 已是 Prometheus 的采集任务标签。
var matcherAliases = map[string]string{
	"pod_name":       "pod",
	"container_name": "container",
	"job_name":       "job",
}

// Rename 把别名改成资源主键名并丢弃空值。别名与主键名同时出现时以别名为准：
// 别名明确指资源，同名的主键名可能是别的含义（如采集任务 job）。
func (m Matcher) Rename() Matcher {
	newMatcher := make(Matcher, len(m))
	for k, v := range m {
		// 值为空会导致查询扩散，所以需要跳过
		if v == "" {
			continue
		}
		if _, ok := matcherAliases[k]; ok {
			continue
		}
		newMatcher[k] = v
	}
	for alias, name := range matcherAliases {
		if v := m[alias]; v != "" {
			newMatcher[name] = v
		}
	}
	return newMatcher
}

func (m Matcher) ToPromMatcher() ([]*labels.Matcher, error) {
	newMatcher := make([]*labels.Matcher, 0, len(m))
	for k, v := range m {
		matcher, err := labels.NewMatcher(labels.MatchEqual, k, v)
		if err != nil {
			return nil, err
		}
		newMatcher = append(newMatcher, matcher)
	}
	return newMatcher, nil
}
