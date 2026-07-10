package bksql

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/bksql/sql_expr"
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
			name:         "纯 COUNT star 使用常量投影",
			selectFields: []string{"COUNT(*) AS `_value_`"},
			expected:     unionDummyProjection,
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
			name:         "双引号字符串里的标识符不当作字段",
			selectFields: []string{`regexp_extract(log, "user=(\\d+)", 1) AS user_id`},
			expected:     "`log`",
		},
		{
			name:         "数字科学计数法不当作字段",
			selectFields: []string{"1e3"},
			expected:     unionDummyProjection,
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
		{
			name:         "算术乘法不是 wildcard",
			selectFields: []string{"a * b AS value"},
			expected:     "`a`, `b`",
		},
		{
			name:         "dotted 引用只收集 root 字段",
			selectFields: []string{"resource.bk.instance AS resource_instance", "`path`"},
			expected:     "`resource`, `path`",
		},
		{
			name:         "反引号 keyword 字段保留为真实字段",
			selectFields: []string{"`time`, `path`"},
			expected:     "`time`, `path`",
		},
		{
			name:         "DISTINCT star 按 wildcard 处理",
			selectFields: []string{"DISTINCT(*)"},
			expected:     selectAll,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, collectUnionSelectFields(tt.selectFields, tt.groupFields, tt.orderFields))
		})
	}
}

func TestQueryFactoryUnionSelectListValidation(t *testing.T) {
	tables := []string{"`db_b`.doris", "`db_a`.doris"}

	tests := []struct {
		name           string
		selectFields   []string
		tableFieldsMap TableFieldsMap
		expected       string
		errContains    string
	}{
		{
			name:         "字段存在且类型兼容",
			selectFields: []string{"`path`"},
			tableFieldsMap: TableFieldsMap{
				"`db_b`.doris": {"path": {FieldType: "text"}},
				"`db_a`.doris": {"path": {FieldType: "varchar(128)"}},
			},
			expected: "`path`",
		},
		{
			name:         "数组类型等价写法兼容",
			selectFields: []string{"`events`"},
			tableFieldsMap: TableFieldsMap{
				"`db_b`.doris": {"events": {FieldType: "ARRAY<TEXT>"}},
				"`db_a`.doris": {"events": {FieldType: "TEXT ARRAY"}},
			},
			expected: "`events`",
		},
		{
			name:         "缺失字段返回明确错误",
			selectFields: []string{"`path`"},
			tableFieldsMap: TableFieldsMap{
				"`db_b`.doris": {"path": {FieldType: "text"}},
				"`db_a`.doris": {"log": {FieldType: "text"}},
			},
			errContains: "missing",
		},
		{
			name:         "对象 root 投影允许 leaf schema 校验",
			selectFields: []string{"`dimensions`"},
			tableFieldsMap: TableFieldsMap{
				"`db_b`.doris": {"dimensions.pipelineName": {FieldType: "text"}},
				"`db_a`.doris": {"dimensions.pipelineName": {FieldType: "varchar(128)"}},
			},
			expected: "`dimensions`",
		},
		{
			name:         "类型不兼容返回明确错误",
			selectFields: []string{"`path`"},
			tableFieldsMap: TableFieldsMap{
				"`db_b`.doris": {"path": {FieldType: "text"}},
				"`db_a`.doris": {"path": {FieldType: "bigint"}},
			},
			errContains: "type mismatch",
		},
		{
			name:         "JSON 类型不自动投影",
			selectFields: []string{"`payload`"},
			tableFieldsMap: TableFieldsMap{
				"`db_b`.doris": {"payload": {FieldType: "json"}},
				"`db_a`.doris": {"payload": {FieldType: "json"}},
			},
			errContains: "unsupported type",
		},
		{
			name:         "multi table SELECT star 不再静默生成 DB-side union",
			selectFields: []string{"*"},
			tableFieldsMap: TableFieldsMap{
				"`db_b`.doris": {"path": {FieldType: "text"}},
				"`db_a`.doris": {"path": {FieldType: "text"}},
			},
			errContains: "SELECT *",
		},
		{
			name:         "无真实字段依赖时使用常量投影",
			selectFields: []string{"COUNT(*) AS `_value_`"},
			tableFieldsMap: TableFieldsMap{
				"`db_b`.doris": {"path": {FieldType: "text"}},
				"`db_a`.doris": {"extra": {FieldType: "bigint"}},
			},
			expected: unionDummyProjection,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query := &metadata.Query{Measurement: sql_expr.Doris}
			f := NewQueryFactory(context.Background(), query).WithTableFieldsMap(tt.tableFieldsMap)
			got, err := f.unionSelectList(tt.selectFields, nil, nil, tables)
			if tt.errContains != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestQueryFactoryUnionSelectListAllowsHDFSSelectAll(t *testing.T) {
	query := &metadata.Query{
		Measurement: "hdfs",
	}
	tables := []string{"`db_b`.hdfs", "`db_a`.hdfs"}
	tableFieldsMap := TableFieldsMap{
		"`db_b`.hdfs": {"path": {FieldType: "text"}},
		"`db_a`.hdfs": {"path": {FieldType: "text"}},
	}

	f := NewQueryFactory(context.Background(), query).WithTableFieldsMap(tableFieldsMap)
	got, err := f.unionSelectList([]string{"*"}, nil, nil, tables)

	require.NoError(t, err)
	assert.Equal(t, selectAll, got)
}
