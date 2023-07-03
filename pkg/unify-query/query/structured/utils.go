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
	"encoding/json"

	"github.com/prometheus/prometheus/promql/parser"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// getExpressionByParam
func getExpressionByParam(param interface{}) (parser.Expr, error) {
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
		log.Errorf(context.TODO(), "unknown vArg type:%#v", t)
		return nil, ErrExprNotAllow
	}
}

// 拼接一个参数列表
func combineExprList(position int, expr parser.Expr, exprParams []interface{}) ([]parser.Expr, error) {
	var params = make([]parser.Expr, 0)
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
