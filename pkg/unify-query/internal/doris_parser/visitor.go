// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package doris_parser

import (
	"context"
	"fmt"
	"sort"
	"strings"

	antlr "github.com/antlr4-go/antlr/v4"
	"github.com/spf13/cast"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/doris_parser/gen"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

const (
	Star                 = "*"
	unionDummyProjection = "1"
)

const (
	SelectItem = "SELECT"
	TableItem  = "FROM"
	WhereItem  = "WHERE"
	OrderItem  = "ORDER BY"
	GroupItem  = "GROUP BY"
	LimitItem  = "LIMIT"
	OffsetItem = "OFFSET"

	AsItem = "AS"

	defaultLimit = "100"
)

const (
	whereCtxType = iota
	selectCtxType
	groupCtxType
	orderCtxType
)

type Encode func(string) (string, string)

type Node interface {
	antlr.ParseTreeVisitor
	String() string
	Error() error

	WithAddIgnoreField(func(string))
	WithEncode(Encode)
	WithAliasScope(map[string]struct{})
}

type baseNode struct {
	antlr.BaseParseTreeVisitor

	AddIgnoreField func(string)
	Encode         Encode
	aliasScope     map[string]struct{}
}

func (n *baseNode) String() string {
	return ""
}

func (n *baseNode) Error() error {
	return nil
}

func (n *baseNode) WithEncode(encode Encode) {
	n.Encode = encode
}

func (n *baseNode) WithAddIgnoreField(fn func(string)) {
	n.AddIgnoreField = fn
}

func (n *baseNode) WithAliasScope(aliases map[string]struct{}) {
	n.aliasScope = aliases
}

type Statement struct {
	baseNode

	isSubQuery bool

	// isFromSubQuery 为 true 时表示该 Statement 是 FROM 子句中的子查询（AliasedQuery）
	// 这种子查询不应注入默认 LIMIT
	isFromSubQuery bool

	nodeMap map[string]Node

	Tables         []string
	Where          string
	TableFieldsMap TableFieldsMap
	// RejectSelectAllUnion controls Doris-only schema drift protection for SELECT * unions.
	RejectSelectAllUnion bool
	Offset               int
	Limit                int
	errNode              []string
}

type TableFieldsMap map[string]metadata.FieldsMap

// UnionProjectionField carries the physical field used by inner UNION branches
// and the exact schema field that should be validated for compatibility.
type UnionProjectionField struct {
	Field        string
	ValidateName string
}

type unionProjectionField struct {
	field        string
	validateName string
}

type selectAllUnionField struct {
	projection unionProjectionField
	fieldType  string
}

// collectColumnNamesFromSQL 从已经渲染完成的 SQL 片段里提取物理列名。
//
// 多表 UNION 的内层投影必须保留外层表达式依赖的源字段，例如
// `CAST(log AS TEXT)` 里的 log、`CAST(__ext[...] AS TEXT)` 里的 __ext。
// 因此这里同时识别反引号字段和可解析的未反引号 identifier。
//
// 这里不是完整 SQL parser，只处理 visitor 已渲染出的 SELECT/GROUP/ORDER 片段：
// 跳过字符串字面量、未反引号 SQL keyword、函数名和 AS 后的 alias。dotted path
// 只收 root，避免把对象 key 或路径段误投影为原表列。ignoreNames 只用于 GROUP/ORDER
// 这类可能引用 SELECT alias 的片段；SELECT 自身不能用全局 alias 名过滤，否则
// `SELECT host AS ip, ip` 会把真实源字段 `ip` 错删。
func collectColumnNamesFromSQL(s string, ignoreNames map[string]struct{}) []string {
	fields := collectUnionProjectionFields(s, ignoreNames)
	var names []string
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if _, ok := seen[field.field]; ok {
			continue
		}
		seen[field.field] = struct{}{}
		names = append(names, field.field)
	}
	return names
}

func collectUnionProjectionFields(s string, ignoreNames map[string]struct{}) []unionProjectionField {
	var fields []unionProjectionField
	seen := make(map[string]struct{})
	for idx := 0; idx < len(s); idx++ {
		switch s[idx] {
		case '\'':
			idx = skipSingleQuotedSQLString(s, idx)
			continue
		case '"':
			idx = skipDoubleQuotedSQLString(s, idx)
			continue
		case '`':
			start := idx
			end := strings.IndexByte(s[idx+1:], '`')
			if end < 0 {
				return fields
			}
			end += idx + 1
			name := s[idx+1 : end]
			idx = end
			if shouldSkipColumnName(s, start, name, ignoreNames, true) {
				continue
			}
			field := fmt.Sprintf("`%s`", name)
			validateName := name + collectObjectPathSuffix(s, idx+1)
			key := field + "\x00" + validateName
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			fields = append(fields, unionProjectionField{field: field, validateName: validateName})
			continue
		}

		if !isSQLIdentifierStart(s[idx]) {
			continue
		}

		start := idx
		for idx < len(s) && isSQLIdentifierPart(s[idx]) {
			idx++
		}
		name := s[start:idx]
		idx--
		if isIdentifierPartOfNumericLiteral(s, start) {
			continue
		}
		if previousNonSpaceByte(s, start) == '.' {
			continue
		}
		if shouldSkipColumnName(s, start, name, ignoreNames, false) {
			continue
		}
		// 标识符后紧跟 '(' 时是函数名，例如 COUNT(*) 或 CAST(...)，
		// 函数参数会在后续扫描中单独识别，COUNT(*) 不会产生字段依赖。
		if nextNonSpaceByte(s, idx+1) == '(' {
			continue
		}
		validateName := name + collectObjectPathSuffix(s, idx+1)
		field := fmt.Sprintf("`%s`", name)
		key := field + "\x00" + validateName
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		fields = append(fields, unionProjectionField{field: field, validateName: validateName})
	}
	return fields
}

func shouldSkipColumnName(s string, start int, name string, ignoreNames map[string]struct{}, quoted bool) bool {
	if name == "" {
		return true
	}
	if ignoreNamesContains(ignoreNames, name) {
		return true
	}
	if !quoted && isSQLKeyword(name) {
		return true
	}
	if previousTokenIsAS(s, start) {
		return true
	}
	return false
}

func ignoreNamesContains(ignoreNames map[string]struct{}, name string) bool {
	if len(ignoreNames) == 0 {
		return false
	}
	if _, ok := ignoreNames[name]; ok {
		return true
	}
	_, ok := ignoreNames[strings.ToLower(name)]
	return ok
}

func collectObjectPathSuffix(s string, start int) string {
	var parts []string
	for idx := start; idx < len(s); {
		for idx < len(s) && s[idx] == ' ' {
			idx++
		}
		if idx >= len(s) {
			break
		}
		switch s[idx] {
		case '.':
			idx++
			for idx < len(s) && s[idx] == ' ' {
				idx++
			}
			partStart := idx
			if idx >= len(s) || !isSQLIdentifierStart(s[idx]) {
				return strings.Join(parts, "")
			}
			for idx < len(s) && isSQLIdentifierPart(s[idx]) {
				idx++
			}
			parts = append(parts, "."+s[partStart:idx])
		case '[':
			part, next, ok := scanBracketObjectPathPart(s, idx)
			if !ok {
				return strings.Join(parts, "")
			}
			if part != "" {
				parts = append(parts, "."+part)
			}
			idx = next
		default:
			return strings.Join(parts, "")
		}
	}
	return strings.Join(parts, "")
}

func scanBracketObjectPathPart(s string, start int) (string, int, bool) {
	idx := start + 1
	for idx < len(s) && s[idx] == ' ' {
		idx++
	}
	if idx >= len(s) {
		return "", idx, false
	}

	var part string
	switch s[idx] {
	case '\'', '"', '`':
		quote := s[idx]
		end := skipQuotedSQLString(s, idx, quote)
		if end <= idx || end >= len(s) {
			return "", end, false
		}
		part = s[idx+1 : end]
		idx = end + 1
	default:
		partStart := idx
		for idx < len(s) && isSQLIdentifierPart(s[idx]) {
			idx++
		}
		part = s[partStart:idx]
	}

	for idx < len(s) && s[idx] == ' ' {
		idx++
	}
	if idx >= len(s) || s[idx] != ']' {
		return "", idx, false
	}
	return part, idx + 1, true
}

func previousTokenIsAS(s string, start int) bool {
	idx := start - 1
	for idx >= 0 && s[idx] == ' ' {
		idx--
	}
	end := idx + 1
	for idx >= 0 && isSQLIdentifierPart(s[idx]) {
		idx--
	}
	return strings.EqualFold(s[idx+1:end], "AS")
}

func previousNonSpaceByte(s string, start int) byte {
	for idx := start - 1; idx >= 0; idx-- {
		if s[idx] != ' ' {
			return s[idx]
		}
	}
	return 0
}

func nextNonSpaceByte(s string, start int) byte {
	for idx := start; idx < len(s); idx++ {
		if s[idx] != ' ' {
			return s[idx]
		}
	}
	return 0
}

func isSQLIdentifierStart(b byte) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

func isSQLIdentifierPart(b byte) bool {
	return isSQLIdentifierStart(b) || b >= '0' && b <= '9'
}

func isSQLDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

func isIdentifierPartOfNumericLiteral(s string, start int) bool {
	return start > 0 && isSQLDigit(s[start-1])
}

func isSQLKeyword(name string) bool {
	switch strings.ToUpper(name) {
	case "AND", "ARRAY", "AS", "ASC", "BETWEEN", "BIGINT", "BOOL", "BOOLEAN", "BY", "CASE", "CAST",
		"DATE", "DATETIME", "DECIMAL", "DESC", "DISTINCT", "DOUBLE", "ELSE", "END", "FALSE", "FLOAT",
		"FROM", "GROUP", "IN", "INT", "INTEGER", "IS", "LIKE", "LIMIT", "MATCH_ALL", "MATCH_ANY",
		"MATCH_PHRASE", "MATCH_PHRASE_EDGE", "MATCH_PHRASE_PREFIX", "MATCH_REGEXP",
		"NOT", "NULL", "OR", "ORDER", "REGEXP", "SELECT", "STRING", "TEXT", "THEN", "TIME", "TIMESTAMP",
		"TRUE", "VARCHAR", "WHEN", "WHERE":
		return true
	default:
		return false
	}
}

func skipSingleQuotedSQLString(s string, start int) int {
	return skipQuotedSQLString(s, start, '\'')
}

func skipDoubleQuotedSQLString(s string, start int) int {
	return skipQuotedSQLString(s, start, '"')
}

func skipQuotedSQLString(s string, start int, quote byte) int {
	for idx := start + 1; idx < len(s); idx++ {
		switch s[idx] {
		case '\\':
			idx++
		case quote:
			if idx+1 < len(s) && s[idx+1] == quote {
				idx++
				continue
			}
			return idx
		}
	}
	return len(s) - 1
}

func hasTopLevelWildcard(s string) bool {
	if isDistinctStarExpression(s) {
		return true
	}

	return scanTopLevelWildcard(s, isWildcardToken)
}

func hasTopLevelQualifiedWildcard(s string) bool {
	return scanTopLevelWildcard(s, isQualifiedWildcardToken)
}

func scanTopLevelWildcard(s string, match func(string, int) bool) bool {
	depth := 0
	for idx := 0; idx < len(s); idx++ {
		switch s[idx] {
		case '\'':
			idx = skipSingleQuotedSQLString(s, idx)
		case '"':
			idx = skipDoubleQuotedSQLString(s, idx)
		case '`':
			end := strings.IndexByte(s[idx+1:], '`')
			if end < 0 {
				return false
			}
			idx += end + 1
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 && isDistinctStarAt(s, idx) {
				return true
			}
		case '*':
			if depth == 0 && match(s, idx) {
				return true
			}
		}
	}
	return false
}

func isDistinctStarAt(s string, idx int) bool {
	if idx > 0 && isSQLIdentifierPart(s[idx-1]) {
		return false
	}
	const distinct = "DISTINCT"
	if idx+len(distinct) > len(s) || !strings.EqualFold(s[idx:idx+len(distinct)], distinct) {
		return false
	}
	idx += len(distinct)
	if idx < len(s) && isSQLIdentifierPart(s[idx]) {
		return false
	}
	for idx < len(s) && s[idx] == ' ' {
		idx++
	}
	if idx < len(s) && s[idx] == '*' {
		next := nextNonSpaceByte(s, idx+1)
		return next == 0 || next == ','
	}
	if idx >= len(s) || s[idx] != '(' {
		return false
	}
	idx++
	for idx < len(s) && s[idx] == ' ' {
		idx++
	}
	if idx >= len(s) || s[idx] != '*' {
		return false
	}
	idx++
	for idx < len(s) && s[idx] == ' ' {
		idx++
	}
	return idx < len(s) && s[idx] == ')'
}

func isDistinctStarExpression(s string) bool {
	normalized := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r':
			return -1
		default:
			return r
		}
	}, s)
	return strings.EqualFold(normalized, "DISTINCT(*)") || strings.EqualFold(normalized, "DISTINCT*")
}

func isWildcardToken(s string, idx int) bool {
	prev := previousNonSpaceByte(s, idx)
	next := nextNonSpaceByte(s, idx+1)
	return (prev == 0 || prev == ',') && (next == 0 || next == ',')
}

func isQualifiedWildcardToken(s string, idx int) bool {
	prev := previousNonSpaceByte(s, idx)
	next := nextNonSpaceByte(s, idx+1)
	return prev == '.' && (next == 0 || next == ',')
}

func (v *Statement) unionSelectList() string {
	selectSQL := v.ItemString(SelectItem)

	if hasTopLevelQualifiedWildcard(selectSQL) {
		// 多表 UNION 会把 FROM 改写成 combined_data 子查询，原始表别名不再存在。
		// 因此只有 plain * 可以按公共字段展开，t.* 这类 qualified wildcard 继续要求显式字段。
		if len(v.Tables) > 1 && v.RejectSelectAllUnion {
			v.errNode = append(v.errNode, "doris multi-table union does not support SELECT *; use explicit fields")
		}
		return Star
	}

	if selectSQL == Star || hasTopLevelWildcard(selectSQL) {
		if len(v.Tables) > 1 && v.RejectSelectAllUnion {
			fields, err := ExpandSelectAllUnionFields(v.Tables, v.TableFieldsMap)
			if err != nil {
				v.errNode = append(v.errNode, err.Error())
				return Star
			}
			if len(fields) > 0 {
				aliases := v.collectSelectAliases()
				for alias := range collectAliasesFromSQL(selectSQL) {
					addAlias(aliases, alias)
				}
				projectionFields := collectUnionProjectionFields(selectSQL, nil)
				projectionFields = append(projectionFields, collectUnionProjectionFields(v.ItemString(GroupItem), aliases)...)
				projectionFields = append(projectionFields, collectUnionProjectionFields(v.ItemString(OrderItem), aliases)...)
				if err := validateUnionProjectionFields(v.Tables, projectionFields, v.TableFieldsMap); err != nil {
					v.errNode = append(v.errNode, err.Error())
					return Star
				}
				fields = appendMissingUnionFieldNames(fields, projectionFields)
				return strings.Join(fields, ", ")
			}
			v.errNode = append(v.errNode, "doris multi-table union does not support SELECT *; use explicit fields")
		}
		return Star
	}

	// 多张 Doris 表合并时不能无条件 SELECT *：
	// 历史表和当前表可能存在字段漂移，Doris 要求 UNION ALL 两侧列数一致。
	// 因此外层 SQL 只依赖部分字段时，内层子查询只投影这些字段；顶层 SELECT *
	// 在有表结构时会先转换成公共字段投影。COUNT(*) 不是顶层 wildcard，不会被展开。
	// 若没有任何真实字段依赖，内层 UNION 使用常量投影即可保留行数语义。
	aliases := v.collectSelectAliases()
	for alias := range collectAliasesFromSQL(selectSQL) {
		addAlias(aliases, alias)
	}
	fields := collectUnionProjectionFields(selectSQL, nil)
	fields = append(fields, collectUnionProjectionFields(v.ItemString(GroupItem), aliases)...)
	fields = append(fields, collectUnionProjectionFields(v.ItemString(OrderItem), aliases)...)
	if len(fields) == 0 {
		if len(v.Tables) > 1 {
			return unionDummyProjection
		}
		return Star
	}

	seen := make(map[string]struct{}, len(fields))
	result := make([]string, 0, len(fields))
	for _, field := range fields {
		if _, ok := seen[field.field]; ok {
			continue
		}
		seen[field.field] = struct{}{}
		result = append(result, field.field)
	}
	if err := validateUnionProjectionFields(v.Tables, fields, v.TableFieldsMap); err != nil {
		v.errNode = append(v.errNode, err.Error())
	}
	return strings.Join(result, ", ")
}

func ValidateUnionProjectionFields(tables []string, fields []string, tableFieldsMap TableFieldsMap) error {
	projectionFields := make([]unionProjectionField, 0, len(fields))
	for _, field := range fields {
		projectionFields = append(projectionFields, unionProjectionField{
			field:        field,
			validateName: unquoteUnionField(field),
		})
	}
	return validateUnionProjectionFields(tables, projectionFields, tableFieldsMap)
}

// ValidateUnionProjectionFieldNames validates UNION projections when the SQL
// projection root differs from the schema leaf that the outer query reads.
func ValidateUnionProjectionFieldNames(tables []string, fields []UnionProjectionField, tableFieldsMap TableFieldsMap) error {
	projectionFields := make([]unionProjectionField, 0, len(fields))
	for _, field := range fields {
		projectionFields = append(projectionFields, unionProjectionField{
			field:        field.Field,
			validateName: field.ValidateName,
		})
	}
	return validateUnionProjectionFields(tables, projectionFields, tableFieldsMap)
}

// ExpandSelectAllUnionFields converts SELECT * for Doris multi-table UNION into
// a deterministic explicit projection over fields shared by every table.
func ExpandSelectAllUnionFields(tables []string, tableFieldsMap TableFieldsMap) ([]string, error) {
	fields, err := collectSelectAllUnionProjectionFields(tables, tableFieldsMap)
	if err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return nil, nil
	}
	if err := validateUnionProjectionFields(tables, fields, tableFieldsMap); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(fields))
	for _, field := range fields {
		names = append(names, field.field)
	}
	return names, nil
}

func collectSelectAllUnionProjectionFields(tables []string, tableFieldsMap TableFieldsMap) ([]unionProjectionField, error) {
	if len(tables) == 0 || len(tableFieldsMap) == 0 {
		return nil, nil
	}

	common := make(map[string]selectAllUnionField)
	for idx, table := range tables {
		fieldsMap, ok := tableFieldsMap[table]
		if !ok {
			return nil, fmt.Errorf("doris multi-table union missing schema for table %s", table)
		}
		if idx == 0 {
			for name, option := range fieldsMap {
				name = strings.TrimSpace(name)
				if name == "" || !option.Existed() || isUnsupportedUnionFieldType(option.FieldType) {
					continue
				}
				key := strings.ToLower(name)
				if _, ok := common[key]; ok {
					continue
				}
				common[key] = selectAllUnionField{
					projection: unionProjectionField{
						field:        selectAllUnionProjectionField(name, option.FieldType),
						validateName: name,
					},
					fieldType: option.FieldType,
				}
			}
			continue
		}

		for key, field := range common {
			option := fieldsMap.Field(field.projection.validateName)
			if !option.Existed() ||
				isUnsupportedUnionFieldType(option.FieldType) ||
				!compatibleUnionFieldTypes(field.fieldType, option.FieldType) {
				delete(common, key)
			}
		}
	}

	if len(common) == 0 {
		return nil, fmt.Errorf("doris multi-table union SELECT * has no common fields")
	}

	keys := make([]string, 0, len(common))
	for key := range common {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	fields := make([]unionProjectionField, 0, len(keys))
	for _, key := range keys {
		fields = append(fields, common[key].projection)
	}
	return fields, nil
}

func selectAllUnionProjectionField(field string, fieldType string) string {
	field = unquoteUnionField(strings.TrimSpace(field))
	if !strings.Contains(field, ".") {
		return quoteUnionField(field)
	}
	return fmt.Sprintf("CAST(%s AS %s) AS `%s`", dorisObjectFieldExpression(field), dorisCastType(fieldType), field)
}

func dorisObjectFieldExpression(field string) string {
	parts := strings.Split(field, ".")
	if len(parts) == 0 {
		return field
	}

	mapFieldSet := map[string]struct{}{
		"resource":   {},
		"attributes": {},
	}

	var builder strings.Builder
	sep := ""
	for idx, part := range parts {
		switch idx {
		case 0:
			sep = "['"
		case len(parts) - 1:
			sep = "']"
		}

		builder.WriteString(part)
		builder.WriteString(sep)
		if _, ok := mapFieldSet[part]; ok {
			sep = "."
		} else if sep != "." {
			sep = "']['"
		}
	}
	return builder.String()
}

func dorisCastType(fieldType string) string {
	fieldType = strings.ToUpper(strings.TrimSpace(fieldType))
	if strings.HasPrefix(fieldType, "ARRAY<") && strings.HasSuffix(fieldType, ">") {
		return strings.TrimSuffix(strings.TrimPrefix(fieldType, "ARRAY<"), ">") + " ARRAY"
	}
	if fieldType == "" {
		return "STRING"
	}
	return fieldType
}

func appendMissingUnionFieldNames(fields []string, extraFields []unionProjectionField) []string {
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		seen[field] = struct{}{}
	}
	for _, field := range extraFields {
		if _, ok := seen[field.field]; ok {
			continue
		}
		seen[field.field] = struct{}{}
		fields = append(fields, field.field)
	}
	return fields
}

func validateUnionProjectionFields(tables []string, fields []unionProjectionField, tableFieldsMap TableFieldsMap) error {
	if len(tableFieldsMap) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		name := field.validateName
		if name == "" {
			name = unquoteUnionField(field.field)
		}
		key := field.field + "\x00" + name
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		var base metadata.FieldOption
		var baseTable string
		for _, table := range tables {
			fieldsMap, ok := tableFieldsMap[table]
			if !ok {
				return fmt.Errorf("doris multi-table union missing schema for table %s", table)
			}
			fieldOption, existed := unionFieldOption(fieldsMap, field.field, name)
			if !existed {
				return fmt.Errorf("doris multi-table union field %s is missing from table %s", field.field, table)
			}
			if isUnsupportedUnionFieldType(fieldOption.FieldType) {
				return fmt.Errorf("doris multi-table union field %s in table %s has unsupported type %s", field.field, table, fieldOption.FieldType)
			}
			if base.Existed() && !compatibleUnionFieldTypes(base.FieldType, fieldOption.FieldType) {
				return fmt.Errorf(
					"doris multi-table union field %s type mismatch: table %s has %s, table %s has %s",
					field.field, baseTable, base.FieldType, table, fieldOption.FieldType,
				)
			}
			if !base.Existed() {
				base = fieldOption
				baseTable = table
			}
		}
	}
	return nil
}

func unionFieldOption(fieldsMap metadata.FieldsMap, field string, validateName string) (metadata.FieldOption, bool) {
	fieldOption := fieldsMap.Field(validateName)
	if fieldOption.Existed() {
		return fieldOption, true
	}

	rootName := unquoteUnionField(field)
	if validateName != rootName {
		fieldOption = fieldsMap.Field(rootName)
		if fieldOption.Existed() {
			return fieldOption, true
		}
		return metadata.FieldOption{}, false
	}

	prefix := rootName + "."
	for fieldName, option := range fieldsMap {
		if strings.HasPrefix(fieldName, prefix) && option.Existed() {
			return option, true
		}
	}
	return metadata.FieldOption{}, false
}

func unquoteUnionField(field string) string {
	return strings.TrimSuffix(strings.TrimPrefix(field, "`"), "`")
}

func quoteUnionField(field string) string {
	field = unquoteUnionField(strings.TrimSpace(field))
	return fmt.Sprintf("`%s`", field)
}

func isUnsupportedUnionFieldType(fieldType string) bool {
	switch normalizeUnionFieldType(fieldType) {
	case "json", "jsonb":
		return true
	default:
		return false
	}
}

func compatibleUnionFieldTypes(left, right string) bool {
	return normalizeUnionFieldType(left) == normalizeUnionFieldType(right)
}

func normalizeUnionFieldType(fieldType string) string {
	t := strings.ToLower(strings.TrimSpace(fieldType))
	if strings.HasPrefix(t, "array<") && strings.HasSuffix(t, ">") {
		return "array:" + normalizeUnionFieldType(t[len("array<"):len(t)-1])
	}
	if strings.HasSuffix(t, " array") {
		return "array:" + normalizeUnionFieldType(strings.TrimSuffix(t, " array"))
	}
	if idx := strings.IndexByte(t, '('); idx >= 0 {
		t = t[:idx]
	}
	t = strings.TrimSpace(t)
	switch t {
	case "char", "varchar", "string", "text":
		return "string"
	case "tinyint", "smallint", "int", "integer", "bigint", "largeint":
		return "integer"
	case "float", "double", "decimal", "decimalv3":
		return "number"
	case "bool", "boolean":
		return "boolean"
	case "date", "datetime", "timestamp":
		return "time"
	default:
		return t
	}
}

func (v *Statement) ItemString(name string) string {
	if n, ok := v.nodeMap[name]; ok {
		return nodeToString(n)
	}

	return ""
}

func (v *Statement) collectSelectAliases() map[string]struct{} {
	aliases := make(map[string]struct{})
	if v.nodeMap == nil {
		return aliases
	}
	selectNode, ok := v.nodeMap[SelectItem]
	if !ok {
		return aliases
	}
	sn, ok := selectNode.(*SelectNode)
	if !ok {
		return aliases
	}
	for _, fn := range sn.fieldsNode {
		fieldNode, ok := fn.(*FieldNode)
		if !ok {
			continue
		}
		if fieldNode.as != nil {
			name := strings.Trim(nodeToString(fieldNode.as), "`")
			if name != "" {
				addAlias(aliases, name)
			}
		}
	}
	return aliases
}

func addAlias(aliases map[string]struct{}, name string) {
	if name == "" {
		return
	}
	aliases[name] = struct{}{}
	aliases[strings.ToLower(name)] = struct{}{}
}

// collectAliasesFromSQL 是测试和非标准节点的兜底：真实 parser 节点优先通过
// collectSelectAliases() 提供 alias，但部分单测直接塞渲染后的 SQL 字符串。
// 这里补齐 AS alias，避免 GROUP/ORDER 引用外层 alias 时被误下推到原表。
func collectAliasesFromSQL(s string) map[string]struct{} {
	aliases := make(map[string]struct{})
	depth := 0
	for idx := 0; idx < len(s); idx++ {
		switch s[idx] {
		case '\'':
			idx = skipSingleQuotedSQLString(s, idx)
			continue
		case '"':
			idx = skipDoubleQuotedSQLString(s, idx)
			continue
		case '`':
			end := strings.IndexByte(s[idx+1:], '`')
			if end < 0 {
				return aliases
			}
			idx += end + 1
			continue
		case '(':
			depth++
			continue
		case ')':
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth > 0 || !isASClauseAt(s, idx) {
			continue
		}

		idx += len(" AS ")
		for idx < len(s) && s[idx] == ' ' {
			idx++
		}
		if idx >= len(s) {
			break
		}
		if s[idx] == '`' {
			end := strings.IndexByte(s[idx+1:], '`')
			if end < 0 {
				break
			}
			name := s[idx+1 : idx+1+end]
			if name != "" {
				addAlias(aliases, name)
			}
			idx += end + 1
			continue
		}
		start := idx
		for idx < len(s) && isSQLIdentifierPart(s[idx]) {
			idx++
		}
		if idx > start {
			addAlias(aliases, s[start:idx])
		}
	}
	return aliases
}

func isASClauseAt(s string, idx int) bool {
	return idx+len(" AS ") <= len(s) && strings.EqualFold(s[idx:idx+len(" AS ")], " AS ")
}

func (v *Statement) String() string {
	var result []string

	for _, name := range []string{SelectItem, TableItem, WhereItem, GroupItem, OrderItem, LimitItem} {
		res := v.ItemString(name)
		key := name

		switch name {
		case TableItem:
			// 当 FROM 是子查询时，把 Tables/Where 注入子查询内部，保留外层 FROM 结构
			if tableNode, ok := v.nodeMap[TableItem].(*TableNode); ok && tableNode.SubQuery != nil {
				tableNode.SubQuery.Tables = v.Tables
				tableNode.SubQuery.Where = v.Where
				tableNode.SubQuery.TableFieldsMap = v.TableFieldsMap
				tableNode.SubQuery.RejectSelectAllUnion = v.RejectSelectAllUnion
				v.Where = ""
				res = tableNode.String()
				if err := tableNode.SubQuery.Error(); err != nil {
					v.errNode = append(v.errNode, err.Error())
				}
			} else if len(v.Tables) > 0 {
				if len(v.Tables) == 1 {
					res = v.Tables[0]
				} else {
					stmts := make([]string, 0, len(v.Tables))
					selectList := v.unionSelectList()
					for _, t := range v.Tables {
						// 多表合并时将时间/查询条件下推到每张物理表，同时用显式投影规避表结构不一致。
						s := fmt.Sprintf("SELECT %s FROM %s", selectList, t)
						if v.Where != "" {
							s = fmt.Sprintf("%s WHERE %s", s, v.Where)
						}
						stmts = append(stmts, s)
					}
					res = fmt.Sprintf("(%s) AS combined_data", strings.Join(stmts, " UNION ALL "))
					v.Where = ""
				}
			}
		case WhereItem:
			// 清空 where 条件
			if len(v.Tables) > 1 {
				res = ""
			}

			if v.Where != "" {
				if res == "" {
					res = v.Where
				} else {
					res = fmt.Sprintf("%s AND %s", res, v.Where)
				}
			}
		case LimitItem:
			key = ""
		}

		if res != "" {
			if key != "" {
				res = fmt.Sprintf("%s %s", key, res)
			}
			result = append(result, res)
		}
	}

	sql := strings.Join(result, " ")
	if v.isSubQuery {
		sql = fmt.Sprintf("(%s)", sql)
	}

	return sql
}

func (v *Statement) Error() error {
	if len(v.errNode) > 0 {
		return fmt.Errorf("%s", strings.Join(v.errNode, " "))
	}
	return nil
}

func (v *Statement) VisitErrorNode(ctx antlr.ErrorNode) any {
	v.errNode = append(v.errNode, ctx.GetText())
	return nil
}

func (v *Statement) VisitChildren(ctx antlr.RuleNode) any {
	var next Node
	next = v

	if v.nodeMap == nil {
		v.nodeMap = map[string]Node{
			LimitItem: &LimitNode{
				ParentLimit:    v.Limit,
				ParentOffset:   v.Offset,
				noDefaultLimit: v.isFromSubQuery,
			},
		}
	}

	switch ctx.(type) {
	case *gen.SelectClauseContext:
		v.nodeMap[SelectItem] = &SelectNode{}
		next = v.nodeMap[SelectItem]
	case *gen.FromClauseContext:
		tableNode := &TableNode{
			onAliasesReady: func(aliases map[string]struct{}) {
				v.aliasScope = aliases
				// SELECT 절은 FROM 보다 먼저 파싱되므로 aliasScope 가 없는 상태로 생성됨.
				// FROM 서브쿼리 파싱 완료 후 SELECT 노드에도 aliasScope 를 전파.
				if sn, ok := v.nodeMap[SelectItem]; ok {
					sn.WithAliasScope(aliases)
				}
			},
		}
		v.nodeMap[TableItem] = tableNode
		next = tableNode
	case *gen.WhereClauseContext:
		v.nodeMap[WhereItem] = &WhereNode{
			LogicInc: &LogicNodesInc{},
		}
		next = v.nodeMap[WhereItem]
	case *gen.AggClauseContext:
		v.nodeMap[GroupItem] = &AggNode{}
		next = v.nodeMap[GroupItem]
	case *gen.SortClauseContext:
		v.nodeMap[OrderItem] = &SortNode{}
		next = v.nodeMap[OrderItem]
	case *gen.LimitClauseContext:
		next = v.nodeMap[LimitItem]
	}

	return visitChildren(v.AddIgnoreField, v.Encode, v.aliasScope, next, ctx)
}

type LimitNode struct {
	baseNode

	prefix string

	limit  int
	offset int

	ParentLimit  int
	ParentOffset int

	// noDefaultLimit 为 true 时，当 SQL 未指定 LIMIT 时不注入默认值（用于 FROM 子查询）
	noDefaultLimit bool
}

func (v *LimitNode) getOffsetAndLimit() (string, string) {
	offset := v.offset + v.ParentOffset

	// 计算原始数据的上限位置（offset + limit）
	// 如果 limit > 0，上限 = offset + limit
	// 如果 limit <= 0，表示无限制
	upperBound := 0
	if v.limit > 0 {
		upperBound = v.offset + v.limit
	}

	// 如果外层的 OFFSET 已经超出了内层的 LIMIT，则需要设置 LIMIT 为 0，代表没有数据
	if v.limit > 0 && v.ParentOffset >= v.limit {
		return "", "0"
	}

	limit := v.limit
	if v.ParentLimit > 0 {
		if v.limit <= 0 || v.limit > v.ParentLimit {
			limit = v.ParentLimit
		}
	}

	var resultOffset, resultLimit string
	if offset > 0 {
		resultOffset = cast.ToString(offset)
	}

	if limit > 0 {
		resultLimit = cast.ToString(limit)
	} else if !v.noDefaultLimit {
		resultLimit = defaultLimit
	}

	// 只有指定了 ParentOffset 且有上限时才需要进行切割
	// 计算剩余可取数量：上限 - 当前offset
	if v.ParentOffset > 0 && upperBound > 0 {
		remaining := upperBound - offset
		if remaining < limit {
			resultLimit = cast.ToString(remaining)
		}
	}

	return resultOffset, resultLimit
}

func (v *LimitNode) String() string {
	var s []string

	offset, limit := v.getOffsetAndLimit()
	if limit != "" {
		s = append(s, fmt.Sprintf("%s %s", LimitItem, limit))
	}
	if offset != "" {
		s = append(s, fmt.Sprintf("%s %s", OffsetItem, offset))
	}

	return strings.Join(s, " ")
}

func (v *LimitNode) VisitTerminal(ctx antlr.TerminalNode) any {
	result := strings.ToUpper(ctx.GetText())
	switch result {
	case LimitItem, OffsetItem:
		v.prefix = result
	case ",":
		v.offset = v.limit
	default:
		if v.prefix == LimitItem {
			v.limit = cast.ToInt(result)
		} else if v.prefix == OffsetItem {
			v.offset = cast.ToInt(result)
		}
	}

	return nil
}

type SortNode struct {
	nodes []Node

	baseNode
}

func (v *SortNode) String() string {
	var ns []string
	for _, fn := range v.nodes {
		ss := nodeToString(fn)
		if ss != "" {
			ns = append(ns, ss)
		}
	}

	return strings.Join(ns, ", ")
}

func (v *SortNode) VisitChildren(ctx antlr.RuleNode) any {
	var next Node
	next = v

	switch ctx.(type) {
	case *gen.SortItemContext:
		fn := &OrderNode{}
		next = fn
		v.nodes = append(v.nodes, fn)
	}
	return visitChildren(v.AddIgnoreField, v.Encode, v.aliasScope, next, ctx)
}

type OrderNode struct {
	node Node
	sort Node

	baseNode
}

func (v *OrderNode) String() string {
	var ns []string
	result := nodeToString(v.node)
	if result != "" {
		ns = append(ns, result)
	}
	if result == "" {
		return ""
	}

	sort := nodeToString(v.sort)
	if sort != "" {
		ns = append(ns, sort)
	}

	return strings.Join(ns, " ")
}

func (v *OrderNode) VisitTerminal(ctx antlr.TerminalNode) any {
	result := strings.ToUpper(ctx.GetText())
	v.sort = &StringNode{
		Name: result,
	}
	return nil
}

func (v *OrderNode) VisitChildren(ctx antlr.RuleNode) any {
	var next Node
	next = v

	switch ctx.(type) {
	case *gen.ExpressionContext:
		v.node = &FieldNode{
			exprType: orderCtxType,
		}
		next = v.node
	}
	return visitChildren(v.AddIgnoreField, v.Encode, v.aliasScope, next, ctx)
}

type AggNode struct {
	fieldsNode []Node

	baseNode
}

func (v *AggNode) String() string {
	var ns []string
	for _, fn := range v.fieldsNode {
		ss := nodeToString(fn)
		if ss != "" {
			ns = append(ns, ss)
		}
	}

	return strings.Join(ns, ", ")
}

func (v *AggNode) VisitChildren(ctx antlr.RuleNode) any {
	var next Node
	next = v

	switch ctx.(type) {
	case *gen.ExpressionContext:
		fn := &FieldNode{
			exprType: groupCtxType,
		}
		next = fn
		v.fieldsNode = append(v.fieldsNode, fn)
	}
	return visitChildren(v.AddIgnoreField, v.Encode, v.aliasScope, next, ctx)
}

type WhereNode struct {
	baseNode

	nodes []Node

	LogicInc *LogicNodesInc

	err error
}

func (v *WhereNode) add(node Node) {
	v.nodes = append(v.nodes, node)
}

func (v *WhereNode) Error() error {
	return v.err
}

func (v *WhereNode) String() string {
	var list []string
	for _, n := range v.nodes {
		switch n.(type) {
		case *LogicNode:
			v.LogicInc.Append(nodeToString(n))
		default:
			v.LogicInc.Inc(n)
			item := nodeToString(n)
			if item != "" {
				list = append(list, item)
			}

			logicName := v.LogicInc.Name()
			if logicName != "" {
				list = append(list, logicName)
			}
		}
	}

	return strings.Join(list, " ")
}

func (v *WhereNode) VisitChildren(ctx antlr.RuleNode) any {
	var next Node
	next = v

	switch n := ctx.(type) {
	case *gen.LogicalBinaryContext:
		v.add(&LogicNode{
			Op: &StringNode{
				Name: strings.ToUpper(n.GetOperator().GetText()),
			},
		})
	case *gen.ParenthesizedExpressionContext:
		v.add(&LeftParenNode{})
		defer func() {
			v.add(&RightParenNode{})
		}()
	case *gen.PredicatedContext:
		// 忽略带有括号的
		s := ctx.GetText()
		if s[0] == '(' && s[len(s)-1] == ')' {
			break
		}

		on := &OperatorNode{}
		v.add(on)
		next = on
	}
	return visitChildren(v.AddIgnoreField, v.Encode, v.aliasScope, next, ctx)
}

type LeftParenNode struct {
	baseNode
}

func (v *LeftParenNode) String() string {
	return fmt.Sprintf("(")
}

type RightParenNode struct {
	baseNode
}

func (v *RightParenNode) String() string {
	return fmt.Sprintf(")")
}

type ParentNode struct {
	baseNode

	node Node
}

func (v *ParentNode) String() string {
	return fmt.Sprintf("(%s)", nodeToString(v.node))
}

func (v *ParentNode) VisitChildren(ctx antlr.RuleNode) any {
	v.node = &ConditionNode{}
	next := v.node
	return visitChildren(v.AddIgnoreField, v.Encode, v.aliasScope, next, ctx)
}

type LogicNode struct {
	baseNode

	Op Node
}

func (v *LogicNode) String() string {
	return nodeToString(v.Op)
}

type LogicNodesInc struct {
	list []*LogicNodeInc
}

type LogicNodeInc struct {
	name string
	inc  int
}

func (l *LogicNodesInc) Append(name string) {
	if l.list == nil {
		l.list = make([]*LogicNodeInc, 0)
	}
	l.list = append(l.list, &LogicNodeInc{
		name: name,
	})
}

func (l *LogicNodesInc) Name() (name string) {
	if len(l.list) == 0 {
		return name
	}

	last := l.list[len(l.list)-1]
	if last.inc == 0 {
		name = last.name
		l.list = l.list[:len(l.list)-1]
	}

	return name
}

func (l *LogicNodesInc) Inc(e Node) {
	if e == nil || len(l.list) == 0 {
		return
	}

	switch e.(type) {
	case *LeftParenNode:
		l.list[len(l.list)-1].inc++
	case *RightParenNode:
		l.list[len(l.list)-1].inc--
	}
}

type ConditionNode struct {
	baseNode

	node Node
}

func (v *ConditionNode) String() string {
	return nodeToString(v.node)
}

func (v *ConditionNode) VisitChildren(ctx antlr.RuleNode) any {
	var next Node
	next = v

	switch ctx.(type) {
	case *gen.PredicatedContext:
		v.node = &OperatorNode{}
		next = v.node
	case *gen.LogicalBinaryContext:
		v.node = &LogicNode{
			Op: &StringNode{
				Name: ctx.GetText(),
			},
		}
		next = v.node
	}

	return visitChildren(v.AddIgnoreField, v.Encode, v.aliasScope, next, ctx)
}

type OperatorNode struct {
	baseNode

	Left  Node
	Right Node
	Op    Node
}

func (v *OperatorNode) String() string {
	left := nodeToString(v.Left)
	op := nodeToString(v.Op)
	right := nodeToString(v.Right)

	if strings.ToUpper(op) == "IN" {
		right = "(" + right + ")"
	}
	result := fmt.Sprintf("%s %s %s", left, op, right)
	return result
}

func (v *OperatorNode) VisitTerminal(node antlr.TerminalNode) any {
	banTokens := []string{"(", ")", ","}
	token := node.GetText()

	for _, bt := range banTokens {
		if token == bt {
			return nil
		}
	}

	if v.Op == nil {
		v.Op = &StringsNode{}
	}

	if op, ok := v.Op.(*StringsNode); ok {
		op.add(node.GetText())
	}

	return nil
}

func (v *OperatorNode) VisitChildren(ctx antlr.RuleNode) any {
	var next Node
	next = v

	switch ctx.(type) {
	case *gen.ValueExpressionDefaultContext:
		if v.Left == nil {
			v.Left = &FieldNode{
				exprType: whereCtxType,
			}
			next = v.Left
		} else if v.Right == nil {
			v.Right = &ValueNode{}
			next = v.Right
		} else {
			next = v.Right
		}
	}
	return visitChildren(v.AddIgnoreField, v.Encode, v.aliasScope, next, ctx)
}

type TableNode struct {
	baseNode
	Table    Node
	SubQuery *Statement // FROM (subquery) alias 中的子查询
	Alias    string     // 子查询别名
	// onAliasesReady 在子查询解析完毕后由父 Statement 注入，
	// 用于将子查询的 SELECT 别名集合回传，无需事后遍历整棵树。
	onAliasesReady func(map[string]struct{})
}

func (v *TableNode) String() string {
	if v.SubQuery != nil {
		if v.Alias != "" {
			return fmt.Sprintf("%s %s", v.SubQuery.String(), v.Alias)
		}
		return v.SubQuery.String()
	}
	if v.Table == nil {
		return ""
	}
	return v.Table.String()
}

func (v *TableNode) VisitChildren(ctx antlr.RuleNode) any {
	var next Node
	next = v

	switch c := ctx.(type) {
	case *gen.TableNameContext:
		v.Table = &StringNode{Name: ctx.GetText()}
	case *gen.AliasedQueryContext:
		v.SubQuery = &Statement{
			isSubQuery:     true,
			isFromSubQuery: true,
		}
		if alias := c.TableAlias(); alias != nil {
			if ident := alias.StrictIdentifier(); ident != nil {
				v.Alias = ident.GetText()
			}
		}
		next = v.SubQuery
	}
	result := visitChildren(v.AddIgnoreField, v.Encode, v.aliasScope, next, ctx)

	if v.SubQuery != nil && v.onAliasesReady != nil {
		v.onAliasesReady(v.SubQuery.collectSelectAliases())
	}
	return result
}

type SelectNode struct {
	baseNode

	DistinctIndex int
	Distinct      bool
	fieldsNode    []Node
}

// WithAliasScope 는 SelectNode 자신과 이미 파싱된 모든 하위 FieldNode 에 aliasScope 를 전파합니다.
// SELECT 절은 FROM 절보다 먼저 파싱되므로, 서브쿼리 alias 가 확정된 후 재전파가 필요합니다.
func (v *SelectNode) WithAliasScope(aliases map[string]struct{}) {
	v.aliasScope = aliases
	for _, fn := range v.fieldsNode {
		fn.WithAliasScope(aliases)
	}
}

func (v *SelectNode) VisitTerminal(ctx antlr.TerminalNode) any {
	name := ctx.GetText()
	switch name {
	case "DISTINCT":
		v.Distinct = true
		v.DistinctIndex = len(v.fieldsNode)
	}
	return nil
}

func (v *SelectNode) String() string {
	var ns []string
	for idx, fn := range v.fieldsNode {
		ss := nodeToString(fn)
		if ss != "" {
			if v.Distinct && idx == v.DistinctIndex {
				// 如果字段包含AS别名，则不添加外层括号
				if strings.Contains(ss, " AS ") {
					ss = fmt.Sprintf("DISTINCT %s", ss)
				} else {
					ss = fmt.Sprintf("DISTINCT(%s)", ss)
				}
			}
			ns = append(ns, ss)
		}
	}

	return strings.Join(ns, ", ")
}

func (v *SelectNode) VisitChildren(ctx antlr.RuleNode) any {
	var next Node
	next = v

	switch ctx.(type) {
	case *gen.NamedExpressionContext:
		fn := &FieldNode{
			exprType: selectCtxType,
		}
		next = fn
		v.fieldsNode = append(v.fieldsNode, fn)
	}

	return visitChildren(v.AddIgnoreField, v.Encode, v.aliasScope, next, ctx)
}

type FieldNode struct {
	baseNode

	// 表达式类型
	exprType int

	// 区分是函数计算还是字段本身
	isField bool

	node Node
	as   Node

	sort Node

	args []Node
}

// WithAliasScope 将 aliasScope 向下传播到 node（可能是 FunctionNode）。
func (v *FieldNode) WithAliasScope(aliases map[string]struct{}) {
	v.aliasScope = aliases
	if v.node != nil {
		v.node.WithAliasScope(aliases)
	}
}

func (v *FieldNode) String() string {
	var result string
	result = nodeToString(v.node)

	if result == Star {
		return result
	}

	if v.isField && v.Encode != nil {
		key := strings.Trim(result, "`")
		if _, isAlias := v.aliasScope[key]; isAlias {
			// 字段来自子查询 SELECT alias，在外层任何位置（SELECT/WHERE/GROUP BY 等）
			// 都应保留原名并加反引号，不走 fieldMap 转换（否则会被映射为 Null）。
			result = fmt.Sprintf("`%s`", key)
		} else {
			originField, as := v.Encode(result)
			if v.exprType == selectCtxType && as != "" && v.as == nil {
				v.as = &StringNode{Name: as}
			}
			result = originField
		}
	}

	if result == metadata.Null {
		if v.exprType != selectCtxType && v.exprType != whereCtxType {
			return ""
		}
	}

	var cols []string
	for _, val := range v.args {
		col := nodeToString(val)
		if col != "" {
			cols = append(cols, col)
		}
	}
	if len(cols) > 0 {
		result = fmt.Sprintf("%s[%s]", result, strings.Join(cols, "]["))
	}

	as := nodeToString(v.as)
	if as != "" {
		result = fmt.Sprintf("%s %s %s", result, AsItem, as)
	}

	sort := nodeToString(v.sort)
	if sort != "" && result != "" {
		result = fmt.Sprintf("%s %s", result, sort)
	}

	return result
}

func (v *FieldNode) VisitChildren(ctx antlr.RuleNode) any {
	next := visitFieldNode(ctx, v)
	return visitChildren(v.AddIgnoreField, v.Encode, v.aliasScope, next, ctx)
}

type BinaryNode struct {
	baseNode
	Left  Node
	Right Node
	Op    Node
}

func (v *BinaryNode) String() string {
	return fmt.Sprintf("%s %s %s", nodeToString(v.Left), nodeToString(v.Op), nodeToString(v.Right))
}

func (v *BinaryNode) VisitTerminal(node antlr.TerminalNode) any {
	v.Op = &StringNode{
		Name: node.GetText(),
	}
	return nil
}

func (v *BinaryNode) VisitChildren(ctx antlr.RuleNode) any {
	var next Node
	next = v

	switch ctx.(type) {
	case *gen.ArithmeticBinaryContext:
		if v.Op == nil {
			v.Left = &BinaryNode{}
			next = v.Left
		} else {
			v.Right = &BinaryNode{}
			next = v.Right
		}
	case *gen.ValueExpressionDefaultContext:
		// 算术子式中的列（含函数参数内的 dt/3600 等）应只做物理列映射，不应套用 SELECT 列表的「列 AS 展示名」规则。
		if v.Op == nil {
			v.Left = &FieldNode{
				exprType: whereCtxType,
			}
			next = v.Left
		} else {
			v.Right = &FieldNode{
				exprType: whereCtxType,
			}
			next = v.Right
		}
	// 兼容类型识别异常情况
	case *antlr.BaseParserRuleContext:
		if v.Op == nil {
			v.Left = &StringNode{Name: ctx.GetText()}
		} else {
			v.Right = &StringNode{Name: ctx.GetText()}
		}
	}
	return visitChildren(v.AddIgnoreField, v.Encode, v.aliasScope, next, ctx)
}

type FunctionNode struct {
	baseNode

	Distinct bool
	FuncName string
	Values   []Node
}

// WithAliasScope 将 aliasScope 传播到所有已存储的子节点（含 FieldNode）。
func (v *FunctionNode) WithAliasScope(aliases map[string]struct{}) {
	v.aliasScope = aliases
	for _, val := range v.Values {
		val.WithAliasScope(aliases)
	}
}

func (v *FunctionNode) String() string {
	var result string

	var cols []string
	for _, val := range v.Values {
		col := nodeToString(val)
		if col != "" {
			cols = append(cols, col)
		}
	}

	result = strings.Join(cols, ", ")

	if v.Distinct {
		result = fmt.Sprintf("DISTINCT(%s)", result)
	}

	if v.FuncName != "" {
		result = fmt.Sprintf("%s(%s)", v.FuncName, result)
	}
	return result
}

func (v *FunctionNode) VisitTerminal(ctx antlr.TerminalNode) any {
	name := ctx.GetText()
	switch name {
	case "DISTINCT":
		v.Distinct = true
	}
	return nil
}

func (v *FunctionNode) VisitChildren(ctx antlr.RuleNode) any {
	var next Node
	next = v

	switch ctx.(type) {
	case *gen.SearchedCaseContext:
		sn := &SearchCaseNode{}
		v.Values = append(v.Values, sn)
		next = sn
	case *gen.ArithmeticBinaryContext:
		bn := &BinaryNode{}
		v.Values = append(v.Values, bn)
		next = bn
	case *gen.CastContext:
		bn := &CastNode{}
		v.Values = append(v.Values, bn)
		next = bn
	case *gen.FunctionCallContext:
		bn := &FunctionNode{}
		v.Values = append(v.Values, bn)
		next = bn
	case *gen.FunctionIdentifierContext:
		v.FuncName = ctx.GetText()
	case *gen.ColumnReferenceContext:
		// 用 FieldNode 延迟到渲染时处理 aliasScope 和 Encode，
		// 避免解析时 aliasScope 尚未就绪（SELECT 比 FROM 先解析）。
		// 使用 whereCtxType 防止 Encode 返回 (value, alias) 时错误地添加 AS。
		fn := &FieldNode{
			exprType: whereCtxType,
			isField:  true,
			node:     &ColumnNode{Names: []Node{&StringNode{Name: ctx.GetText()}}},
		}
		fn.WithEncode(v.Encode)
		fn.WithAliasScope(v.aliasScope)
		v.Values = append(v.Values, fn)
	case *gen.ConstantDefaultContext:
		v.Values = append(v.Values, &StringNode{Name: ctx.GetText()})
	case *gen.StarContext:
		v.Values = append(v.Values, &StringNode{Name: ctx.GetText()})
	}
	return visitChildren(v.AddIgnoreField, v.Encode, v.aliasScope, next, ctx)
}

type SearchCaseNode struct {
	baseNode

	ops   []string
	nodes []Node
}

func (v *SearchCaseNode) String() string {
	s := strings.Builder{}
	if len(v.nodes) > 0 && len(v.ops) > len(v.nodes) {
		s.WriteString("CASE")
		for idx, n := range v.nodes {
			op := v.ops[idx+1]

			when := nodeToString(n)
			if when != "" {
				s.WriteString(fmt.Sprintf(" %s %s", op, nodeToString(n)))
			}
		}

		s.WriteString(" END")
	}

	return s.String()
}

func (v *SearchCaseNode) VisitTerminal(ctx antlr.TerminalNode) any {
	v.ops = append(v.ops, strings.ToUpper(ctx.GetText()))
	return nil
}

func (v *SearchCaseNode) VisitChildren(ctx antlr.RuleNode) any {
	var next Node
	next = v

	switch ctx.(type) {
	case *gen.ArithmeticBinaryContext:
		bn := &BinaryNode{}
		v.nodes = append(v.nodes, bn)
		next = bn
	case *gen.CastContext:
		bn := &CastNode{}
		v.nodes = append(v.nodes, bn)
		next = bn
	case *gen.FunctionCallContext:
		bn := &FunctionNode{}
		v.nodes = append(v.nodes, bn)
		next = bn
	case *gen.ColumnReferenceContext:
		col := ctx.GetText()
		if v.Encode != nil {
			col, _ = v.Encode(col)
		}
		cn := &StringNode{Name: col}
		v.nodes = append(v.nodes, cn)
		next = cn
	case *gen.ConstantDefaultContext, *gen.StarContext:
		sn := &StringNode{Name: ctx.GetText()}
		v.nodes = append(v.nodes, sn)
		next = sn
	}
	return visitChildren(v.AddIgnoreField, v.Encode, v.aliasScope, next, ctx)
}

type CastNode struct {
	baseNode
	Value Node
	As    Node
}

func (v *CastNode) String() string {
	var result string
	result = nodeToString(v.Value)

	as := nodeToString(v.As)
	if as != "" {
		result = fmt.Sprintf("CAST(%s %s %s)", result, AsItem, as)
	}
	return result
}

func (v *CastNode) VisitChildren(ctx antlr.RuleNode) any {
	var next Node
	next = v

	switch ctx.(type) {
	case *gen.CastDataTypeContext:
		v.As = &StringNode{
			Name: ctx.GetText(),
		}
		next = v.As
	case *gen.ArithmeticBinaryContext:
		// CAST(FLOOR(x)*n AS T) 的内层是乘法，必须先建 BinaryNode，否则 n 会按 ConstantDefaultContext 被追加到 FLOOR 的 FunctionNode 上。
		v.Value = &BinaryNode{}
		next = v.Value
	case *gen.FunctionCallContext:
		v.Value = &FunctionNode{}
		next = v.Value
	case *gen.ColumnReferenceContext:
		v.Value = &ColumnNode{
			Names: []Node{
				&StringNode{Name: ctx.GetText()},
			},
		}
	case *gen.ConstantDefaultContext:
		if v.Value != nil {
			switch n := v.Value.(type) {
			case *ColumnNode:
				n.Sep = "]["
				n.Names = append(n.Names, &StringNode{Name: ctx.GetText()})
			case *FunctionNode:
				n.Values = append(n.Values, &StringNode{Name: ctx.GetText()})
			}
		} else {
			v.Value = &StringNode{
				Name: ctx.GetText(),
			}
		}
	case *gen.StarContext:
		v.Value = &StringNode{Name: ctx.GetText()}
		next = v.Value
	}
	return visitChildren(v.AddIgnoreField, v.Encode, v.aliasScope, next, ctx)
}

type ColumnNode struct {
	baseNode

	Sep   string
	Names []Node
}

func (v *ColumnNode) String() string {
	var ns []string
	for _, name := range v.Names {
		s := nodeToString(name)
		if s != "" {
			ns = append(ns, s)
		}
	}
	if len(ns) == 0 {
		return ""
	}

	if v.Sep == "." {
		return strings.Join(ns, v.Sep)
	}

	s := ns[0]
	if len(ns) > 1 {
		s = fmt.Sprintf("%s[%s]", s, strings.Join(ns[1:], v.Sep))
	}
	return s
}

func (v *ColumnNode) VisitChildren(ctx antlr.RuleNode) any {
	return visitChildren(v.AddIgnoreField, v.Encode, v.aliasScope, v, ctx)
}

type ValueNode struct {
	baseNode

	nodes []Node
}

func (v *ValueNode) String() string {
	var names []string
	for _, n := range v.nodes {
		names = append(names, n.String())
	}
	if len(names) == 1 {
		return names[0]
	}

	return strings.Join(names, ", ")
}

func (v *ValueNode) VisitChildren(ctx antlr.RuleNode) any {
	var next Node
	next = v

	switch ctx.(type) {
	case *gen.FunctionCallContext:
		node := &FunctionNode{}
		v.nodes = append(v.nodes, node)
		next = node
	case *gen.ConstantDefaultContext:
		v.nodes = append(v.nodes, &StringNode{Name: ctx.GetText()})
	}
	return visitChildren(v.AddIgnoreField, v.Encode, v.aliasScope, next, ctx)
}

type StringsNode struct {
	baseNode
	Names []string
}

func (v *StringsNode) add(s string) {
	v.Names = append(v.Names, s)
}

func (v *StringsNode) String() string {
	return strings.Join(v.Names, " ")
}

func (v *StringsNode) VisitChildren(ctx antlr.RuleNode) any {
	return visitChildren(v.AddIgnoreField, v.Encode, v.aliasScope, v, ctx)
}

type StringNode struct {
	baseNode
	Name string
}

func (v *StringNode) String() string {
	return v.Name
}

func (v *StringNode) VisitChildren(ctx antlr.RuleNode) any {
	return visitChildren(v.AddIgnoreField, v.Encode, v.aliasScope, v, ctx)
}

func visitFieldNode(ctx antlr.RuleNode, node *FieldNode) Node {
	var next Node
	next = node

	switch ctx.(type) {
	case *gen.SubqueryExpressionContext:
		node.node = &Statement{
			isSubQuery: true,
		}
		next = node.node
	case *gen.SearchedCaseContext:
		node.node = &SearchCaseNode{}
		next = node.node
	case *gen.ArithmeticBinaryContext:
		node.node = &BinaryNode{}
		next = node.node
	case *gen.CastContext:
		node.node = &CastNode{}
		next = node.node
	case *gen.FunctionCallContext:
		node.node = &FunctionNode{}
		next = node.node
	case *gen.ColumnReferenceContext:
		node.node = &ColumnNode{}
		node.isField = true
	// 兼容 a.b.c 的字段情况
	case *gen.IdentifierContext:
		if node.node != nil {
			switch n := node.node.(type) {
			case *ColumnNode:
				n.Sep = "."
				n.Names = append(n.Names, &StringNode{Name: ctx.GetText()})
			}
		}
	case *gen.ConstantDefaultContext:
		if node.node != nil {
			switch n := node.node.(type) {
			case *ColumnNode:
				n.Sep = "]["
				n.Names = append(n.Names, &StringNode{Name: ctx.GetText()})
			case *FunctionNode:
				n.Values = append(n.Values, &StringNode{Name: ctx.GetText()})
			}
		} else {
			node.node = &StringNode{
				Name: ctx.GetText(),
			}
		}
	case *gen.IdentifierOrTextContext:
		if node.AddIgnoreField != nil {
			node.AddIgnoreField(ctx.GetText())
		}
		node.as = &StringNode{
			Name: ctx.GetText(),
		}
		next = node.as
	case *gen.StarContext:
		node.node = &StringNode{Name: ctx.GetText()}
	}

	return next
}

func nodeToString(node Node) string {
	if node == nil {
		return ""
	}
	return node.String()
}

func visitChildren(addIgnoreField func(string), encode Encode, aliasScope map[string]struct{}, next Node, node antlr.RuleNode) any {
	next.WithAddIgnoreField(addIgnoreField)
	next.WithEncode(encode)
	next.WithAliasScope(aliasScope)
	for _, child := range node.GetChildren() {
		if tree, ok := child.(antlr.ParseTree); ok {
			log.Debugf(context.TODO(), `"ENTER","%T","%s"`, tree, tree.GetText())
			tree.Accept(next)
			log.Debugf(context.TODO(), `"EXIT","%T","%s"`, tree, tree.GetText())
		}
	}

	return nil
}

type Option struct {
	DimensionTransform Encode
	AddIgnoreField     func(string)

	Tables               []string
	Where                string
	TableFieldsMap       TableFieldsMap
	RejectSelectAllUnion bool
	Offset               int
	Limit                int
}
