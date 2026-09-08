// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/prometheus/prometheus/model/labels"
	promPromql "github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/promql/parser"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	uqPromql "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

func evaluationStepPlan(
	statement string,
	start, end time.Time,
	step time.Duration,
	instant bool,
) (map[string]int64, error) {
	expr, err := parser.ParseExpr(statement)
	if err != nil {
		return nil, err
	}

	baseDuration := end.Sub(start)
	if instant || baseDuration < 0 {
		baseDuration = 0
	}
	if step <= 0 {
		step = time.Second
	}
	stepsByReference := make(map[string]int64)

	var inspectErr error
	parser.Inspect(expr, func(node parser.Node, path []parser.Node) error {
		if inspectErr != nil {
			return inspectErr
		}
		selector, ok := node.(*parser.VectorSelector)
		if !ok {
			return nil
		}
		effectiveDuration := baseDuration
		effectiveStep := step
		for _, ancestor := range path {
			subquery, isSubquery := ancestor.(*parser.SubqueryExpr)
			if !isSubquery {
				continue
			}
			effectiveDuration += subquery.Range
			subqueryStep := subquery.Step
			if subqueryStep <= 0 {
				subqueryStep = uqPromql.GetDefaultStep()
			}
			if subqueryStep > 0 && subqueryStep < effectiveStep {
				effectiveStep = subqueryStep
			}
		}
		selectorSteps := int64(effectiveDuration/effectiveStep) + 1
		reference := selector.Name
		if reference == "" {
			for _, matcher := range selector.LabelMatchers {
				if matcher.Name == labels.MetricName {
					reference = matcher.Value
					break
				}
			}
		}
		current := stepsByReference[reference]
		if current > math.MaxInt64-selectorSteps {
			inspectErr = fmt.Errorf("evaluation capacity step estimate overflow")
			return inspectErr
		}
		stepsByReference[reference] = current + selectorSteps
		return nil
	})
	if inspectErr != nil {
		return nil, inspectErr
	}
	return stepsByReference, nil
}

func executeQueryWithResourceBudget(
	ctx context.Context,
	budget *metadata.ResourceBudget,
	instance tsdb.Instance,
	statement string,
	start, end time.Time,
	step time.Duration,
	instant bool,
) (result any, partial bool, release func(), err error) {
	if budget != nil {
		plan, planErr := evaluationStepPlan(statement, start, end, step, instant)
		if planErr != nil {
			return nil, false, nil, planErr
		}
		if planErr = budget.BeginEvaluation(plan); planErr != nil {
			return nil, false, nil, planErr
		}
	}

	var closeResult func()
	if instant {
		if owned, ok := instance.(tsdb.InstantQueryWithClose); ok {
			var vector promPromql.Vector
			vector, partial, closeResult, err = owned.DirectQueryWithClose(ctx, statement, end)
			result = vector
		} else if statusAware, ok := instance.(tsdb.InstantQueryWithPartial); ok {
			var vector promPromql.Vector
			vector, partial, err = statusAware.DirectQueryWithPartial(ctx, statement, end)
			result = vector
		} else {
			result, err = instance.DirectQuery(ctx, statement, end)
		}
	} else if owned, ok := instance.(tsdb.RangeQueryWithClose); ok {
		var matrix promPromql.Matrix
		matrix, partial, closeResult, err = owned.DirectQueryRangeWithClose(ctx, statement, start, end, step)
		result = matrix
	} else {
		result, partial, err = instance.DirectQueryRange(ctx, statement, start, end, step)
	}

	var once sync.Once
	release = func() {
		once.Do(func() {
			if closeResult != nil {
				closeResult()
			}
			if budget != nil {
				budget.EndEvaluation()
			}
		})
	}
	if err != nil {
		release()
		return nil, false, nil, err
	}
	return result, partial, release, nil
}

type ownedNamedQueryResult struct {
	value   any
	release func()
}
