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
	"encoding/json"
	"fmt"
	"sort"
)

// replacedTopologyFields 是拓扑查询项已替换的旧字段及其新写法。旧字段不再生效，按名字拒绝，
// 避免调用方的方向或根条件被静默忽略。
var replacedTopologyFields = map[string]string{
	"dynamic_relation_direction": "direction",
	"source_type":                "sources[].source_type",
	"source_info":                "sources[].source_info",
	"source_expand_info":         "sources[].source_expand_info",
}

// UnmarshalJSON 解码拓扑查询项，带有已替换字段的项整体拒绝。
func (q *SharedTopologyQuery) UnmarshalJSON(data []byte) error {
	type plain SharedTopologyQuery
	if err := json.Unmarshal(data, (*plain)(q)); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	replaced := make([]string, 0)
	for field := range fields {
		if _, ok := replacedTopologyFields[field]; ok {
			replaced = append(replaced, field)
		}
	}
	if len(replaced) == 0 {
		return nil
	}
	sort.Strings(replaced)
	return fmt.Errorf("topology query field %q has been replaced by %q", replaced[0], replacedTopologyFields[replaced[0]])
}
