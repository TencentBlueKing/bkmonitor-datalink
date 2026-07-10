package doris_parser

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

type unionSelectTestNode struct {
	baseNode
	value string
}

func (n *unionSelectTestNode) String() string {
	return n.value
}

func TestCollectColumnNamesFromSQLForUnion(t *testing.T) {
	tests := []struct {
		name        string
		sql         string
		ignoreNames map[string]struct{}
		expected    []string
	}{
		{
			name:     "select alias 与真实字段同名时保留真实字段",
			sql:      "`host` AS ip, `ip`",
			expected: []string{"`host`", "`ip`"},
		},
		{
			name:     "字符串字面量里的反引号不当作字段",
			sql:      "regexp_extract(`log`, '`user`=(\\\\d+)', 1) AS user_id",
			expected: []string{"`log`"},
		},
		{
			name:     "双引号字符串里的标识符不当作字段",
			sql:      `regexp_extract(log, "user=(\\d+)", 1) AS user_id`,
			expected: []string{"`log`"},
		},
		{
			name:     "数字科学计数法不当作字段",
			sql:      "1e3",
			expected: nil,
		},
		{
			name:        "GROUP/ORDER 引用外层聚合 alias 时不下推",
			sql:         "`_value_` DESC",
			ignoreNames: map[string]struct{}{"_value_": {}},
			expected:    nil,
		},
		{
			name:        "GROUP/ORDER 引用外层 alias 时大小写不敏感",
			sql:         "C DESC",
			ignoreNames: map[string]struct{}{"c": {}},
			expected:    nil,
		},
		{
			name:     "dotted 引用只收集 root 字段",
			sql:      "__ext.cluster.extra.name_space, `path`",
			expected: []string{"`__ext`", "`path`"},
		},
		{
			name:     "反引号 keyword 字段保留为真实字段",
			sql:      "`time`, `path`",
			expected: []string{"`time`", "`path`"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, collectColumnNamesFromSQL(tt.sql, tt.ignoreNames))
		})
	}
}

func TestCollectAliasesFromSQLForUnion(t *testing.T) {
	tests := []struct {
		name     string
		sql      string
		expected map[string]struct{}
	}{
		{
			name:     "跳过字符串里的 AS 文本",
			sql:      "COUNT(regexp_extract(log, ' AS path ', 1)) AS user_id",
			expected: map[string]struct{}{"user_id": {}},
		},
		{
			name:     "跳过括号内 CAST 类型 AS",
			sql:      "CAST(log AS TEXT) AS log_text",
			expected: map[string]struct{}{"log_text": {}},
		},
		{
			name:     "收集反引号 alias",
			sql:      "COUNT(*) AS `log_count`",
			expected: map[string]struct{}{"log_count": {}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, collectAliasesFromSQL(tt.sql))
		})
	}
}

func TestStatementUnionSelectListFallbacks(t *testing.T) {
	tests := []struct {
		name      string
		selectSQL string
		groupSQL  string
		orderSQL  string
		tables    []string
		expected  string
	}{
		{
			name:      "混合 wildcard 保留 SELECT star 语义",
			selectSQL: "*, `log`",
			expected:  Star,
		},
		{
			name:      "未加反引号的对象字段表达式保守回退",
			selectSQL: "CAST(__ext['pod'] AS TEXT) AS pod, COUNT(*) AS cnt",
			groupSQL:  "pod",
			expected:  "`__ext`",
		},
		{
			name:      "CAST 普通字段表达式保留源字段",
			selectSQL: "CAST(log AS TEXT) AS log_text, `path`",
			expected:  "`log`, `path`",
		},
		{
			name:      "COUNT star 不增加字段依赖",
			selectSQL: "`minute1`, COUNT(*) AS log_count",
			groupSQL:  "`minute1`",
			orderSQL:  "`minute1` DESC",
			expected:  "`minute1`",
		},
		{
			name:      "纯 COUNT star 多表 union 使用常量投影",
			selectSQL: "COUNT(*) AS log_count",
			tables:    []string{"`db_b`.doris", "`db_a`.doris"},
			expected:  unionDummyProjection,
		},
		{
			name:      "可识别字段按首次出现顺序去重",
			selectSQL: "`path`, COUNT(*) AS cnt",
			groupSQL:  "`path`",
			orderSQL:  "`path` DESC",
			expected:  "`path`",
		},
		{
			name:      "字符串里的 AS 不会误跳过 GROUP BY 字段",
			selectSQL: "regexp_extract(log, ' AS path ', 1) AS user_id",
			groupSQL:  "path",
			expected:  "`log`, `path`",
		},
		{
			name:      "算术乘法不是 wildcard",
			selectSQL: "a * b AS value",
			expected:  "`a`, `b`",
		},
		{
			name:      "纯数字科学计数法多表 union 使用常量投影",
			selectSQL: "1e3",
			tables:    []string{"`db_b`.doris", "`db_a`.doris"},
			expected:  unionDummyProjection,
		},
		{
			name:      "DISTINCT star 按 wildcard 处理",
			selectSQL: "DISTINCT(*)",
			tables:    []string{"`db_b`.doris", "`db_a`.doris"},
			expected:  Star,
		},
		{
			name:      "混合 DISTINCT star 按 wildcard 处理",
			selectSQL: "DISTINCT(*), `log`",
			tables:    []string{"`db_b`.doris", "`db_a`.doris"},
			expected:  Star,
		},
		{
			name:      "混合 DISTINCT 空格 star 按 wildcard 处理",
			selectSQL: "DISTINCT *, `log`",
			tables:    []string{"`db_b`.doris", "`db_a`.doris"},
			expected:  Star,
		},
		{
			name:      "ORDER BY 大小写不同的 alias 不下推",
			selectSQL: "COUNT(*) AS c",
			orderSQL:  "C DESC",
			tables:    []string{"`db_b`.doris", "`db_a`.doris"},
			expected:  unionDummyProjection,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stmt := &Statement{
				Tables: tt.tables,
				nodeMap: map[string]Node{
					SelectItem: &unionSelectTestNode{value: tt.selectSQL},
					GroupItem:  &unionSelectTestNode{value: tt.groupSQL},
					OrderItem:  &unionSelectTestNode{value: tt.orderSQL},
				},
			}
			assert.Equal(t, tt.expected, stmt.unionSelectList())
		})
	}
}

func TestStatementUnionSelectListRejectsMultiTableWildcard(t *testing.T) {
	stmt := &Statement{
		Tables:               []string{"`db_b`.doris", "`db_a`.doris"},
		RejectSelectAllUnion: true,
		nodeMap: map[string]Node{
			SelectItem: &unionSelectTestNode{value: "*"},
		},
	}

	assert.Equal(t, Star, stmt.unionSelectList())
	assert.ErrorContains(t, stmt.Error(), "SELECT *")
}

func TestStatementUnionSelectListValidatesTableSchema(t *testing.T) {
	stmt := &Statement{
		Tables: []string{"`db_his`.doris", "`db_current`.doris"},
		TableFieldsMap: TableFieldsMap{
			"`db_his`.doris": {
				"log": {FieldType: "text"},
			},
			"`db_current`.doris": {
				"path": {FieldType: "text"},
			},
		},
		nodeMap: map[string]Node{
			SelectItem: &unionSelectTestNode{value: "`path`, COUNT(*) AS c"},
			GroupItem:  &unionSelectTestNode{value: "`path`"},
		},
	}

	assert.Equal(t, "`path`", stmt.unionSelectList())
	assert.ErrorContains(t, stmt.Error(), "missing from table `db_his`.doris")
}

func TestStatementUnionSelectListValidatesRequestedObjectLeaf(t *testing.T) {
	stmt := &Statement{
		Tables: []string{"`db_his`.doris", "`db_current`.doris"},
		TableFieldsMap: TableFieldsMap{
			"`db_his`.doris": {
				"dimensions.pipelineName": {FieldType: "text"},
				"dimensions.retry_count":  {FieldType: "int"},
			},
			"`db_current`.doris": {
				"dimensions.pipelineName": {FieldType: "varchar"},
				"dimensions.retry_count":  {FieldType: "double"},
			},
		},
		nodeMap: map[string]Node{
			SelectItem: &unionSelectTestNode{value: "dimensions['pipelineName'], COUNT(*) AS c"},
			GroupItem:  &unionSelectTestNode{value: "dimensions['pipelineName']"},
		},
	}

	assert.Equal(t, "`dimensions`", stmt.unionSelectList())
	assert.NoError(t, stmt.Error())
}

func TestStatementSubQueryUnionInheritsTableSchema(t *testing.T) {
	subQuery := &Statement{
		nodeMap: map[string]Node{
			SelectItem: &unionSelectTestNode{value: "`path`"},
			TableItem:  &unionSelectTestNode{value: "`ignored`.doris"},
		},
	}
	stmt := &Statement{
		Tables: []string{"`db_his`.doris", "`db_current`.doris"},
		TableFieldsMap: TableFieldsMap{
			"`db_his`.doris": {
				"log": {FieldType: "text"},
			},
			"`db_current`.doris": {
				"path": {FieldType: "text"},
			},
		},
		nodeMap: map[string]Node{
			SelectItem: &unionSelectTestNode{value: "`path`"},
			TableItem: &TableNode{
				SubQuery: subQuery,
				Alias:    "s",
			},
		},
	}

	_ = stmt.String()
	assert.ErrorContains(t, stmt.Error(), "missing from table `db_his`.doris")
}
