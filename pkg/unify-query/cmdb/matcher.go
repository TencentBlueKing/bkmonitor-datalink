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

func (m Matcher) Rename() Matcher {
	nameMap := map[string]string{
		"pod_name":       "pod",
		"container_name": "container",
	}
	newMatcher := make(Matcher, len(m))
	for k, v := range m {
		// 值为空会导致查询扩散，所以需要跳过
		if v == "" {
			continue
		}

		var (
			nk string
			ok bool
		)
		if nk, ok = nameMap[k]; !ok {
			nk = k
		}
		newMatcher[nk] = v
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
