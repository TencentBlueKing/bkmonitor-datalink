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
	"testing"

	"github.com/stretchr/testify/require"

	internaljson "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
)

// 已替换的字段按名字拒绝，并指出新写法；HTTP 入口使用的 JSON 实现也走同一校验。
func TestSharedTopologyQueryRejectsReplacedFields(t *testing.T) {
	for _, decode := range map[string]func([]byte, any) error{"encoding/json": json.Unmarshal, "internal/json": internaljson.Unmarshal} {
		for field, replacement := range map[string]string{
			`"dynamic_relation_direction":"outbound"`: `"dynamic_relation_direction" has been replaced by "direction"`,
			`"source_type":"pod"`:                     `"source_type" has been replaced by "sources[].source_type"`,
			`"source_info":{"pod":"p"}`:               `"source_info" has been replaced by "sources[].source_info"`,
			`"source_expand_info":{"version":"v"}`:    `"source_expand_info" has been replaced by "sources[].source_expand_info"`,
		} {
			var request SharedTopologyRequest
			err := decode([]byte(`{"query_list":[{"timestamp":1700000000,"sources":[{"source_type":"pod"}],`+field+`}]}`), &request)
			require.ErrorContains(t, err, replacement)
		}
		var request SharedTopologyRequest
		require.NoError(t, decode([]byte(`{"query_list":[{"timestamp":1700000000,"direction":"in","relation_groups":["BELONG_TO"],"sources":[{"source_type":"pod","source_info":{"pod":"p"},"source_expand_info":{"version":"v"}},{"source_type":"node"}]}]}`), &request))
		require.Len(t, request.QueryList, 1)
		query := request.QueryList[0]
		require.Equal(t, "in", query.Direction)
		require.Equal(t, []string{"BELONG_TO"}, query.RelationGroups)
		require.Equal(t, []TopologySource{
			{SourceType: "pod", SourceInfo: Matcher{"pod": "p"}, SourceExpandInfo: Matcher{"version": "v"}},
			{SourceType: "node"},
		}, query.Sources)
	}
}
