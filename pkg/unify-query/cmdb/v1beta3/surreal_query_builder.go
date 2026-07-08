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
	"sort"
	"strings"
)

// SQL 模板常量
const (
	sqlIndent1 = "    "                     // 1级缩进
	sqlIndent2 = "        "                 // 2级缩进
	sqlIndent3 = "            "             // 3级缩进
	sqlIndent4 = "                "         // 4级缩进
	sqlIndent5 = "                    "     // 5级缩进
	sqlIndent6 = "                        " // 6级缩进

	fieldIn         = "in"
	fieldOut        = "out"
	fieldRelationID = "relation_id"

	// SQL 子查询模板
	tplLivenessSelect    = "(SELECT * FROM %s WHERE %s = $parent.id AND period_end >= $start AND period_start <= $end)"
	tplLivenessSelectRef = "(SELECT * FROM %s WHERE %s = $parent.%s AND period_end >= $start AND period_start <= $end)"
	tplRelLivenessSelect = "(SELECT * FROM %s WHERE relation_id = $parent.id AND period_end >= $start_ms AND period_start <= $end_ms)"
	tplLivenessFilter    = "(SELECT * FROM %s WHERE %s = $parent.id AND $end >= period_start AND $start <= period_end LIMIT 1)[0] != NONE"
	tplLivenessFilterRef = "(SELECT * FROM %s WHERE %s = $parent.%s AND $end >= period_start AND $start <= period_end LIMIT 1)[0] != NONE"
	tplRelLivenessFilter = "(SELECT * FROM %s WHERE relation_id = $parent.id AND $end_ms >= period_start AND $start_ms <= period_end LIMIT 1)[0] != NONE"
)

// buildEntityDataFields 构建 entity_data 字段列表
func buildEntityDataFields(keys []string, prefix string) string {
	fields := make([]string, 0, len(keys))
	for _, key := range keys {
		if prefix == "" {
			fields = append(fields, fmt.Sprintf("%s: %s", key, key))
		} else {
			fields = append(fields, fmt.Sprintf("%s: %s.%s", key, prefix, key))
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
	return builder
}

// WithoutLivenessProjection keeps liveness existence filters but omits liveness
// payloads from SELECT projections. Instant relation APIs only need target
// labels, while range APIs still require periods for bucket alignment.
func (b *SurrealQueryBuilder) WithoutLivenessProjection() *SurrealQueryBuilder {
	if b != nil {
		b.projectLiveness = false
	}
	return b
}

func (b *SurrealQueryBuilder) livenessProjection(prefix, field, tpl string, args ...any) string {
	if b == nil || !b.projectLiveness {
		return ""
	}
	return prefix + field + ": " + fmt.Sprintf(tpl, args...)
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
	sb.WriteString(b.buildMainQuery())
	return sb.String()
}

// buildVariables 构建变量定义部分
func (b *SurrealQueryBuilder) buildVariables() string {
	startMs, endMs := b.request.GetQueryRange()
	// 实体 liveness 表沿用旧 VM 秒级窗口，关系 liveness 表写入的是毫秒级窗口。
	// 同一条 SurrealQL 同时保留两组变量，避免在查询层混用单位导致节点或边误判为不活跃。
	startSec := startMs / 1000
	endSec := endMs / 1000
	return fmt.Sprintf(`LET $timestamp = %d;
LET $look_back_delta = %d;
LET $start = %d;
LET $end = %d;
LET $start_ms = %d;
LET $end_ms = %d;`,
		b.request.Timestamp,
		b.request.LookBackDelta,
		startSec,
		endSec,
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
	sb.WriteString(fmt.Sprintf("FROM %s\n", b.request.SourceType))
	sb.WriteString(b.buildWhereClause())
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("LIMIT %d;", b.request.Limit))

	return sb.String()
}

// buildRootSelect 构建 Root 实体的 SELECT 结构
func (b *SurrealQueryBuilder) buildRootSelect() string {
	sourceType := b.request.SourceType
	rootFields := b.rootEntityDataFields(sourceType)
	livenessTable := GetLivenessRecordTableName(sourceType)
	livenessIDField := GetLivenessIDField(sourceType)

	return fmt.Sprintf(`{
        entity_type: meta::tb(id),
        entity_id: <string>id,
        entity_data: { %s },
        created_at: created_at,
        updated_at: updated_at%s
    }`,
		buildEntityDataFields(rootFields, ""),
		b.livenessProjection(",\n        ", ResponseFieldLiveness, tplLivenessSelect, livenessTable, livenessIDField))
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

// buildRelationQuery 构建单个关系的查询
func (b *SurrealQueryBuilder) buildRelationQuery(hop int, _ ResourceType, rel *RelationQueryInfo) string {
	relationType := rel.Schema.RelationType
	relationTable := string(relationType)
	relationLivenessTable := GetRelationLivenessRecordTableName(relationType)
	targetLivenessTable := GetLivenessRecordTableName(rel.TargetType)
	targetLivenessIDField := GetLivenessIDField(rel.TargetType)
	keyName := relationTable + rel.KeySuffix
	targetFields := b.targetEntityDataFields(rel.TargetType)

	var fieldsBuilder strings.Builder
	fieldsBuilder.WriteString(fmt.Sprintf(`
            hop: %d,
            relation_type: '%s',
            relation_category: '%s',`, hop, relationType, rel.Schema.Category))

	if rel.Schema.Category == RelationCategoryDynamic {
		fieldsBuilder.WriteString(fmt.Sprintf(`
            direction: '%s',`, rel.Direction))
	}

	fieldsBuilder.WriteString(fmt.Sprintf(`
            relation_id: <string>id%s,
            target: {
                entity_type: '%s',
                entity_id: <string>%s,
                entity_data: { %s }%s`,
		b.livenessProjection(",\n            ", ResponseFieldRelationLiveness, tplRelLivenessSelect, relationLivenessTable),
		rel.TargetType,
		rel.SelectField,
		buildEntityDataFields(targetFields, rel.SelectField),
		b.livenessProjection(",\n                ", ResponseFieldLiveness, tplLivenessSelectRef, targetLivenessTable, targetLivenessIDField, rel.SelectField)))

	if hop < b.request.MaxHops {
		nextHopKey := fmt.Sprintf("hop%d", hop+1)
		nextHopSelect := b.buildNestedHopSelect(hop+1, rel.TargetType, rel.SelectField)
		fieldsBuilder.WriteString(fmt.Sprintf(`,
                %s: %s`, nextHopKey, nextHopSelect))
	}

	fieldsBuilder.WriteString(`
            }`)

	return fmt.Sprintf(sqlIndent2+`%s: (SELECT VALUE {%s
        } FROM %s WHERE %s = $parent.id
          AND `+tplRelLivenessFilter+`
          AND `+tplLivenessFilterRef+`)`,
		keyName,
		fieldsBuilder.String(),
		relationTable,
		rel.WhereField,
		relationLivenessTable,
		targetLivenessTable,
		targetLivenessIDField,
		rel.SelectField)
}

// buildNestedHopSelect 构建嵌套在 target 内的下一跳查询
func (b *SurrealQueryBuilder) buildNestedHopSelect(hop int, currentType ResourceType, parentField string) string {
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
		sb.WriteString(b.buildNestedRelationQuery(hop, rel, parentField))
	}

	sb.WriteString("\n" + sqlIndent4 + "}")

	return sb.String()
}

// buildNestedRelationQuery 构建嵌套的关系查询（用于 hop2+）
func (b *SurrealQueryBuilder) buildNestedRelationQuery(hop int, rel *RelationQueryInfo, parentField string) string {
	relationType := rel.Schema.RelationType
	relationTable := string(relationType)
	relationLivenessTable := GetRelationLivenessRecordTableName(relationType)
	targetLivenessTable := GetLivenessRecordTableName(rel.TargetType)
	targetLivenessIDField := GetLivenessIDField(rel.TargetType)
	keyName := relationTable + rel.KeySuffix
	targetFields := b.targetEntityDataFields(rel.TargetType)

	var fieldsBuilder strings.Builder
	fieldsBuilder.WriteString(fmt.Sprintf(`
                        hop: %d,
                        relation_type: '%s',
                        relation_category: '%s',`, hop, relationType, rel.Schema.Category))

	if rel.Schema.Category == RelationCategoryDynamic {
		fieldsBuilder.WriteString(fmt.Sprintf(`
                        direction: '%s',`, rel.Direction))
	}

	fieldsBuilder.WriteString(fmt.Sprintf(`
                        relation_id: <string>id%s,
                        target: {
                            entity_type: '%s',
                            entity_id: <string>%s,
                            entity_data: { %s }%s`,
		b.livenessProjection(",\n                        ", ResponseFieldRelationLiveness, tplRelLivenessSelect, relationLivenessTable),
		rel.TargetType,
		rel.SelectField,
		buildEntityDataFields(targetFields, rel.SelectField),
		b.livenessProjection(",\n                            ", ResponseFieldLiveness, tplLivenessSelectRef, targetLivenessTable, targetLivenessIDField, rel.SelectField)))

	if hop < b.request.MaxHops {
		nextHopKey := fmt.Sprintf("hop%d", hop+1)
		nextHopSelect := b.buildDeeperNestedHopSelect(hop+1, rel.TargetType, rel.SelectField, 4)
		fieldsBuilder.WriteString(fmt.Sprintf(`,
                            %s: %s`, nextHopKey, nextHopSelect))
	}

	fieldsBuilder.WriteString(`
                        }`)

	return fmt.Sprintf(sqlIndent5+`%s: (SELECT VALUE {%s
                    } FROM %s WHERE %s = $parent.%s
                      AND `+tplRelLivenessFilter+`
                      AND `+tplLivenessFilterRef+`)`,
		keyName,
		fieldsBuilder.String(),
		relationTable,
		rel.WhereField,
		parentField,
		relationLivenessTable,
		targetLivenessTable,
		targetLivenessIDField,
		rel.SelectField)
}

// buildDeeperNestedHopSelect 构建更深层嵌套的 hop（hop3+）
func (b *SurrealQueryBuilder) buildDeeperNestedHopSelect(hop int, currentType ResourceType, parentField string, indentLevel int) string {
	if hop > b.request.MaxHops {
		return "{}"
	}

	relations := b.getRelationsForType(hop, currentType)
	if len(relations) == 0 {
		return "{}"
	}

	indent := strings.Repeat(sqlIndent1, indentLevel)
	var sb strings.Builder
	sb.WriteString("{\n")

	first := true
	for _, rel := range relations {
		if !first {
			sb.WriteString(",\n")
		}
		first = false
		sb.WriteString(b.buildDeeperNestedRelationQuery(hop, rel, parentField, indentLevel))
	}

	sb.WriteString(fmt.Sprintf("\n%s}", indent))

	return sb.String()
}

// buildDeeperNestedRelationQuery 构建更深层嵌套的关系查询
func (b *SurrealQueryBuilder) buildDeeperNestedRelationQuery(hop int, rel *RelationQueryInfo, parentField string, indentLevel int) string {
	relationType := rel.Schema.RelationType
	relationTable := string(relationType)
	relationLivenessTable := GetRelationLivenessRecordTableName(relationType)
	targetLivenessTable := GetLivenessRecordTableName(rel.TargetType)
	targetLivenessIDField := GetLivenessIDField(rel.TargetType)

	keyName := relationTable + rel.KeySuffix
	indent := strings.Repeat(sqlIndent1, indentLevel)
	innerIndent := strings.Repeat(sqlIndent1, indentLevel+1)
	targetFields := b.targetEntityDataFields(rel.TargetType)

	var fieldsBuilder strings.Builder
	fieldsBuilder.WriteString(fmt.Sprintf(`
%shop: %d,
%srelation_type: '%s',
%srelation_category: '%s',`, innerIndent, hop, innerIndent, relationType, innerIndent, rel.Schema.Category))

	if rel.Schema.Category == RelationCategoryDynamic {
		fieldsBuilder.WriteString(fmt.Sprintf(`
%sdirection: '%s',`, innerIndent, rel.Direction))
	}

	fieldsBuilder.WriteString(fmt.Sprintf(`
%srelation_id: <string>id%s,
%starget: {
%s    entity_type: '%s',
%s    entity_id: <string>%s,
%s    entity_data: { %s }%s`,
		innerIndent,
		b.livenessProjection(fmt.Sprintf(",\n%s", innerIndent), ResponseFieldRelationLiveness, tplRelLivenessSelect, relationLivenessTable),
		innerIndent,
		innerIndent, rel.TargetType,
		innerIndent, rel.SelectField,
		innerIndent, buildEntityDataFields(targetFields, rel.SelectField),
		b.livenessProjection(fmt.Sprintf(",\n%s    ", innerIndent), ResponseFieldLiveness, tplLivenessSelectRef, targetLivenessTable, targetLivenessIDField, rel.SelectField)))

	if hop < b.request.MaxHops {
		nextHopKey := fmt.Sprintf("hop%d", hop+1)
		nextHopSelect := b.buildDeeperNestedHopSelect(hop+1, rel.TargetType, rel.SelectField, indentLevel+2)
		fieldsBuilder.WriteString(fmt.Sprintf(`,
%s    %s: %s`, innerIndent, nextHopKey, nextHopSelect))
	}

	fieldsBuilder.WriteString(fmt.Sprintf(`
%s}`, innerIndent))

	return fmt.Sprintf(`%s%s: (SELECT VALUE {%s
%s} FROM %s WHERE %s = $parent.%s
%s  AND `+tplRelLivenessFilter+`
%s  AND `+tplLivenessFilterRef+`)`,
		indent, keyName,
		fieldsBuilder.String(),
		indent, relationTable, rel.WhereField, parentField,
		indent, relationLivenessTable,
		indent, targetLivenessTable, targetLivenessIDField, rel.SelectField)
}

// buildWhereClause 构建 WHERE 子句
func (b *SurrealQueryBuilder) buildWhereClause() string {
	var conditions []string

	if len(b.request.SourceInfo) > 0 {
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
			v := b.request.SourceInfo[k]
			conditions = append(conditions, fmt.Sprintf("%s = '%s'", k, escapeSurrealString(v)))
		}
	}

	if len(b.request.SourceExpandInfo) > 0 {
		keys := make([]string, 0, len(b.request.SourceExpandInfo))
		for k := range b.request.SourceExpandInfo {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, k := range keys {
			if !isSafeSurrealField(k) {
				// expand 字段来自调用方可选过滤，不像 SourceInfo 那样强制主键；
				// 因此这里只允许简单字段名，避免把表达式片段拼进 SurrealQL。
				continue
			}
			v := b.request.SourceExpandInfo[k]
			conditions = append(conditions, fmt.Sprintf("%s = '%s'", k, escapeSurrealString(v)))
		}
	}

	livenessTable := GetLivenessRecordTableName(b.request.SourceType)
	livenessIDField := GetLivenessIDField(b.request.SourceType)
	conditions = append(conditions, fmt.Sprintf(tplLivenessFilter, livenessTable, livenessIDField))

	return "WHERE " + strings.Join(conditions, "\n  AND ")
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
	for _, r := range field {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return false
	}
	return true
}
