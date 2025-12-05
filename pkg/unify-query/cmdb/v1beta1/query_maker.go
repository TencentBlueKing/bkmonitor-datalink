// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta1

import (
	"fmt"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

const ascii = 97 // a

type QueryFactory struct {
	Path []string

	Step  string
	Start string
	End   string

	Source        cmdb.Resource
	IndexMatcher  cmdb.Matcher
	ExpandMatcher cmdb.Matcher

	Target     cmdb.Resource
	ExpandShow bool

	index       int
	metricMerge string
}

func (q *QueryFactory) pathParser(p []string) (cmdb.Path, error) {
	if len(p) < 2 {
		return nil, fmt.Errorf("path format is wrong %s", p)
	}

	path := make(cmdb.Path, 0, len(p)-1)
	for i := 0; i < len(p)-1; i++ {
		v := [2]cmdb.Resource{cmdb.Resource(p[i]), cmdb.Resource(p[i+1])}
		path = append(path, cmdb.Relation{
			V: v,
		})
	}
	return path, nil
}

func (q *QueryFactory) matcherToConditionFields(matcher cmdb.Matcher, op string) (conditionFields []structured.ConditionField) {
	for k, v := range matcher {
		if k == "" {
			continue
		}
		conditionFields = append(conditionFields, structured.ConditionField{
			DimensionName: k,
			Value:         []string{v},
			Operator:      op,
		})
	}
	return conditionFields
}

func (q *QueryFactory) MakeQueryTs() (*structured.QueryTs, error) {
	var queryList []*structured.Query

	if len(q.Path) > 1 {
		cmdbPath, err := q.pathParser(q.Path)
		if err != nil {
			return nil, err
		}
		for _, node := range cmdbPath {
			queries, err := q.buildRelationQueries(node)
			if err != nil {
				return nil, err
			}

			queryList = append(queryList, queries...)
		}
	}

	if q.ExpandShow {
		infoIndex := ResourcesInfo(q.Target)
		targetIndex := ResourcesIndex(q.Target)

		if len(infoIndex) == 0 {
			return nil, fmt.Errorf("该资源未配置 info 扩展数据")
		}

		ref := string(rune(ascii + q.index))
		queries, err := q.buildInfoQuery(q.Target, q.IndexMatcher, q.ExpandMatcher)
		if err != nil {
			return nil, err
		}

		queryList = append(queryList, queries)

		allIndex := append([]string{}, targetIndex...)
		allIndex = append(allIndex, infoIndex...)

		if q.metricMerge == "" {
			q.metricMerge = fmt.Sprintf("count(%s) by (%s)", ref, strings.Join(allIndex, ","))
		} else {
			q.metricMerge = fmt.Sprintf("(%s) * on(%s) group_left(%s) %s", q.metricMerge, strings.Join(targetIndex, ","), strings.Join(infoIndex, ","), ref)
		}
	}

	return &structured.QueryTs{
		QueryList:    queryList,
		MetricMerge:  q.metricMerge,
		Start:        q.Start,
		End:          q.End,
		Step:         q.Step,
		NotTimeAlign: true,
	}, nil
}

func (q *QueryFactory) buildConditionFields(allIndex []string, indexMatcher, expandMatcher cmdb.Matcher) structured.Conditions {
	eqMatcher := make(cmdb.Matcher)
	neMatcher := make(cmdb.Matcher)
	for _, i := range allIndex {
		if v, ok := indexMatcher[i]; ok {
			eqMatcher[i] = v
		} else {
			neMatcher[i] = ""
		}
	}

	for k, v := range expandMatcher {
		eqMatcher[k] = v
	}

	fieldList := make([]structured.ConditionField, 0, len(eqMatcher)+len(neMatcher))
	fieldList = append(fieldList, q.matcherToConditionFields(eqMatcher, structured.ConditionEqual)...)
	fieldList = append(fieldList, q.matcherToConditionFields(neMatcher, structured.ConditionNotEqual)...)

	if len(fieldList) == 0 {
		return structured.Conditions{}
	}

	var conditionList []string
	for i := 1; i < len(fieldList); i++ {
		conditionList = append(conditionList, structured.ConditionAnd)
	}

	return structured.Conditions{
		FieldList:     fieldList,
		ConditionList: conditionList,
	}
}

func (q *QueryFactory) buildInfoQuery(resource cmdb.Resource, indexMatcher, expandMatcher cmdb.Matcher) (query *structured.Query, err error) {
	if resource == "" {
		return query, err
	}

	allIndex := ResourcesIndex(resource)

	field := fmt.Sprintf("%s_info_relation", resource)
	ref := string(rune(ascii + q.index))
	query = &structured.Query{
		FieldName:     field,
		ReferenceName: ref,
		Step:          q.Step,
	}
	if q.Step != "" {
		query.TimeAggregation = structured.TimeAggregation{
			Function: structured.CountOT,
			Window:   structured.Window(q.Step),
		}
	}

	query.Conditions = q.buildConditionFields(allIndex, indexMatcher, expandMatcher)
	q.index++
	return query, err
}

func (q *QueryFactory) buildRelationQueries(path cmdb.Relation) (queries []*structured.Query, err error) {
	source, target, metric := path.Info()
	if metric == "" {
		return queries, err
	}
	ref := string(rune(ascii + q.index))

	query := &structured.Query{
		FieldName:     metric,
		ReferenceName: ref,
		Step:          q.Step,
	}
	if q.Step != "" {
		query.TimeAggregation = structured.TimeAggregation{
			Function: structured.CountOT,
			Window:   structured.Window(q.Step),
		}
	}

	query.Conditions = q.buildConditionFields(ResourcesIndex(source, target), q.IndexMatcher, nil)
	queries = append(queries, query)
	q.index++

	targetIndex := strings.Join(ResourcesIndex(target), ",")
	sourceIndex := strings.Join(ResourcesIndex(source), ",")

	// 拼接入 info 查询扩展
	if source == q.Source && len(q.ExpandMatcher) > 0 {
		infoRef := string(rune(ascii + q.index))
		infoQuery, infoErr := q.buildInfoQuery(source, q.IndexMatcher, q.ExpandMatcher)
		if infoErr != nil {
			err = infoErr
			return queries, err
		}
		queries = append(queries, infoQuery)
		ref = fmt.Sprintf("%s * on(%s) group_left() (%s)", ref, sourceIndex, infoRef)
	}

	if q.metricMerge == "" {
		q.metricMerge = fmt.Sprintf("count(%s) by (%s)", ref, targetIndex)
	} else {
		q.metricMerge = fmt.Sprintf("count(%s * on(%s) group_left() (%s)) by (%s)", ref, sourceIndex, q.metricMerge, targetIndex)
	}

	return queries, err
}
