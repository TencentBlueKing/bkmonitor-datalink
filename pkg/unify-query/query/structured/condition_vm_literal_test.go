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

	"github.com/VictoriaMetrics/metricsql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Condition values with control characters, quotes, backslashes or non-ASCII
// text are written as MetricsQL string literals that parse back to the exact
// value. Escaping only backslash and double quote left a raw newline, carriage
// return or tab inside the literal, and the whole query failed to parse.
func TestAllConditions_VMStringRoundTripsEveryValue(t *testing.T) {
	values := []struct {
		operator string
		value    string
	}{
		{ConditionEqual, "\t\r\nhttp://example.com"},
		{ConditionRegEqual, "/?dev/loop.*\r\n"},
		{ConditionEqual, "a\\b\"c"},
		{ConditionEqual, "中文 值"},
		{ConditionNotEqual, "nul\x00bell\x07"},
	}
	for _, v := range values {
		t.Run(v.value, func(t *testing.T) {
			conditions := AllConditions{{{DimensionName: "path", Operator: v.operator, Value: []string{v.value}}}}
			condition, _ := conditions.VMString("rt\n1", "metric\t1", false)

			expr, err := metricsql.Parse(condition.ToMatch())
			require.NoError(t, err, "condition %s does not parse", condition)
			metric, ok := expr.(*metricsql.MetricExpr)
			require.True(t, ok)
			got := map[string]string{}
			for _, filters := range metric.LabelFilterss {
				for _, filter := range filters {
					got[filter.Label] = filter.Value
				}
			}
			assert.Equal(t, v.value, got["path"])
			assert.Equal(t, "rt\n1", got["result_table_id"])
			assert.Equal(t, "metric\t1", got["__name__"])
		})
	}
}
