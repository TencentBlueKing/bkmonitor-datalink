package bksql

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCollectUnionSelectFields(t *testing.T) {
	tests := []struct {
		name         string
		selectFields []string
		groupFields  []string
		orderFields  []string
		expected     string
	}{
		{
			name:         "raw 查询包含 wildcard 时保留全部列",
			selectFields: []string{"*", "`value` AS `_value_`", "`dtEventTimeStamp` AS `_timestamp_`"},
			expected:     selectAll,
		},
		{
			name:         "聚合 order by 外层 alias 不下推",
			selectFields: []string{"COUNT(*) AS `_value_`"},
			groupFields:  []string{"`path`"},
			orderFields:  []string{"`_value_` DESC"},
			expected:     "`path`",
		},
		{
			name:         "未加反引号的系统字段依赖保守回退",
			selectFields: []string{"HISTOGRAM(`value`, dtEventTimeStamp) AS `_value_`"},
			expected:     "`value`, `dtEventTimeStamp`",
		},
		{
			name:         "字符串字面量里的反引号不当作字段",
			selectFields: []string{"regexp_extract(`log`, '`user`=(\\\\d+)', 1) AS user_id"},
			expected:     "`log`",
		},
		{
			name:         "COUNT star 不增加字段依赖",
			selectFields: []string{"`minute1`", "COUNT(*) AS log_count"},
			groupFields:  []string{"`minute1`"},
			orderFields:  []string{"`minute1` DESC"},
			expected:     "`minute1`",
		},
		{
			name:         "CAST 对象字段表达式收集未加反引号 root",
			selectFields: []string{"CAST(resource['bk.instance.id'] AS STRING) AS `resource__bk_46__bk__bk_46__instance__bk_46__id`", "`path`"},
			groupFields:  []string{"`resource__bk_46__bk__bk_46__instance__bk_46__id`", "`path`"},
			expected:     "`resource`, `path`",
		},
		{
			name:         "自定义时间字段未加反引号时参与投影",
			selectFields: []string{"HISTOGRAM(`value`, customTimeField) AS `_value_`"},
			expected:     "`value`, `customTimeField`",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, collectUnionSelectFields(tt.selectFields, tt.groupFields, tt.orderFields))
		})
	}
}
