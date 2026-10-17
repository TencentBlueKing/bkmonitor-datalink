// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package policy

import (
	"context"
	"errors"
	"slices"
	"sort"
)

// CandidateReader 读取一个具有明确类型和映射路径的字段；可返回多个等级的值并集。
// 缺失字段返回 Present=false；读取失败会保守保留依赖该字段的规则，不能误作未命中。
type CandidateReader func(context.Context, string, FieldMapping) (Value, error)

type candidateField struct {
	name    string
	mapping FieldMapping
}

type candidateAnchor struct {
	field candidateField
	value string
}

type candidatePostings struct {
	values map[string][]int
	ranks  []int
}

// candidateIndex 只索引必要的正向等值条件；返回的超集仍须经原匹配器裁决。
// 策略和 posting 均随 Catalog 快照一次构建，查询不遍历全部策略。
type candidateIndex struct {
	policies []FrozenPolicy
	fields   map[candidateField]*candidatePostings
	fallback []int
}

func newCandidateIndex(policies []FrozenPolicy) *candidateIndex {
	index := &candidateIndex{policies: policies, fields: make(map[candidateField]*candidatePostings)}
	for rank, item := range policies {
		compiled := item.Compiled
		// 停用只能通过新发布版本改变；保留完整目录供旧绑定读取，查询索引无需登记它。
		if item.Release.Deleted || (compiled != nil && !compiled.Common.Enabled) {
			continue
		}
		if compiled == nil {
			index.fallback = append(index.fallback, rank)
			continue
		}
		// 1. 条件组按 OR 覆盖；依赖屏蔽同时覆盖注册主和匹配子的入口。
		expressions := compiled.Conditions
		if compiled.Rely != nil {
			expressions = append(slices.Clone(expressions), compiled.Rely)
		}
		anchors := []candidateAnchor{}
		universal := len(expressions) == 0
		for _, expression := range expressions {
			cover := expression.candidateCover(expression.root, compiled.Common.FieldMappings)
			if len(cover) == 0 {
				universal = true
				break
			}
			anchors = append(anchors, cover...)
		}
		if universal {
			index.fallback = append(index.fallback, rank)
			continue
		}
		// 2. 同一策略在同一字段/值只登记一次，保留原 Catalog 优先级。
		seen := make(map[candidateAnchor]bool, len(anchors))
		seenFields := make(map[candidateField]bool)
		for _, anchor := range anchors {
			if seen[anchor] {
				continue
			}
			seen[anchor] = true
			postings := index.fields[anchor.field]
			if postings == nil {
				postings = &candidatePostings{values: make(map[string][]int)}
				index.fields[anchor.field] = postings
			}
			postings.values[anchor.value] = append(postings.values[anchor.value], rank)
			if !seenFields[anchor.field] {
				postings.ranks = append(postings.ranks, rank)
				seenFields[anchor.field] = true
			}
		}
	}
	return index
}

// candidateCover 用一个必要锚点集合覆盖布尔树，不展开 DNF。
// AND 任选一个有锚点的分支；OR 必须两侧都有锚点，否则整条规则保守兜底。
func (e *Expression) candidateCover(node *booleanNode, mappings map[string]FieldMapping) []candidateAnchor {
	if e == nil || node == nil {
		return nil
	}
	if node.op == "and" || node.op == "or" {
		left, right := e.candidateCover(node.left, mappings), e.candidateCover(node.right, mappings)
		if node.op == "or" {
			if len(left) == 0 || len(right) == 0 {
				return nil
			}
			return append(left, right...)
		}
		if len(left) == 0 || (len(right) != 0 && len(right) < len(left)) {
			return right
		}
		return left
	}
	clause := e.clauses[node.id]
	if clause.negative || len(clause.reference) != 0 || clause.condition.Relation != "" || (clause.operator != "term" && clause.operator != "terms") || (clause.kind == FieldText && clause.operator == "terms") {
		return nil
	}
	mapping := mappings[clause.condition.Field]
	mapping.Kind = clause.kind
	field := candidateField{name: clause.condition.Field, mapping: mapping}
	anchors := make([]candidateAnchor, 0, len(clause.values))
	for _, value := range clause.values {
		anchors = append(anchors, candidateAnchor{field: field, value: value})
	}
	return anchors
}

func (index *candidateIndex) selectPolicies(ctx context.Context, read CandidateReader) ([]FrozenPolicy, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if read == nil {
		return slices.Clone(index.policies), nil
	}
	selected := make(map[int]bool, len(index.fallback))
	add := func(ranks []int) {
		for _, rank := range ranks {
			selected[rank] = true
		}
	}
	add(index.fallback)
	for field, postings := range index.fields {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		value, err := read(ctx, field.name, field.mapping)
		if errors.Is(err, ErrAccess) {
			return nil, err
		}
		if err != nil {
			add(postings.ranks)
			continue
		}
		if !value.Present || value.Data == nil {
			continue
		}
		values, nodes := []any{}, 0
		if scalarValues(value.Data, &values, 0, &nodes) != nil {
			add(postings.ranks)
			continue
		}
		for _, scalar := range values {
			text, err := queryScalar(field.mapping.Kind, scalar)
			if err != nil {
				add(postings.ranks)
				break
			}
			add(postings.values[text])
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// 3. 只排序相关候选，避免为恢复优先级重新遍历整份策略列表。
	ranks := make([]int, 0, len(selected))
	for rank := range selected {
		ranks = append(ranks, rank)
	}
	sort.Ints(ranks)
	result := make([]FrozenPolicy, 0, len(ranks))
	for _, rank := range ranks {
		result = append(result, index.policies[rank])
	}
	return result, nil
}
