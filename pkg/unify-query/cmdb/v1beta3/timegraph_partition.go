// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.
package v1beta3

import (
	"context"
	"fmt"
	"math"
	"slices"

	"github.com/prometheus/prometheus/model/labels"

	pl "github.com/prometheus/prometheus/promql"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

// Only the relation builder's single count-over-time/count-by query is eligible.
// Each partition label survives aggregation, so filtering it cannot split an
// aggregate group. Callers must also prove that series order cannot change node
// attributes before enabling cardinality partitioning.
func topologyPartitionFields(ts *structured.QueryTs) []string {
	if ts == nil || len(ts.QueryList) != 1 || ts.QueryList[0] == nil {
		return nil
	}
	q := ts.QueryList[0]
	if ts.MetricMerge != q.ReferenceName || q.TimeAggregation.Function != structured.CountOT ||
		len(q.AggregateMethodList) != 1 || q.AggregateMethodList[0].Method != structured.COUNT {
		return nil
	}
	fields := slices.Clone(q.AggregateMethodList[0].Dimensions)
	// Exact filters already bound a dimension to one value. Do not issue a probe
	// for them, including when every OR branch has the same exact value.
	groups, err := q.Conditions.AnalysisConditions()
	if err != nil {
		return nil
	}
	return slices.DeleteFunc(fields, func(name string) bool {
		var value string
		for i, group := range groups {
			found := false
			for _, f := range group {
				if f.DimensionName == name && f.Operator == structured.ConditionEqual && len(f.Value) == 1 {
					if i > 0 && value != f.Value[0] {
						return false
					}
					value, found = f.Value[0], true
					break
				}
			}
			if !found {
				return false
			}
		}
		return len(groups) > 0
	})
}

// Probes only choose a partition; they never select the result's identity set.
// The complementary !~ branch always runs, including for values absent from a
// partial probe or appearing later. Both branches retain the full original
// time grid, conditions, aggregation dimensions and endpoint identities.
func (loader *timeGraphMatrixLoader) partitionTopologyQuery(ctx context.Context, ts *structured.QueryTs, fields []string) (left, right *structured.QueryTs, remaining []string, split bool, err error) {
	for i, field := range fields {
		if err = ctx.Err(); err != nil {
			return
		}
		probe := *ts
		// A probe only chooses a split. Positive and negative filters still cover
		// values that first appear later, so one point is sufficient for the
		// estimate and avoids returning a full-range probe matrix.
		probe.End = probe.Start
		q := *ts.QueryList[0]
		q.Conditions.FieldList = slices.Clone(q.Conditions.FieldList)
		q.Conditions.ConditionList = slices.Clone(q.Conditions.ConditionList)
		for j := range q.Conditions.FieldList {
			q.Conditions.FieldList[j].Value = slices.Clone(q.Conditions.FieldList[j].Value)
		}
		q.AggregateMethodList = slices.Clone(q.AggregateMethodList)
		q.AggregateMethodList[0].Dimensions = []string{field}
		probe.QueryList = []*structured.Query{&q}
		probeCtx := newTimeGraphSubqueryContext(ctx)
		metadata.GetQueryParams(probeCtx).SetIsSkipK8s(metadata.GetQueryParams(ctx).IsSkipK8s)
		probeCtx = withTimeGraphQueryStage(probeCtx, "relation-partition")
		matrix, _, probeErr := loader.queryWithQuality(probeCtx, &probe, false)
		// A probe is not graph input and must not remain in the matrix reuse slot.
		loader.lastMatrix = timeGraphMatrixSlot{}
		if err = ctx.Err(); err != nil {
			return
		}
		if probeErr != nil {
			return nil, nil, nil, false, nil
		}
		pattern, ok := topologyPartitionPattern(matrix, field)
		if !ok {
			continue
		}
		if _, compileErr := labels.NewMatcher(labels.MatchRegexp, field, pattern); compileErr != nil {
			continue
		}

		left, err = topologyQueryWithPartition(ts, field, pattern, structured.ConditionRegEqual)
		if err != nil {
			return
		}
		right, err = topologyQueryWithPartition(ts, field, pattern, structured.ConditionNotRegEqual)
		if err != nil {
			return
		}
		return left, right, fields[i+1:], true, nil
	}
	return nil, nil, nil, false, nil
}

// Choose a character-position split using approximate physical series counts.
// The expression contains only an index and an ASCII range, never an enumerated
// identity list or escaped label contents. Its length stays small for large pod
// populations and survives the backend's regex transport. Values outside that
// range, shorter labels and values absent from the probe stay in the !~ branch.
func topologyPartitionPattern(matrix pl.Matrix, field string) (string, bool) {
	weights := make(map[string]float64, len(matrix))
	for _, series := range matrix {
		if len(series.Points) == 0 {
			continue
		}
		weight := 1.0
		for _, point := range series.Points {
			if !math.IsNaN(point.V) && !math.IsInf(point.V, 0) && point.V > weight {
				weight = point.V
			}
		}
		value := series.Metric.Get(field)
		weights[value] = max(weights[value], weight)
	}
	if len(weights) < 2 {
		return "", false
	}
	histograms := map[int]*[128]float64{}
	total := 0.0
	maxPosition := 0
	for value, weight := range weights {
		total += weight
		position := 0
		for _, r := range value {
			if r >= ' ' && r <= '~' {
				if histograms[position] == nil {
					histograms[position] = new([128]float64)
				}
				histograms[position][r] += weight
				maxPosition = max(maxPosition, position)
			}
			position++
		}
	}
	bestPosition, bestCut, bestDistance := -1, byte(0), math.Inf(1)
	for position := 0; position <= maxPosition; position++ {
		histogram := histograms[position]
		if histogram == nil {
			continue
		}
		prefix := 0.0
		for cut := byte(' '); cut <= '~'; cut++ {
			prefix += histogram[cut]
			// Alphanumeric endpoints require no regex/string/SQL escaping.
			if !((cut >= '0' && cut <= '9') || (cut >= 'A' && cut <= 'Z') || (cut >= 'a' && cut <= 'z')) || prefix <= 0 || prefix >= total {
				continue
			}
			if distance := math.Abs(total - 2*prefix); distance < bestDistance {
				bestPosition, bestCut, bestDistance = position, cut, distance
			}
		}
	}
	if bestPosition < 0 {
		return "", false
	}
	return fmt.Sprintf("(?s)^.{%d}[ -%c].*$", bestPosition, bestCut), true
}

func topologyQueryWithPartition(ts *structured.QueryTs, field, pattern, operator string) (*structured.QueryTs, error) {
	result := *ts
	q := *ts.QueryList[0]
	groups, err := q.Conditions.AnalysisConditions()
	if err != nil {
		return nil, err
	}
	if len(groups) == 0 {
		groups = structured.AllConditions{nil}
	}
	conditions := structured.Conditions{}
	for _, group := range groups {
		if len(conditions.FieldList) > 0 {
			conditions.ConditionList = append(conditions.ConditionList, structured.ConditionOr)
		}
		for _, f := range group {
			f.Value = slices.Clone(f.Value)
			conditions.FieldList = append(conditions.FieldList, f)
			conditions.ConditionList = append(conditions.ConditionList, structured.ConditionAnd)
		}
		conditions.FieldList = append(conditions.FieldList, structured.ConditionField{DimensionName: field, Value: []string{pattern}, Operator: operator})
	}
	q.Conditions = conditions
	result.QueryList = []*structured.Query{&q}
	return &result, nil
}
