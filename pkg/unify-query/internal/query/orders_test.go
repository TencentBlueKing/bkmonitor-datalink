// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package query

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func TestOrders_SortSliceList(t *testing.T) {
	testCases := []struct {
		name     string
		orders   metadata.Orders
		list     []map[string]any
		expected []map[string]any
	}{
		{
			name: "test - 1",
			orders: metadata.Orders{
				{
					Name: "a",
					Ast:  false,
				},
				{
					Name: "b",
					Ast:  true,
				},
			},
			list: []map[string]any{
				{
					"a": "123456",
					"b": "abc",
				},
				{
					"a": "123457",
					"b": "abd",
				},
				{
					"a": "123456",
					"b": "abd",
				},
			},
			expected: []map[string]any{
				{
					"a": "123457",
					"b": "abd",
				},
				{
					"a": "123456",
					"b": "abc",
				},
				{
					"a": "123456",
					"b": "abd",
				},
			},
		},
		{
			name: "test - 2",
			orders: metadata.Orders{
				{
					Name: "a",
					Ast:  false,
				},
				{
					Name: "b",
					Ast:  false,
				},
			},
			list: []map[string]any{
				{
					"a": "123456",
					"b": "abc",
				},
				{
					"a": "123457",
					"b": "abd",
				},
				{
					"a": "123456",
					"b": "abd",
				},
			},
			expected: []map[string]any{
				{
					"a": "123457",
					"b": "abd",
				},
				{
					"a": "123456",
					"b": "abd",
				},
				{
					"a": "123456",
					"b": "abc",
				},
			},
		},
		{
			name: "test - 3",
			list: []map[string]any{
				{
					"a": "123456",
					"b": "abc",
				},
				{
					"a": "123457",
					"b": "abd",
				},
				{
					"a": "123456",
					"b": "abd",
				},
			},
			expected: []map[string]any{
				{
					"a": "123456",
					"b": "abc",
				},
				{
					"a": "123457",
					"b": "abd",
				},
				{
					"a": "123456",
					"b": "abd",
				},
			},
		},
		{
			name: "test - time",
			orders: metadata.Orders{
				{
					Name: "time",
					Ast:  false,
				},
			},
			list: []map[string]any{
				{
					"time": "1754466569000000002", // 2025-08-06 15:49:29
				},
				{
					"time": "2025-08-06T17:49:29.000000001Z",
				},
				{
					"time": "2025-08-06T17:49:29.000000002Z",
				},
				{
					"time": "1754466568000", // 2025-08-06 15:49:28
				},
				{
					"time": "2025-08-06T17:46:29.000000002Z",
				},
				{
					"time": "1754866568000", // 2025-08-11 06:56:08
				},
			},
			expected: []map[string]any{
				{
					"time": "1754866568000", // 2025-08-11 06:56:08
				},
				{
					"time": "2025-08-06T17:49:29.000000002Z",
				},
				{
					"time": "2025-08-06T17:49:29.000000001Z",
				},
				{
					"time": "2025-08-06T17:46:29.000000002Z",
				},
				{
					"time": "1754466569000000002", // 2025-08-06 15:49:29
				},
				{
					"time": "1754466568000", // 2025-08-06 15:49:28
				},
			},
		},
	}

	for _, c := range testCases {
		t.Run(c.name, func(t *testing.T) {
			SortSliceListWithTime(c.list, c.orders, map[string]string{
				"time": metadata.TypeDate,
			})
			assert.Equal(t, c.expected, c.list)
		})
	}
}
