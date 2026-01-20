// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v2

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

	fieldSourceID   = "source_id"
	fieldTargetID   = "target_id"
	fieldRelationID = "relation_id"

	// SQL 子查询模板
	tplLivenessSelect    = "(SELECT * FROM %s WHERE %s = $parent.id AND period_end >= $start AND period_start <= $end)"
	tplLivenessSelectRef = "(SELECT * FROM %s WHERE %s = $parent.%s AND period_end >= $start AND period_start <= $end)"
	tplRelLivenessSelect = "(SELECT * FROM %s WHERE relation_id = $parent.id AND period_end >= $start AND period_start <= $end)"
	tplLivenessFilter    = "(SELECT count() FROM only %s WHERE %s = $parent.id AND $end >= period_start AND $start <= period_end GROUP ALL) > 0"
	tplRelLivenessFilter = "(SELECT count() FROM only %s WHERE relation_id = $parent.id AND $end >= period_start AND $start <= period_end GROUP ALL) > 0"
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
	request    *QueryRequest
	pathFinder *PathFinder
}

// NewSurrealQueryBuilder 创建查询构建器
func NewSurrealQueryBuilder(request *QueryRequest) *SurrealQueryBuilder {
	request.Normalize()
	pf := NewPathFinder(
		WithAllowedCategories(request.AllowedRelationTypes...),
		WithDynamicDirection(request.DynamicRelationDirection),
		WithMaxHops(request.MaxHops),
	)
	return &SurrealQueryBuilder{request: request, pathFinder: pf}
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
	start, end := b.request.GetQueryRange()
	return fmt.Sprintf(`LET $timestamp = %d;
LET $look_back_delta = %d;
LET $start = %d;
LET $end = %d;`,
		b.request.Timestamp,
		b.request.LookBackDelta,
		start,
		end)
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
	primaryKeys := GetResourcePrimaryKeys(sourceType)
	livenessTable := GetLivenessRecordTableName(sourceType)
	livenessIDField := GetLivenessIDField(sourceType)

	return fmt.Sprintf(`{
        entity_type: meta::tb(id),
        entity_id: <string>id,
        entity_data: { %s },
        created_at: created_at,
        updated_at: updated_at,
        liveness: `+tplLivenessSelect+`
    }`,
		buildEntityDataFields(primaryKeys, ""),
		livenessTable,
		livenessIDField)
}

// buildHopSelect 构建指定跳数的 SELECT 结构
func (b *SurrealQueryBuilder) buildHopSelect(hop int, currentType ResourceType) string {
	if hop > b.request.MaxHops {
		return "{}"
	}

	relations := b.getRelationsForType(currentType)
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
	TargetField string       // 目标字段 (source_id 或 target_id)
	TargetType  ResourceType // 目标资源类型
	WhereField  string       // WHERE 子句中用于匹配当前实体的字段
	SelectField string       // SELECT 中获取目标实体的字段
}

// getRelationsForType 获取指定资源类型的所有可用关系查询
func (b *SurrealQueryBuilder) getRelationsForType(resourceType ResourceType) []*RelationQueryInfo {
	return b.pathFinder.getRelationsForType(resourceType)
}

// buildRelationQuery 构建单个关系的查询
func (b *SurrealQueryBuilder) buildRelationQuery(hop int, _ ResourceType, rel *RelationQueryInfo) string {
	relationType := rel.Schema.RelationType
	relationTable := string(relationType)
	relationLivenessTable := GetRelationLivenessRecordTableName(relationType)
	targetLivenessTable := GetLivenessRecordTableName(rel.TargetType)
	targetLivenessIDField := GetLivenessIDField(rel.TargetType)
	keyName := relationTable + rel.KeySuffix
	targetPrimaryKeys := GetResourcePrimaryKeys(rel.TargetType)

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
            relation_id: <string>id,
            relation_liveness: `+tplRelLivenessSelect+`,
            target: {
                entity_type: '%s',
                entity_id: <string>%s,
                entity_data: { %s },
                liveness: `+tplLivenessSelectRef,
		relationLivenessTable,
		rel.TargetType,
		rel.SelectField,
		buildEntityDataFields(targetPrimaryKeys, rel.SelectField),
		targetLivenessTable,
		targetLivenessIDField,
		rel.SelectField))

	if hop < b.request.MaxHops {
		nextHopKey := fmt.Sprintf("hop%d", hop+1)
		nextHopSelect := b.buildNestedHopSelect(hop+1, rel.TargetType, rel.SelectField)
		fieldsBuilder.WriteString(fmt.Sprintf(`,
                %s: %s`, nextHopKey, nextHopSelect))
	}

	fieldsBuilder.WriteString(`
            }`)

	return fmt.Sprintf(sqlIndent2+`%s: (SELECT {%s
        } FROM %s WHERE %s = $parent.id
          AND `+tplRelLivenessFilter+`)`,
		keyName,
		fieldsBuilder.String(),
		relationTable,
		rel.WhereField,
		relationLivenessTable)
}

// buildNestedHopSelect 构建嵌套在 target 内的下一跳查询
func (b *SurrealQueryBuilder) buildNestedHopSelect(hop int, currentType ResourceType, parentField string) string {
	if hop > b.request.MaxHops {
		return "{}"
	}

	relations := b.getRelationsForType(currentType)
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
	targetPrimaryKeys := GetResourcePrimaryKeys(rel.TargetType)

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
                        relation_id: <string>id,
                        relation_liveness: `+tplRelLivenessSelect+`,
                        target: {
                            entity_type: '%s',
                            entity_id: <string>%s,
                            entity_data: { %s },
                            liveness: `+tplLivenessSelectRef,
		relationLivenessTable,
		rel.TargetType,
		rel.SelectField,
		buildEntityDataFields(targetPrimaryKeys, rel.SelectField),
		targetLivenessTable,
		targetLivenessIDField,
		rel.SelectField))

	if hop < b.request.MaxHops {
		nextHopKey := fmt.Sprintf("hop%d", hop+1)
		nextHopSelect := b.buildDeeperNestedHopSelect(hop+1, rel.TargetType, rel.SelectField, 4)
		fieldsBuilder.WriteString(fmt.Sprintf(`,
                            %s: %s`, nextHopKey, nextHopSelect))
	}

	fieldsBuilder.WriteString(`
                        }`)

	return fmt.Sprintf(sqlIndent5+`%s: (SELECT {%s
                    } FROM %s WHERE %s = $parent.%s
                      AND `+tplRelLivenessFilter+`)`,
		keyName,
		fieldsBuilder.String(),
		relationTable,
		rel.WhereField,
		parentField,
		relationLivenessTable)
}

// buildDeeperNestedHopSelect 构建更深层嵌套的 hop（hop3+）
func (b *SurrealQueryBuilder) buildDeeperNestedHopSelect(hop int, currentType ResourceType, parentField string, indentLevel int) string {
	if hop > b.request.MaxHops {
		return "{}"
	}

	relations := b.getRelationsForType(currentType)
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
	targetPrimaryKeys := GetResourcePrimaryKeys(rel.TargetType)

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
%srelation_id: <string>id,
%srelation_liveness: `+tplRelLivenessSelect+`,
%starget: {
%s    entity_type: '%s',
%s    entity_id: <string>%s,
%s    entity_data: { %s },
%s    liveness: `+tplLivenessSelectRef,
		innerIndent,
		innerIndent, relationLivenessTable,
		innerIndent,
		innerIndent, rel.TargetType,
		innerIndent, rel.SelectField,
		innerIndent, buildEntityDataFields(targetPrimaryKeys, rel.SelectField),
		innerIndent, targetLivenessTable, targetLivenessIDField, rel.SelectField))

	if hop < b.request.MaxHops {
		nextHopKey := fmt.Sprintf("hop%d", hop+1)
		nextHopSelect := b.buildDeeperNestedHopSelect(hop+1, rel.TargetType, rel.SelectField, indentLevel+2)
		fieldsBuilder.WriteString(fmt.Sprintf(`,
%s    %s: %s`, innerIndent, nextHopKey, nextHopSelect))
	}

	fieldsBuilder.WriteString(fmt.Sprintf(`
%s}`, innerIndent))

	return fmt.Sprintf(`%s%s: (SELECT {%s
%s} FROM %s WHERE %s = $parent.%s
%s  AND `+tplRelLivenessFilter+`)`,
		indent, keyName,
		fieldsBuilder.String(),
		indent, relationTable, rel.WhereField, parentField,
		indent, relationLivenessTable)
}

// buildWhereClause 构建 WHERE 子句
func (b *SurrealQueryBuilder) buildWhereClause() string {
	var conditions []string

	if len(b.request.SourceInfo) > 0 {
		keys := make([]string, 0, len(b.request.SourceInfo))
		for k := range b.request.SourceInfo {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, k := range keys {
			v := b.request.SourceInfo[k]
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
