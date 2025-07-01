// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package sql_parser

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

func TestParseWithFieldAlias(t *testing.T) {
	testCases := []struct {
		name     string
		q        string
		expected string
	}{
		{
			name: "test - 1",
			q: `SELECT
  JSON_EXTRACT_STRING (__ext, '$.io_kubernetes_pod_namespace') as ns,
  split_part (log, '|', 3) as ct,
  count(*)
WHERE
 log MATCH_ALL 'Reliable RPC called out of limit'
group by
  ns,
  ct
LIMIT
  1000`,
		},
		{
			name: "test - 2",
			q: `SELECT
  serverIp,
  COUNT(*) AS log_count
WHERE
  log MATCH_PHRASE 'Error' OR log MATCH_PHRASE 'Fatal'
GROUP BY
  serverIp
LIMIT
  1000
`,
		},
	}

	mock.Init()
	fieldMap := map[string]string{
		"namespace": "__ext.io_kubernetes_pod_namespace",
	}

	for _, c := range testCases {
		t.Run(c.name, func(t *testing.T) {
			actual, err := ParseWithFieldAlias(c.q, fieldMap)

			assert.Nil(t, err)
			assert.Equal(t, c.expected, actual)
		})
	}
}
