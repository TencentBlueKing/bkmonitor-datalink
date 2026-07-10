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
			expected:     selectAll,
		},
		{
			name:         "字符串字面量里的反引号不当作字段",
			selectFields: []string{"regexp_extract(`log`, '`user`=(\\\\d+)', 1) AS user_id"},
			expected:     "`log`",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, collectUnionSelectFields(tt.selectFields, tt.groupFields, tt.orderFields))
		})
	}
}
