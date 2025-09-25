// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package structured

import (
	"context"

	"github.com/prometheus/prometheus/promql/parser"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/errno"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query"
)

// getExpressionByParam
func getExpressionByParam(param any) (parser.Expr, error) {
	switch t := param.(type) {
	case string:
		return &parser.StringLiteral{Val: param.(string)}, nil
	case float64:
		return &parser.NumberLiteral{Val: param.(float64)}, nil
	case json.Number:
		num, err := param.(json.Number).Float64()
		if err != nil {
			return nil, err
		}
		return &parser.NumberLiteral{Val: num}, nil
	case int:
		return &parser.NumberLiteral{Val: float64(param.(int))}, nil
	default:
		codedErr := errno.ErrDataProcessFailed().
			WithComponent("结构化查询").
			WithOperation("参数类型处理").
			WithContext("未知类型", t).
			WithSolution("检查vArg参数类型是否支持")
		log.ErrorWithCodef(context.TODO(), codedErr)
		return nil, ErrExprNotAllow
	}
}

// 拼接一个参数列表
func combineExprList(position int, expr parser.Expr, exprParams []any) ([]parser.Expr, error) {
	params := make([]parser.Expr, 0)
	// 判断是否需要追加参数
	if len(exprParams) != 0 {
		for _, vArg := range exprParams {
			expression, err := getExpressionByParam(vArg)
			if err != nil {
				return nil, err
			}
			params = append(params, expression)
		}
	}

	results := make([]parser.Expr, len(params)+1)
	results[position] = expr

	// 将expr插队到exprs中
	for index := range results {
		if index < position {
			results[index] = params[index]
		}
		if index == position {
			results[index] = expr
		}
		if index > position {
			results[index] = params[index-1]
		}
	}

	return results, nil
}

// containElement  判断一个切片中是否包含某个元素
func containElement(slice []string, element string) bool {
	if element == "" || len(slice) == 0 {
		return false
	}
	for _, item := range slice {
		if item == element {
			return true
		}
	}
	return false
}

// judgeFilter 判断 filter 是否符合合并压缩的条件
// 目前仅支持 tsDB 的每一个 filter 的 key 均为一致，并且 key 的长度为 2 的情况
func judgeFilter(filters []query.Filter) (bool, []string) {
	// 如果只有1个条件或者更少的条件则无需合并
	if len(filters) < 2 {
		return false, nil
	}

	tKeys := make(map[string]struct{})
	values := make([]string, 0)
	for idx, filter := range filters {
		if len(filter) != 2 {
			return false, nil
		}
		if idx == 0 {
			for k := range filter {
				tKeys[k] = struct{}{}
				values = append(values, k)
			}
		} else {
			// 如果存在 key 不一致的情况，直接退出
			for k := range filter {
				if _, ok := tKeys[k]; !ok {
					return false, nil
				}
			}
		}
	}
	return true, values
}

// compressFilterCondition 对 filterCondition 压缩，减少后续产出的 vm 查询语句的长度
func compressFilterCondition(tKeys []string, filters []query.Filter) [][]ConditionField {
	// 分别取出两个 key ，并且通过 2 个 key 的值的个数来判断选择拿哪个key进行分组
	key1, key2 := tKeys[0], tKeys[1]
	var (
		tArr1 = make([]string, 0)
		tArr2 = make([]string, 0)
	)
	for _, filter := range filters {
		for k, v := range filter {
			if v == "" {
				continue
			}
			if k == tKeys[0] && !containElement(tArr1, v) {
				tArr1 = append(tArr1, v)
			}
			if k == tKeys[1] && !containElement(tArr2, v) {
				tArr2 = append(tArr2, v)
			}
		}
	}
	// 根据两个 key 的长度来进行判断 选用哪个 key 作为分组依据
	groupKey, subKey := key1, key2
	if len(tArr2) < len(tArr1) {
		groupKey, subKey = key2, key1
	}
	// 开始对所有的内容进行分组，生成一个压缩后的字典
	// 压缩字典内容如下  key => groupKey 对应的值, value 为字符串列表，列表中的元素 为 subKey 对应的值（去重过后的）
	compressMap := make(map[string][]string)
	for _, filter := range filters {
		_, ok := compressMap[filter[groupKey]]
		if !ok {
			if filter[subKey] != "" {
				compressMap[filter[groupKey]] = []string{filter[subKey]}
			}
		} else {
			if !containElement(compressMap[filter[groupKey]], filter[subKey]) && filter[subKey] != "" {
				compressMap[filter[groupKey]] = append(compressMap[filter[groupKey]], filter[subKey])
			}
		}
	}
	// 组装好的compressMap 结构如下 {groupValue:[subVal1,subVal2]}
	// 开始组装 condition
	filterConditions := make([][]ConditionField, 0)
	for k, v := range compressMap {
		cond := make([]ConditionField, 0, 2)
		cond = []ConditionField{{
			DimensionName: groupKey,
			Value:         []string{k},
			Operator:      Contains,
		}, {
			DimensionName: subKey,
			Value:         v,
			Operator:      Contains,
		}}
		filterConditions = append(filterConditions, cond)
	}
	return filterConditions
}
