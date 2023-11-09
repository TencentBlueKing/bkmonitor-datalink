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
	"time"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/promql/parser"
)

// TimeAggregation
type TimeAggregation struct {
	// Function 时间聚合方法
	Function string `json:"function" example:"avg_over_time"`
	// Window 聚合周期
	Window Window `json:"window" example:"60s"`
	// NodeIndex 聚合函数的位置，用于还原 promql 的定位
	NodeIndex int `json:"node_index"`
	// Position 函数参数位置，结合 VArgsList 一起使用，类似 topk, histogram_quantile 需要用到
	Position int `json:"position" swaggerignore:"true"`
	// VargsList 函数参数位置，结合 Position 一起使用，类似 topk, histogram_quantile 需要用到
	VargsList []interface{} `json:"vargs_list" swaggerignore:"true"`
	// IsSubQuery 判断是否为子查询
	IsSubQuery bool `json:"is_sub_query"`
	// Step 子查询区间 step
	Step string `json:"step" swaggerignore:"true"`
}

// ToProm
func (m TimeAggregation) ToProm(expr parser.Expr) (*parser.Call, error) {
	var (
		function = new(parser.Call)
		err      error

		window  time.Duration
		stepDur model.Duration
	)

	window, err = m.Window.ToTime()
	if err != nil {
		return nil, err
	}

	if m.IsSubQuery {
		if m.Step != "" {
			stepDur, err = model.ParseDuration(m.Step)
			if err != nil {
				return nil, err
			}
		}

		expr = &parser.SubqueryExpr{
			Expr:  expr,
			Range: window,
			Step:  time.Duration(stepDur),
		}
	} else {
		expr = &parser.MatrixSelector{
			VectorSelector: expr,
			Range:          window,
		}
	}

	function.Func = &parser.Function{
		Name:       m.Function,
		ArgTypes:   []parser.ValueType{parser.ValueTypeMatrix},
		ReturnType: parser.ValueTypeVector,
	}

	function.Args, err = combineExprList(m.Position, expr, m.VargsList)
	if err != nil {
		return nil, err
	}
	return function, nil
}
