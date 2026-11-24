// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package uq

import (
	"errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

const (
	connectorAnd = "and"
	connectorOr  = "or"
	// operatorRegexp and operatorNotRegexp are the provider's structured
	// operators for a regular expression a label matches and does not;
	// against VictoriaMetrics it renders them =~ and !~.
	operatorRegexp    = "req"
	operatorNotRegexp = "nreq"
)

// withInputSlice is a clause's conditions with slice's put into every group
// of them, as the provider reads them: the condition list split into groups
// on "or", "and" within a group (unify-query query/structured, Conditions
// .AnalysisConditions). Each group gets every slice condition ANDed after its
// own, the placement andIntoEveryGroup gives the platform's filters in the
// compiler. Put after the last group only, a series another group matches
// would match outside the slice as well and land in every slice.
//
// A clause without conditions has the slice's alone. A clause of several
// conditions and no connectors is one the provider refuses as it is; joining
// them here would send a query that means something the strategy never did,
// so it is refused.
func withInputSlice(source conditions, slice execution.InputSlice) (conditions, error) {
	if len(source.Fields) > 1 && len(source.Connectors)+1 != len(source.Fields) {
		return conditions{}, errors.New("alarmd access uq: a sliced clause's conditions are not joined by connectors")
	}
	filters := make([]conditionField, 0, len(slice.Conditions))
	for _, condition := range slice.Conditions {
		operator := operatorRegexp
		if condition.Negated {
			operator = operatorNotRegexp
		}
		filters = append(filters, conditionField{Field: condition.Dimension, Operator: operator, Values: []string{condition.Pattern}})
	}
	groups := make([][]conditionField, 0, 1)
	current := make([]conditionField, 0, len(source.Fields))
	for index, field := range source.Fields {
		if index > 0 {
			switch source.Connectors[index-1] {
			case connectorAnd:
			case connectorOr:
				groups, current = append(groups, current), nil
			default:
				return conditions{}, errors.New("alarmd access uq: a sliced clause's conditions are joined by an unknown connector")
			}
		}
		current = append(current, field)
	}
	groups = append(groups, current)
	result := conditions{Fields: make([]conditionField, 0, len(source.Fields)+len(groups)*len(filters))}
	for index, group := range groups {
		for position, field := range append(append([]conditionField(nil), group...), filters...) {
			if len(result.Fields) > 0 {
				connector := connectorAnd
				if index > 0 && position == 0 {
					connector = connectorOr
				}
				result.Connectors = append(result.Connectors, connector)
			}
			result.Fields = append(result.Fields, field)
		}
	}
	return result, nil
}
