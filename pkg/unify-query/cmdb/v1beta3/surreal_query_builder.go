// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta3

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// SQL 模板常量
const (
	sqlIndent1 = "    "

	fieldIn  = "in"
	fieldOut = "out"
)

// buildEntityDataFields 构建 entity_data 字段列表
func buildEntityDataFields(keys []string, prefix string) string {
	fields := make([]string, 0, len(keys))
	for _, key := range keys {
		identifier := escapeSurrealIdentifier(key)
		if prefix == "" {
			fields = append(fields, fmt.Sprintf("%s: %s", identifier, identifier))
		} else {
			fields = append(fields, fmt.Sprintf("%s: %s.%s", identifier, prefix, identifier))
		}
	}
	return strings.Join(fields, ", ")
}

// SurrealQueryBuilder 构建 SurrealQL 关联查询
type SurrealQueryBuilder struct {
	request         *QueryRequest
	pathFinder      *PathFinder
	schemaProvider  SchemaProvider
	namespace       string
	transitions     map[int]map[ResourceType]map[pathTransition]struct{}
	projectLiveness bool
	pathHopCount    int
}

type pathTransition struct {
	relationType RelationType
	targetType   ResourceType
	direction    TraversalDirection
}

// NewSurrealQueryBuilder 创建查询构建器
// 如果不提供 schemaProvider,将使用默认的 StaticSchemaProvider
func NewSurrealQueryBuilder(request *QueryRequest, opts ...PathFinderOption) *SurrealQueryBuilder {
	return NewSurrealQueryBuilderWithSchemaProvider(request, GetSchemaProvider(), opts...)
}

func NewSurrealQueryBuilderWithSchemaProvider(request *QueryRequest, provider SchemaProvider, opts ...PathFinderOption) *SurrealQueryBuilder {
	request.Normalize()
	namespace := request.SchemaNamespace()

	if provider == nil {
		provider = GetSchemaProvider()
	}

	// 创建 PathFinder 时传入 SchemaProvider
	allOpts := append([]PathFinderOption{
		WithSchemaProvider(provider),
		WithNamespace(namespace),
		WithAllowedCategories(request.AllowedRelationTypes...),
		WithDynamicDirection(request.DynamicRelationDirection),
		WithMaxHops(request.MaxHops),
	}, opts...)

	pf := NewPathFinder(allOpts...)

	return &SurrealQueryBuilder{
		request:         request,
		pathFinder:      pf,
		schemaProvider:  provider,
		namespace:       namespace,
		transitions:     buildPathTransitions(request, pf),
		projectLiveness: true,
	}
}

func NewSurrealQueryBuilderForPath(request *QueryRequest, provider SchemaProvider, path resourcePath) *SurrealQueryBuilder {
	pathRequest := cloneQueryRequest(request)
	if hops := len(path.Steps) - 1; hops >= 0 {
		// 单 path 查询只需要展开该 path 的实际跳数；直接路径不再生成空 hop2。
		pathRequest.MaxHops = hops
	}

	builder := NewSurrealQueryBuilderWithSchemaProvider(pathRequest, provider)
	builder.transitions = buildTransitionsFromPaths([]resourcePath{path})
	builder.pathHopCount = maxInt(0, len(path.Steps)-1)
	builder.request.MaxHops = builder.pathHopCount
	return builder
}

func (b *SurrealQueryBuilder) routeName() string {
	if b.usesFlatOneHopRelationQuery() {
		return "single_table_flat_one_hop"
	}
	return "single_table"
}

func (b *SurrealQueryBuilder) WithoutLivenessProjection() *SurrealQueryBuilder {
	if b != nil {
		b.projectLiveness = false
	}
	return b
}

func cloneQueryRequest(request *QueryRequest) *QueryRequest {
	if request == nil {
		return &QueryRequest{}
	}

	cloned := *request
	if request.SourceInfo != nil {
		cloned.SourceInfo = make(map[string]string, len(request.SourceInfo))
		for k, v := range request.SourceInfo {
			cloned.SourceInfo[k] = v
		}
	}
	if request.SourceExpandInfo != nil {
		cloned.SourceExpandInfo = make(map[string]string, len(request.SourceExpandInfo))
		for k, v := range request.SourceExpandInfo {
			cloned.SourceExpandInfo[k] = v
		}
	}
	if request.PathResource != nil {
		cloned.PathResource = append([]ResourceType(nil), request.PathResource...)
	}
	if request.AllowedRelationTypes != nil {
		cloned.AllowedRelationTypes = append([]RelationCategory(nil), request.AllowedRelationTypes...)
	}
	return &cloned
}

// buildPathTransitions 将 PathFinder 算出的候选路径压成「第几跳 + 当前资源类型 -> 允许的关系转移」。
//
// SurrealQL 是按资源类型逐层展开的；当调用方显式指定 target/path_resource 时，
// 这里先把不能到达目标的关系剪掉，避免生成无关 hop 查询。返回 nil 表示不做剪枝，
// 构造器会沿用原有按资源类型全量展开的行为。
func buildPathTransitions(request *QueryRequest, pf *PathFinder) map[int]map[ResourceType]map[pathTransition]struct{} {
	if request == nil || pf == nil || request.MaxHops <= 0 || request.SourceType == "" || request.TargetType == "" {
		return nil
	}
	if !request.TargetTypeExplicit && request.SourceType == request.TargetType {
		// 未显式指定 target_type 的同类型查询是旧接口的信息展示路径，需要保留全量展开能力。
		return nil
	}

	paths, err := pf.FindAllPaths(request.SourceType, request.TargetType, request.PathResource)
	if err != nil {
		// 找不到路径时交给后续校验返回错误；这里不额外收窄，避免构造器因空转移误生成空 hop。
		return nil
	}

	return buildTransitionsFromPaths(paths)
}

func buildTransitionsFromPaths(paths []resourcePath) map[int]map[ResourceType]map[pathTransition]struct{} {
	transitions := make(map[int]map[ResourceType]map[pathTransition]struct{})
	for _, path := range paths {
		for hop := 1; hop < len(path.Steps); hop++ {
			currentType := ResourceType(path.Steps[hop-1].ResourceType)
			step := path.Steps[hop]

			bySource, ok := transitions[hop]
			if !ok {
				bySource = make(map[ResourceType]map[pathTransition]struct{})
				transitions[hop] = bySource
			}
			allowed, ok := bySource[currentType]
			if !ok {
				allowed = make(map[pathTransition]struct{})
				bySource[currentType] = allowed
			}
			allowed[pathTransition{
				relationType: RelationType(step.RelationType),
				targetType:   ResourceType(step.ResourceType),
				direction:    TraversalDirection(step.Direction),
			}] = struct{}{}
		}
	}

	return transitions
}

// Build 构建完整的 SurrealQL 查询
func (b *SurrealQueryBuilder) Build() string {
	var sb strings.Builder
	sb.WriteString(b.buildVariables())
	sb.WriteString("\n\n")
	if b.usesFlatOneHopRelationQuery() {
		sb.WriteString(b.buildFlatOneHopRelationQuery(b.getRelationsForType(1, b.request.SourceType)[0]))
		return sb.String()
	}
	sb.WriteString(b.buildMainQuery())
	return sb.String()
}

// usesFlatOneHopRelationQuery reads a single path directly from its relation table.
func (b *SurrealQueryBuilder) usesFlatOneHopRelationQuery() bool {
	return b != nil && b.pathHopCount == 1 && len(b.getRelationsForType(1, b.request.SourceType)) == 1
}

func (b *SurrealQueryBuilder) buildFlatOneHopRelationQuery(rel *RelationQueryInfo) string {
	relationType := rel.Schema.RelationType
	table := surrealTableName(string(relationType))
	sourceIDField := "source_id"
	targetIDField := "target_id"
	if rel.WhereField == fieldOut {
		sourceIDField = "target_id"
		targetIDField = "source_id"
	}

	direction := ""
	if rel.Schema.Category == RelationCategoryDynamic {
		direction = fmt.Sprintf("\n            direction: '%s',", rel.Direction)
	}

	sourceQuery := "SELECT VALUE id FROM " + surrealTableName(string(b.request.SourceType))
	if conditions := b.sourceFilterConditions(true); len(conditions) > 0 {
		sourceQuery += " WHERE " + strings.Join(conditions, " AND ")
	}
	sourceData := "{ " + buildEntityDataFields(b.rootEntityDataFields(b.request.SourceType), sourceIDField) + " }"
	targetData := "{ " + buildEntityDataFields(b.targetEntityDataFields(rel.TargetType), targetIDField) + " }"
	relationLiveness := ""
	if b.projectLiveness {
		relationLiveness = "\n            relation_liveness: [{ period_start: active_period_start_ms, period_end: active_period_end_ms }],"
	}

	return fmt.Sprintf(`LET $source_ids = (%s);
SELECT {
    root: {
        entity_type: '%s',
        entity_id: <string>%s,
        entity_data: %s
    },
    hop1: {
        %s: [{
            hop: 1,
            relation_type: '%s',
            relation_category: '%s',%s
            relation_id: <string>type::record('%s', relation_id),%s
            target: {
                entity_type: %s,
                entity_id: <string>%s,
                entity_data: %s
            }
        }]
    }
} AS result
FROM %s
WHERE %s
  AND active_period_start_ms <= active_period_end_ms
  AND active_period_start_ms <= $end_ms
  AND active_period_end_ms >= $start_ms
LIMIT %d;`,
		sourceQuery,
		b.request.SourceType,
		sourceIDField,
		sourceData,
		surrealObjectKey(string(relationType)+rel.KeySuffix),
		relationType,
		rel.Schema.Category,
		direction,
		escapeSurrealString(string(relationType)),
		relationLiveness,
		fmt.Sprintf("'%s'", rel.TargetType),
		targetIDField,
		targetData,
		table,
		sourceIDField+" IN $source_ids",
		maxEdgesPerHopQueryLimit(),
	)
}

// buildFlatRelationQueryForPath resolves endpoint IDs using entity labels and
// reads one relation table, without recursively expanding the next hop in SQL.
func buildFlatRelationQueryForPath(
	request *QueryRequest,
	provider SchemaProvider,
	path resourcePath,
	mode graphQueryMode,
) (string, bool) {
	if request == nil || len(path.Steps) != 2 {
		return "", false
	}

	builder := NewSurrealQueryBuilderForPath(request, provider, path)
	configureBuilderForGraphQueryMode(builder, mode)
	relations := builder.getRelationsForType(1, builder.request.SourceType)
	if len(relations) != 1 {
		return "", false
	}

	return builder.buildVariables() + "\n\n" + builder.buildFlatOneHopRelationQuery(relations[0]), true
}

// buildVariables 构建变量定义部分
func (b *SurrealQueryBuilder) buildVariables() string {
	startMs, endMs := b.request.GetQueryRange()
	return fmt.Sprintf(`LET $start_ms = %d;
LET $end_ms = %d;`,
		startMs,
		endMs)
}

// buildMainQuery 构建主查询
func (b *SurrealQueryBuilder) buildMainQuery() string {
	var sb strings.Builder

	sb.WriteString("SELECT {\n")
	sb.WriteString(sqlIndent1 + "root: ")
	sb.WriteString(b.buildRootSelect())

	if b.request.MaxHops > 0 {
		sb.WriteString(",\n\n")
		sb.WriteString(sqlIndent1 + "hop1: ")
		sb.WriteString(b.buildHopSelect(1, b.request.SourceType))
	}
	sb.WriteString("\n")

	sb.WriteString("} AS result\n")
	sb.WriteString(fmt.Sprintf("FROM %s\n", b.buildRootSource()))
	sb.WriteString(b.buildWhereClause())
	sb.WriteString("\n")
	if b.request.DisableRootLimit {
		sb.WriteString(";")
	} else {
		sb.WriteString(fmt.Sprintf("LIMIT %d;", b.request.Limit))
	}

	return sb.String()
}

// buildRootSource reads stored entities; their record IDs come from Databus
// and must not be reconstructed from metadata label names.
func (b *SurrealQueryBuilder) buildRootSource() string {
	return surrealTableName(string(b.request.SourceType))
}

func (b *SurrealQueryBuilder) buildRootSelect() string {
	sourceType := b.request.SourceType
	rootFields := b.rootEntityDataFields(sourceType)
	return fmt.Sprintf(`{
        entity_type: meta::tb(id),
        entity_id: <string>id,
        entity_data: { %s },
        created_at: created_at,
        updated_at: updated_at
    }`,
		buildEntityDataFields(rootFields, ""))
}

func (b *SurrealQueryBuilder) rootEntityDataFields(sourceType ResourceType) []string {
	fields := b.schemaProvider.GetResourcePrimaryKeys(b.namespace, sourceType)
	if b.request.TargetInfoShow && !b.request.TargetTypeExplicit && b.request.TargetType == sourceType {
		// 省略 target_type 时 root 就是隐式 target。此时 target_info_show 必须作用在 root 投影上，
		// 否则后续 filterTargetMatcher 会保留扩展字段，但 SQL 根本没有查出这些字段。
		if infoFields := b.schemaProvider.GetResourceFields(b.namespace, sourceType); len(infoFields) > 0 {
			fields = infoFields
		}
	}
	return fields
}

func (b *SurrealQueryBuilder) targetEntityDataFields(targetType ResourceType) []string {
	fields := b.schemaProvider.GetResourcePrimaryKeys(b.namespace, targetType)
	if b.request.TargetInfoShow {
		if infoFields := b.schemaProvider.GetResourceFields(b.namespace, targetType); len(infoFields) > 0 {
			fields = infoFields
		}
	}
	return fields
}

// buildHopSelect 构建指定跳数的 SELECT 结构
func (b *SurrealQueryBuilder) buildHopSelect(hop int, currentType ResourceType) string {
	if hop > b.request.MaxHops {
		return "{}"
	}

	relations := b.getRelationsForType(hop, currentType)
	if len(relations) == 0 {
		return "{}"
	}

	var sb strings.Builder
	sb.WriteString("{\n")

	first := true
	for _, rel := range relations {
		if !first {
			sb.WriteString(",\n")
		}
		first = false
		sb.WriteString(b.buildRelationQuery(hop, currentType, rel))
	}

	sb.WriteString("\n" + sqlIndent1 + "}")

	return sb.String()
}

// RelationQueryInfo 关系查询信息
type RelationQueryInfo struct {
	Schema      *RelationSchema
	Direction   TraversalDirection
	KeySuffix   string       // 键名后缀（动态关系才有）
	TargetField string       // 目标字段 (in 或 out)
	TargetType  ResourceType // 目标资源类型
	WhereField  string       // WHERE 子句中用于匹配当前实体的字段
	SelectField string       // SELECT 中获取目标实体的字段
}

// getRelationsForType 获取指定 hop / 资源类型下仍可能到达 target 的关系查询
func (b *SurrealQueryBuilder) getRelationsForType(hop int, resourceType ResourceType) []*RelationQueryInfo {
	relations := b.pathFinder.getRelationsForType(resourceType)
	if len(relations) == 0 || b.transitions == nil {
		return relations
	}

	bySource := b.transitions[hop]
	if len(bySource) == 0 {
		return nil
	}
	allowed := bySource[resourceType]
	if len(allowed) == 0 {
		return nil
	}

	filtered := make([]*RelationQueryInfo, 0, len(relations))
	for _, rel := range relations {
		key := pathTransition{
			relationType: rel.Schema.RelationType,
			targetType:   rel.TargetType,
			direction:    rel.Direction,
		}
		if _, ok := allowed[key]; ok {
			filtered = append(filtered, rel)
		}
	}
	return filtered
}

// buildRelationQuery reads periods directly from the relation table. Endpoint records
// supply labels because the five-argument writer does not populate snapshots.
func (b *SurrealQueryBuilder) buildRelationQuery(hop int, _ ResourceType, rel *RelationQueryInfo) string {
	return b.buildRelationQueryFrom(hop, rel, "$parent.id")
}

func (b *SurrealQueryBuilder) buildRelationQueryFrom(hop int, rel *RelationQueryInfo, parentRef string) string {
	matchField, targetField := "source_id", "target_id"
	if rel.WhereField == fieldOut {
		matchField, targetField = "target_id", "source_id"
	}
	fields := []string{
		fmt.Sprintf("hop: %d", hop),
		fmt.Sprintf("relation_type: '%s'", rel.Schema.RelationType),
		fmt.Sprintf("relation_category: '%s'", rel.Schema.Category),
		fmt.Sprintf("relation_id: <string>type::record('%s', relation_id)", escapeSurrealString(string(rel.Schema.RelationType))),
	}
	if rel.Schema.Category == RelationCategoryDynamic {
		fields = append(fields, fmt.Sprintf("direction: '%s'", rel.Direction))
	}
	if b.projectLiveness {
		fields = append(fields, "relation_liveness: [{ period_start: active_period_start_ms, period_end: active_period_end_ms }]")
	}
	target := fmt.Sprintf("entity_type: '%s', entity_id: <string>%s, entity_data: { %s }", rel.TargetType, targetField, buildEntityDataFields(b.targetEntityDataFields(rel.TargetType), targetField))
	if hop < b.request.MaxHops {
		children := []string{}
		for _, next := range b.getRelationsForType(hop+1, rel.TargetType) {
			children = append(children, b.buildRelationQueryFrom(hop+1, next, "$parent."+targetField))
		}
		target += fmt.Sprintf(", hop%d: { %s }", hop+1, strings.Join(children, ", "))
	}
	fields = append(fields, "target: { "+target+" }")
	return fmt.Sprintf(`%s: (SELECT VALUE { %s } FROM %s WHERE %s = %s
 AND active_period_start_ms <= active_period_end_ms
 AND active_period_start_ms <= $end_ms AND active_period_end_ms >= $start_ms LIMIT %d)`,
		surrealObjectKey(string(rel.Schema.RelationType)+rel.KeySuffix), strings.Join(fields, ", "), surrealTableName(string(rel.Schema.RelationType)), matchField, parentRef, maxEdgesPerHopQueryLimit())
}

// buildWhereClause 构建 WHERE 子句
func (b *SurrealQueryBuilder) buildWhereClause() string {
	conditions := b.sourceFilterConditions(true)
	if len(conditions) == 0 {
		return ""
	}
	return "WHERE " + strings.Join(conditions, "\n  AND ")
}

func (b *SurrealQueryBuilder) sourceFilterConditions(includePrimaryKeys bool) []string {
	conditions := make([]string, 0, len(b.request.SourceInfo)+len(b.request.SourceExpandInfo))
	if includePrimaryKeys && len(b.request.SourceInfo) > 0 {
		// SourceInfo 已在 validateSourceInfoFields 中要求包含完整主键。
		// 这里再次只接收主键白名单，是 SQL 拼接层的兜底保护，避免未知字段进入 SurrealQL。
		allowedFields := make(map[string]bool)
		for _, pk := range b.schemaProvider.GetResourcePrimaryKeys(b.namespace, b.request.SourceType) {
			allowedFields[pk] = true
		}

		keys := make([]string, 0, len(b.request.SourceInfo))
		for k := range b.request.SourceInfo {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, k := range keys {
			if !allowedFields[k] {
				continue
			}
			conditions = append(conditions, fmt.Sprintf("%s = %s", escapeSurrealIdentifier(k), b.fieldLiteral(k, b.request.SourceInfo[k])))
		}
	}

	if len(b.request.SourceExpandInfo) > 0 {
		keys := make([]string, 0, len(b.request.SourceExpandInfo))
		for k := range b.request.SourceExpandInfo {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, k := range keys {
			conditions = append(conditions, fmt.Sprintf("%s = %s", escapeSurrealIdentifier(k), b.fieldLiteral(k, b.request.SourceExpandInfo[k])))
		}
	}
	return conditions
}

func (b *SurrealQueryBuilder) fieldLiteral(_, value string) string {
	// 图顶点属性由指标标签物化而来，即使 metadata 将输入字段声明为数值，
	// 其在 SurrealDB 中仍以字符串存储。构建查询前仍会按 metadata 校验请求值。
	return fmt.Sprintf("'%s'", escapeSurrealString(value))
}

func typedSurrealLiteral(fieldType, value string) (string, bool) {
	switch strings.ToLower(fieldType) {
	case "int", "integer":
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return "", false
		}
		return strconv.FormatInt(n, 10), true
	case "float", "double", "number":
		n, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsInf(n, 0) || math.IsNaN(n) {
			return "", false
		}
		return strconv.FormatFloat(n, 'g', -1, 64), true
	case "bool", "boolean":
		n, err := strconv.ParseBool(value)
		if err != nil {
			return "", false
		}
		return strconv.FormatBool(n), true
	default:
		return fmt.Sprintf("'%s'", escapeSurrealString(value)), true
	}
}

// escapeSurrealString 转义 SurrealQL 字符串中的特殊字符
func escapeSurrealString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return s
}

func isSafeSurrealField(field string) bool {
	if field == "" {
		return false
	}
	for index, r := range field {
		isLetter := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if isLetter || r == '_' || (index > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

// escapeSurrealIdentifier 保持简单 ASCII 标识符可读；复杂名称使用 SurrealQL 尖括号包裹，
// 并转义其中的结束符，避免产生非法语句。
func escapeSurrealIdentifier(identifier string) string {
	if isSafeSurrealField(identifier) {
		return identifier
	}
	return "⟨" + strings.ReplaceAll(identifier, "⟩", `\⟩`) + "⟩"
}

func surrealTableName(name string) string {
	return escapeSurrealIdentifier(name)
}

func surrealObjectKey(name string) string {
	return escapeSurrealIdentifier(name)
}
