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
			name:         "Doris match 操作符不当作字段",
			selectFields: []string{"`log` MATCH_ANY 'x' AS matched"},
			orderFields:  []string{"`message` MATCH_PHRASE_EDGE 'y' DESC", "`path` MATCH_PHRASE_PREFIX 'z' DESC", "`trace_id` MATCH_REGEXP '.*' DESC"},
			expected:     "`log`, `message`, `path`, `trace_id`",
		},
		{
			name:         "DISTINCT star 按 wildcard 处理",
			selectFields: []string{"DISTINCT(*)"},
			expected:     selectAll,
		},
		{
			name:         "qualified wildcard 保守保留 select all",
			selectFields: []string{"t.*"},
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
		expectedErr    string
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
			expectedErr: "doris multi-table union field `path` is missing from table `db_a`.doris",
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
			name:         "对象表达式按请求 leaf 校验而不是任意 root 前缀",
			selectFields: []string{"CAST(resource['bk.instance.id'] AS STRING) AS `resource_id`"},
			tableFieldsMap: TableFieldsMap{
				"`db_b`.doris": {
					"resource.bk.instance.id": {FieldType: "text"},
					"resource.retry_count":    {FieldType: "int"},
				},
				"`db_a`.doris": {
					"resource.bk.instance.id": {FieldType: "varchar(128)"},
					"resource.retry_count":    {FieldType: "double"},
				},
			},
			expected: "`resource`",
		},
		{
			name:         "对象表达式缺失请求 leaf 时返回错误",
			selectFields: []string{"CAST(resource['bk.instance.id'] AS STRING) AS `resource_id`"},
			tableFieldsMap: TableFieldsMap{
				"`db_b`.doris": {
					"resource.bk.instance.id": {FieldType: "text"},
					"resource.retry_count":    {FieldType: "int"},
				},
				"`db_a`.doris": {
					"resource.retry_count": {FieldType: "int"},
				},
			},
			expectedErr: "doris multi-table union field `resource` is missing from table `db_a`.doris",
		},
		{
			name:         "类型不兼容返回明确错误",
			selectFields: []string{"`path`"},
			tableFieldsMap: TableFieldsMap{
				"`db_b`.doris": {"path": {FieldType: "text"}},
				"`db_a`.doris": {"path": {FieldType: "bigint"}},
			},
			expectedErr: "doris multi-table union field `path` type mismatch: table `db_b`.doris has text, table `db_a`.doris has bigint",
		},
		{
			name:         "JSON 类型不自动投影",
			selectFields: []string{"`payload`"},
			tableFieldsMap: TableFieldsMap{
				"`db_b`.doris": {"payload": {FieldType: "json"}},
				"`db_a`.doris": {"payload": {FieldType: "json"}},
			},
			expectedErr: "doris multi-table union field `payload` in table `db_b`.doris has unsupported type json",
		},
		{
			name:         "multi table SELECT star 嵌套字段按完整字段名取交集",
			selectFields: []string{"*"},
			tableFieldsMap: TableFieldsMap{
				"`db_b`.doris": {
					"dimensions.pipelineName": {FieldType: "text"},
					"dimensions.retry_count":  {FieldType: "int"},
				},
				"`db_a`.doris": {
					"dimensions.pipelineName": {FieldType: "varchar(128)"},
					"dimensions.retry_count":  {FieldType: "double"},
					"dimensions.only_current": {FieldType: "varchar(128)"},
				},
			},
			expected: "CAST(dimensions['pipelineName'] AS TEXT) AS `dimensions.pipelineName`",
		},
		{
			name:         "multi table SELECT star 嵌套字段使用安全公共 cast 类型",
			selectFields: []string{"*"},
			tableFieldsMap: TableFieldsMap{
				"`db_b`.doris": {
					"dimensions.pipelineName": {FieldType: "varchar(128)"},
					"dimensions.retry_count":  {FieldType: "int"},
				},
				"`db_a`.doris": {
					"dimensions.pipelineName": {FieldType: "text"},
					"dimensions.retry_count":  {FieldType: "bigint"},
				},
			},
			expected: "CAST(dimensions['pipelineName'] AS TEXT) AS `dimensions.pipelineName`, CAST(dimensions['retry_count'] AS BIGINT) AS `dimensions.retry_count`",
		},
		{
			name:         "multi table SELECT star 嵌套 decimal 字段保持精确 cast 类型",
			selectFields: []string{"*"},
			tableFieldsMap: TableFieldsMap{
				"`db_b`.doris": {
					"dimensions.amount": {FieldType: "decimal(20,4)"},
				},
				"`db_a`.doris": {
					"dimensions.amount": {FieldType: "decimal(30,8)"},
				},
			},
			expected: "CAST(dimensions['amount'] AS DECIMAL(30,8)) AS `dimensions.amount`",
		},
		{
			name:         "multi table SELECT star 跳过超过 Doris precision 上限的 decimal 字段",
			selectFields: []string{"*"},
			tableFieldsMap: TableFieldsMap{
				"`db_b`.doris": {
					"dimensions.amount": {FieldType: "decimal(38,18)"},
					"path":              {FieldType: "text"},
				},
				"`db_a`.doris": {
					"dimensions.amount": {FieldType: "decimal(38,0)"},
					"path":              {FieldType: "varchar(128)"},
				},
			},
			expected: "`path`",
		},
		{
			name:         "multi table SELECT star 转换成公共字段投影",
			selectFields: []string{"*"},
			tableFieldsMap: TableFieldsMap{
				"`db_b`.doris": {
					"dimensions.pipelineName": {FieldType: "text"},
					"dimensions.retry_count":  {FieldType: "int"},
					"path":                    {FieldType: "text"},
					"status":                  {FieldType: "text"},
					"extra":                   {FieldType: "bigint"},
				},
				"`db_a`.doris": {
					"dimensions.pipelineName": {FieldType: "varchar(128)"},
					"dimensions.retry_count":  {FieldType: "double"},
					"dimensions.only_current": {FieldType: "varchar(128)"},
					"path":                    {FieldType: "varchar(128)"},
					"status":                  {FieldType: "bigint"},
				},
			},
			expected: "CAST(dimensions['pipelineName'] AS TEXT) AS `dimensions.pipelineName`, `path`",
		},
		{
			name:         "multi table SELECT star 字段依赖按大小写不敏感匹配",
			selectFields: []string{"*", "`dtEventTimeStamp` AS `_timestamp_`"},
			tableFieldsMap: TableFieldsMap{
				"`db_b`.doris": {
					"dteventtimestamp": {FieldType: "bigint"},
				},
				"`db_a`.doris": {
					"dteventtimestamp": {FieldType: "int"},
				},
			},
			expected: "`dteventtimestamp`",
		},
		{
			name:         "multi table SELECT star 保留显式依赖字段",
			selectFields: []string{"*", "`value` AS `_value_`"},
			tableFieldsMap: TableFieldsMap{
				"`db_b`.doris": {
					"path":  {FieldType: "text"},
					"value": {FieldType: "bigint"},
					"extra": {FieldType: "bigint"},
				},
				"`db_a`.doris": {
					"path":  {FieldType: "varchar(128)"},
					"value": {FieldType: "int"},
				},
			},
			expected: "`path`, `value`",
		},
		{
			name:         "multi table SELECT star 显式依赖字段不参与交集剔除",
			selectFields: []string{"*", "`value` AS `_value_`"},
			tableFieldsMap: TableFieldsMap{
				"`db_b`.doris": {
					"path":  {FieldType: "text"},
					"value": {FieldType: "bigint"},
				},
				"`db_a`.doris": {"path": {FieldType: "varchar(128)"}},
			},
			// `*` 可按 schema 交集保留 `path`，但外层显式依赖的 `value`
			// 不能被静默丢弃；db_a 缺少该字段时必须返回明确错误。
			expectedErr: "doris multi-table union field `value` is missing from table `db_a`.doris",
		},
		{
			name:         "multi table SELECT star 拒绝额外对象依赖字段",
			selectFields: []string{"*", "CAST(dimensions['pipelineName'] AS TEXT) AS `pipeline_name`"},
			tableFieldsMap: TableFieldsMap{
				"`db_b`.doris": {
					"dimensions.pipelineName": {FieldType: "text"},
					"path":                    {FieldType: "text"},
				},
				"`db_a`.doris": {
					"dimensions.pipelineName": {FieldType: "varchar(128)"},
					"path":                    {FieldType: "varchar(128)"},
				},
			},
			expectedErr: "doris multi-table union SELECT * cannot be combined with field dependency `dimensions`; use explicit fields",
		},
		{
			name:         "multi table SELECT star 允许已展开的嵌套 leaf alias 依赖",
			selectFields: []string{"*", "`dimensions.pipelineName`"},
			tableFieldsMap: TableFieldsMap{
				"`db_b`.doris": {
					"dimensions.pipelineName": {FieldType: "text"},
					"path":                    {FieldType: "text"},
				},
				"`db_a`.doris": {
					"dimensions.pipelineName": {FieldType: "varchar(128)"},
					"path":                    {FieldType: "varchar(128)"},
				},
			},
			expected: "CAST(dimensions['pipelineName'] AS TEXT) AS `dimensions.pipelineName`, `path`",
		},
		{
			name:         "multi table qualified wildcard 返回明确错误",
			selectFields: []string{"t.*"},
			tableFieldsMap: TableFieldsMap{
				"`db_b`.doris": {
					"path":   {FieldType: "text"},
					"status": {FieldType: "text"},
					"extra":  {FieldType: "bigint"},
				},
				"`db_a`.doris": {
					"path":   {FieldType: "varchar(128)"},
					"status": {FieldType: "bigint"},
					"other":  {FieldType: "text"},
				},
			},
			expectedErr: "doris multi-table union does not support SELECT *; use explicit fields",
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
			if tt.expectedErr != "" {
				require.Error(t, err)
				assert.EqualError(t, err, tt.expectedErr)
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
