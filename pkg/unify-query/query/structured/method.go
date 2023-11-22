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
	"strings"

	"github.com/prometheus/prometheus/promql/parser"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

var AggregateMap = map[string]parser.ItemType{
	SumAggName:         parser.SUM,
	MinAggName:         parser.MIN,
	MaxAggName:         parser.MAX,
	AVGAggName:         parser.AVG,
	MeanAggName:        parser.AVG,
	GroupAggName:       parser.GROUP,
	StddevAggName:      parser.STDDEV,
	StdvarAggName:      parser.STDVAR,
	CountAggName:       parser.COUNT,
	CountValuesAggName: parser.COUNT_VALUES,
	BottomKAggName:     parser.BOTTOMK,
	TopkAggName:        parser.TOPK,
	QuantileAggName:    parser.QUANTILE,
}

// 参数组合
type Args map[string]string

// 聚合方法
type AggregateMethod struct {
	// Method 聚合方法
	Method string `json:"method" example:"mean"`
	// Without
	Without bool `json:"without" example:false`
	// Dimensions 聚合维度
	Dimensions Dimensions `json:"dimensions" example:"bk_target_ip,bk_target_cloud_id"`
	// Position 函数参数位置，结合 VArgsList 一起使用，类似 topk, histogram_quantile 需要用到
	Position int `json:"position" swaggerignore:"true"`
	// ArgsList 弃用参数
	ArgsList Args `json:"args_list" swaggerignore:"true"`
	// VArgsList 函数参数，结合 Position 一起使用，类似 topk, histogram_quantile 需要用到
	VArgsList []interface{} `json:"vargs_list" swaggerignore:"true"`
}

// ToProm: 将结果返回为一个promql的聚合表达式，但是注意：此时的Expr/Grouping为空，需要在外部进行补充
func (m *AggregateMethod) ToProm(expr parser.Expr) (parser.Expr, error) {

	// 参数在聚合集合里，就用聚合方法
	if method, ok := AggregateMap[strings.ToLower(m.Method)]; ok {
		log.Debugf(context.TODO(), "method->[%s] is aggregate method, will make to AggregateExpr", m.Method)
		var result = new(parser.AggregateExpr)
		result.Expr = expr
		result.Op = method
		if len(m.VArgsList) > 0 {
			// 只取第一个参数
			expression, err := getExpressionByParam(m.VArgsList[0])
			if err != nil {
				return nil, err
			}
			result.Param = expression
		}

		result.Grouping = m.Dimensions
		result.Without = m.Without
		return result, nil
	}

	// 否则视为普通函数调用
	var result = new(parser.Call)
	log.Debugf(context.TODO(), "method->[%s] is call method, will make to call expr.", m.Method)
	result.Func = &parser.Function{
		Name:       m.Method,
		ArgTypes:   []parser.ValueType{parser.ValueTypeMatrix},
		ReturnType: parser.ValueTypeVector,
	}
	params, err := combineExprList(m.Position, expr, m.VArgsList)
	if err != nil {
		return nil, err
	}
	result.Args = params

	return result, nil
}
