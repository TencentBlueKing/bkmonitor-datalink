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
	"fmt"

	"github.com/pkg/errors"
	"github.com/prometheus/prometheus/promql/parser"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// 存入到context的key值，两者的数据结构都为 map[metricName<string>]<InfoType>
// Conditions的InfoType 为 [][]Conditions
// OffSet的InfoType 为 *OffSetInfo
const (
	PromConditionKeyName = "_bkmonitorv3.unify-query.promql.conditionKey"
	PromOffsetKeyName    = "_bkmonitorv3.unify-query.promql.OffsetKey"
)

const (
	SumAggName         = "sum"
	MinAggName         = "min"
	MaxAggName         = "max"
	AVGAggName         = "avg"
	MeanAggName        = "mean"
	GroupAggName       = "group"
	StddevAggName      = "stddev"
	StdvarAggName      = "stdvar"
	CountAggName       = "count"
	CountValuesAggName = "count_values"
	BottomKAggName     = "bottomk"
	TopkAggName        = "topk"
	QuantileAggName    = "quantile"
)

// promExpr: 包含一个prom表达式，同时还包含了一个额外的ctx，用于描述：
// 1. 过滤条件的or关系，由于promql的过滤条件不支持，所以需要额外传递
// 2. limit、slimit、offset和soffset的关系，因为promql不支持，所以也需要额外传递
type PromExpr struct {
	Expr       parser.Expr
	Dimensions []string
	ctx        context.Context
}

// GetExpr
func (p *PromExpr) GetExpr() parser.Expr {
	return p.Expr
}

// GetCtx
func (p *PromExpr) GetCtx() context.Context {
	return p.ctx
}

// handleVectorExpr
func handleVectorExpr(m map[string]*PromExpr, e parser.Expr) (parser.Expr, []string, error) {
	var (
		name      string
		grouping  []string
		ok        bool
		expr      *parser.VectorSelector
		promExpr  *PromExpr
		finalExpr parser.Expr
	)

	expr, ok = e.(*parser.VectorSelector)
	if !ok {
		return nil, nil, fmt.Errorf("type is error: %+v", e)
	}
	name = expr.Name
	if promExpr, ok = m[name]; !ok {
		return nil, nil, ErrMetricMissing
	}
	finalExpr = promExpr.Expr

	if promExpr.Dimensions == nil {
		// 解析真实的表达式,获取group信息
		if aggre, ok := finalExpr.(*parser.AggregateExpr); ok {
			grouping = aggre.Grouping
		}
	} else {
		grouping = promExpr.Dimensions
	}

	log.Debugf(context.TODO(), "exp->[%s] transfer to result->[%s] with grouping->[%s]", e, finalExpr, grouping)
	return finalExpr, grouping, nil
}

func handlerUnaryExpr(m map[string]*PromExpr, e parser.Expr) (parser.Expr, []string, error) {
	var (
		expr *parser.UnaryExpr
		err  error
		ok   bool
	)
	expr, ok = e.(*parser.UnaryExpr)
	if !ok {
		err = fmt.Errorf("error type %+v", e)
		return nil, nil, err
	}

	vector, _, err := handleExpr(m, expr.Expr)
	if err != nil {
		return nil, nil, err
	}
	expr.Expr = vector
	return expr, nil, err
}

// handleBinaryExpr
func handleBinaryExpr(m map[string]*PromExpr, e parser.Expr) (parser.Expr, []string, error) {
	var (
		expr                  *parser.BinaryExpr
		finalGroup            []string
		leftGroup, rightGroup []string
		err                   error
		leftHasNumber         bool
		rightHasNumber        bool
		ok                    bool
	)
	expr, ok = e.(*parser.BinaryExpr)
	if !ok {
		err = fmt.Errorf("error type %+v", e)
		return nil, nil, err
	}

	if _, ok = expr.LHS.(*parser.NumberLiteral); ok {
		leftHasNumber = true
	}
	if _, ok = expr.RHS.(*parser.NumberLiteral); ok {
		rightHasNumber = true
	}
	expr.LHS, leftGroup, err = handleExpr(m, expr.LHS)
	if err != nil {
		return nil, nil, err
	}

	expr.RHS, rightGroup, err = handleExpr(m, expr.RHS)
	if err != nil {
		return nil, nil, err
	}

	if leftHasNumber && rightHasNumber {
		log.Debugf(context.TODO(), "expr->[%s] both side is number, nothing will change.", e)
		return expr, nil, nil
	} else if leftHasNumber {
		log.Debugf(context.TODO(),
			"expr->[%s] with left number, will return expr->[%s] and right grouping->[%s]", e, expr, rightGroup,
		)
		return expr, rightGroup, nil
	} else if rightHasNumber {
		log.Debugf(context.TODO(),
			"expr->[%s] with right number, will return expr->[%s] and left grouping->[%s]",
			e, expr, rightGroup,
		)
		return expr, leftGroup, nil
	}

	// 否则要根据两遍的维度状况进行动态处理
	// 如果是 setOperator 则不需要进行此逻辑
	// "and":    LAND,
	//	"or":     LOR,
	//	"unless": LUNLESS
	// 不对两遍表达式进行拼装 vectorMatching，原先有就有，没有就没有，拼装会导致语句不一致
	if false && !expr.Op.IsSetOperator() {
		if len(leftGroup) != 0 && len(rightGroup) != 0 {
			log.Debugf(context.TODO(),
				"both side has group left->[%d] right->[%d] will find the sub set.", len(leftGroup), len(rightGroup),
			)
			// 哪边为父集，输出列就向哪边对齐，group则选择最小集合
			if isSubset(leftGroup, rightGroup) {
				finalGroup = rightGroup
				expr.VectorMatching = &parser.VectorMatching{MatchingLabels: leftGroup, Card: parser.CardOneToMany, On: true}
			} else if isSubset(rightGroup, leftGroup) {
				finalGroup = leftGroup
				expr.VectorMatching = &parser.VectorMatching{MatchingLabels: rightGroup, Card: parser.CardManyToOne, On: true}
			} else {
				// 全不为空，但又互不为子集是不允许的
				return nil, nil, errors.New("found not sub set")
			}
			// 后面判断单group为空的场景,此时dimension为空的一边强制对齐另外一边,并匹配所有维度
		} else if len(leftGroup) == 0 {
			log.Debugf(context.TODO(), "left group is empty, will use right group")
			finalGroup = rightGroup
			expr.VectorMatching = &parser.VectorMatching{MatchingLabels: nil, Card: parser.CardOneToMany, On: true}
		} else {
			log.Debugf(context.TODO(), "right group is empty, will use right group")
			finalGroup = leftGroup
			expr.VectorMatching = &parser.VectorMatching{MatchingLabels: nil, Card: parser.CardManyToOne, On: true}
		}
	}

	log.Debugf(context.TODO(), "expr->[%s] transfer to final expr->[%s] with group->[%s]", e, expr, finalGroup)
	return expr, finalGroup, nil
}

// handleParenExpr
func handleParenExpr(m map[string]*PromExpr, e parser.Expr) (parser.Expr, []string, error) {
	var (
		expr      *parser.ParenExpr
		innerExpr parser.Expr
		grouping  []string
		err       error
		ok        bool
	)
	expr, ok = e.(*parser.ParenExpr)
	if !ok {
		err = fmt.Errorf("error type %+v", e)
		return nil, nil, err
	}
	innerExpr, grouping, err = handleExpr(m, expr.Expr)
	if err != nil {
		return nil, nil, err
	}
	expr.Expr = innerExpr
	return expr, grouping, err
}

// handlerSubQueryExpr
func handlerSubQueryExpr(m map[string]*PromExpr, e parser.Expr) (parser.Expr, []string, error) {
	var (
		expr *parser.SubqueryExpr
		err  error
	)
	expr = e.(*parser.SubqueryExpr)
	expr.Expr, _, err = handleExpr(m, expr.Expr)
	if err != nil {
		return nil, nil, err
	}
	return expr, nil, nil
}

// handleCall
func handleCall(m map[string]*PromExpr, e parser.Expr) (parser.Expr, []string, error) {
	var (
		expr *parser.Call
		err  error
		ok   bool
	)
	expr, ok = e.(*parser.Call)
	if !ok {
		err = fmt.Errorf("error type %+v", e)
		return nil, nil, err
	}
	args := make(parser.Expressions, len(expr.Args))
	for i, a := range expr.Args {
		args[i], _, err = handleExpr(m, a)
		if err != nil {
			return nil, nil, err
		}
	}
	expr.Args = args
	return expr, nil, nil
}

// handleAggregateExpr
func handleAggregateExpr(m map[string]*PromExpr, e parser.Expr) (parser.Expr, []string, error) {
	var (
		expr *parser.AggregateExpr
		err  error
		ok   bool
	)
	expr, ok = e.(*parser.AggregateExpr)
	if !ok {
		err = fmt.Errorf("error type %+v", e)
		return nil, nil, err
	}
	expr.Expr, _, err = handleExpr(m, expr.Expr)
	if err != nil {
		return nil, nil, err
	}
	return expr, expr.Grouping, nil
}

// handleMatrixSelector
func handleMatrixSelector(m map[string]*PromExpr, e parser.Expr) (parser.Expr, []string, error) {
	var (
		expr *parser.MatrixSelector
		err  error
		ok   bool
	)
	expr, ok = e.(*parser.MatrixSelector)
	if !ok {
		err = fmt.Errorf("error type %+v", e)
		return nil, nil, err
	}
	expr.VectorSelector, _, err = handleExpr(m, expr.VectorSelector)
	if err != nil {
		return nil, nil, err
	}

	return expr, nil, err
}

// handleNumberLiteral
func handleNumberLiteral(m map[string]*PromExpr, e parser.Expr) (parser.Expr, []string, error) {
	var (
		expr *parser.NumberLiteral
		err  error
		ok   bool
	)
	expr, ok = e.(*parser.NumberLiteral)
	if !ok {
		err = fmt.Errorf("error type %+v", e)
	}
	return expr, nil, err
}

// handleExpr: 处理各个表达式之间的维度对齐内容
func handleExpr(m map[string]*PromExpr, expr parser.Expr) (parser.Expr, []string, error) {
	var (
		result parser.Expr
		group  []string
		err    error
	)

	switch expr.(type) {
	case *parser.UnaryExpr:
		result, group, err = handlerUnaryExpr(m, expr)
	case *parser.VectorSelector:
		result, group, err = handleVectorExpr(m, expr)
	case *parser.BinaryExpr:
		result, group, err = handleBinaryExpr(m, expr)
	case *parser.ParenExpr:
		result, group, err = handleParenExpr(m, expr)
	case *parser.Call:
		result, group, err = handleCall(m, expr)
	case *parser.AggregateExpr:
		result, group, err = handleAggregateExpr(m, expr)
	case *parser.NumberLiteral:
		result, group, err = handleNumberLiteral(m, expr)
	case *parser.MatrixSelector:
		result, group, err = handleMatrixSelector(m, expr)
	case *parser.SubqueryExpr:
		result, group, err = handlerSubQueryExpr(m, expr)
	default:
		// 认不出来的直接返回表达式
		log.Debugf(context.TODO(), "nothing need to transfer for expr->[%s]", expr)
		return expr, nil, nil
	}

	log.Debugf(context.TODO(), "expr->[%s] transfer to->[%s] with grouping->[%s] err->[%s]", expr, result, group, err)
	return result, group, err
}

// HandleExpr
func HandleExpr(m map[string]*PromExpr, expr parser.Expr) (parser.Expr, error) {
	resultExpr, _, err := handleExpr(m, expr)
	if err != nil {
		return nil, err
	}
	return resultExpr, nil
}

// 判断A是B的子集
func isSubset(a []string, b []string) bool {
	bMap := make(map[string]bool)
	for _, childItem := range b {
		bMap[childItem] = true
	}

	for _, childItem := range a {
		// B里不存在A，说明A不是B的子集
		if _, ok := bMap[childItem]; !ok {
			return false
		}
	}
	return true
}
