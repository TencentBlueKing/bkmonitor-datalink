// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package structured

import (
	"testing"

	"github.com/prometheus/prometheus/promql/parser"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// TestFuncToProm
func TestFuncToProm(t *testing.T) {
	log.InitTestLogger()

	testData := []struct {
		request AggregateMethod
		result  string
	}{
		{
			request: AggregateMethod{
				Method: "count",
			},
			result: "count(\"test\")",
		},
		{
			request: AggregateMethod{
				Method:    "quantile",
				VArgsList: []any{0.9},
			},
			result: "quantile(0.9, \"test\")",
		},
	}

	for index, data := range testData {
		result, err := data.request.ToProm(&parser.StringLiteral{Val: "test"})
		assert.Nil(t, err, "err must not nil")
		assert.Equal(t, data.result, result.String(), index)
		// assert.Equal(t, data.result.Op, result.Op, "op assert with index->[%d]", index)
		// assert.Equal(t, data.result.Param, result.Param, "op assert with index->[%d]", index)
	}
}
