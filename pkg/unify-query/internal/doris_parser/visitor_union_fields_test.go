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
			name:        "GROUP/ORDER 引用外层聚合 alias 时不下推",
			sql:         "`_value_` DESC",
			ignoreNames: map[string]struct{}{"_value_": {}},
			expected:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, collectColumnNamesFromSQL(tt.sql, tt.ignoreNames))
		})
	}
}

func TestStatementUnionSelectListFallbacks(t *testing.T) {
	tests := []struct {
		name      string
		selectSQL string
		groupSQL  string
		orderSQL  string
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
			name:      "可识别字段按首次出现顺序去重",
			selectSQL: "`path`, COUNT(*) AS cnt",
			groupSQL:  "`path`",
			orderSQL:  "`path` DESC",
			expected:  "`path`",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stmt := &Statement{
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
