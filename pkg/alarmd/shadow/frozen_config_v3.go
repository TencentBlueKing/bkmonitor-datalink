package shadow

import (
	"encoding/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"reflect"
)

// BuildFrozenComparisonConfigV3 projects the final bound query; it performs no
// source parsing, expression rewriting, dependency scheduling or external I/O.
func BuildFrozenComparisonConfigV3(due execution.DuePlan, requirements []execution.DataRequirement, queries map[execution.LogicalQueryRef]execution.QueryPlanFacts) (contract.ComparisonConfigV3, error) {
	common, facts, err := buildFrozenComparisonFacts(due, requirements, queries, true)
	if err != nil {
		return contract.ComparisonConfigV3{}, err
	}
	n := facts.Normalization
	if n.DatasetContract.CollectionTimeField != "" || n.Version != "uq-threshold-normalization-v1" || n.CanonicalValueField != "value" || n.DatasetContract.SourceTimeField != "_time" || n.DatasetContract.ReceivedTimeField != "_received_time" {
		return contract.ComparisonConfigV3{}, ErrFrozenConfigUnsupported
	}
	query, err := FrozenQueryConfigV3(facts)
	if err != nil {
		return contract.ComparisonConfigV3{}, err
	}
	c := contract.ComparisonConfigV3{SchemaVersion: "comparison-config-v3", SelectionMappingVersion: common.SelectionMappingVersion, SelectionOrder: common.SelectionOrder, Levels: common.Levels, Query: query, Projection: common.Projection, Numeric: common.Numeric, EffectiveTime: common.EffectiveTime, Schedule: common.Schedule}
	wire, _, err := contract.CanonicalComparisonConfigV3(c)
	if err != nil {
		return contract.ComparisonConfigV3{}, err
	}
	normalized, err := contract.DecodeComparisonConfigV3(wire, len(wire))
	if err != nil {
		return contract.ComparisonConfigV3{}, err
	}
	return *normalized, nil
}

// FrozenQueryConfigV3 consumes validated QueryPlanFacts, not raw source config.
func FrozenQueryConfigV3(f execution.QueryPlanFacts) (contract.ShadowQueryConfigV3, error) {
	if err := f.Validate(); err != nil {
		return contract.ShadowQueryConfigV3{}, err
	}
	q := contract.ShadowQueryConfigV3{Selectors: []contract.ShadowQuerySelectorV3{}, MetricMerge: f.MetricMerge, StepMillis: f.StepMillis, AlignmentMillis: f.AlignmentMillis, Timezone: f.Timezone, NotTimeAlign: f.NotTimeAlign, DownSampleRange: string(f.DownSampleRange)}
	for _, s := range f.QueryList {
		if s.OffsetForward != "true" && s.OffsetForward != "false" {
			return contract.ShadowQueryConfigV3{}, ErrFrozenConfigUnsupported
		}
		c := contract.ShadowQuerySelectorV3{Reference: s.ReferenceName, DataSource: s.DataSource, Driver: s.Driver, Table: s.TableID, Metric: s.FieldName, TimeField: s.TimeField, IsRegexp: s.IsRegexp, Dimensions: append([]string{}, s.Dimensions...), Conditions: contract.ShadowQueryConditionsV3{Fields: []contract.ShadowQueryConditionV3{}, Connectors: append([]string{}, s.Conditions.Connectors...)}, Functions: []contract.ShadowQueryFunctionV3{}, Offset: s.Offset, OffsetForward: s.OffsetForward == "true", KeepColumns: append([]string{}, s.KeepColumns...), QueryString: s.QueryString}
		for _, condition := range s.Conditions.Fields {
			values, err := frozenQueryScalarsV3(condition.Values)
			if err != nil {
				return contract.ShadowQueryConfigV3{}, err
			}
			c.Conditions.Fields = append(c.Conditions.Fields, contract.ShadowQueryConditionV3{Field: condition.Field, Operator: condition.Operator, Values: values, Wildcard: condition.Wildcard, Prefix: condition.Prefix, Suffix: condition.Suffix})
		}
		for _, function := range s.Functions {
			v, err := frozenQueryFunctionV3(function)
			if err != nil {
				return contract.ShadowQueryConfigV3{}, err
			}
			c.Functions = append(c.Functions, v)
		}
		ta := s.TimeAggregation
		ta.Dimensions = nil
		ta.Arguments = nil
		if !reflect.DeepEqual(ta, execution.QueryFunction{}) || len(s.TimeAggregation.Dimensions) != 0 || len(s.TimeAggregation.Arguments) != 0 {
			v, err := frozenQueryFunctionV3(s.TimeAggregation)
			if err != nil {
				return contract.ShadowQueryConfigV3{}, err
			}
			c.TimeAggregation = &v
		}
		q.Selectors = append(q.Selectors, c)
	}
	return q, nil
}
func frozenQueryScalarsV3(values []execution.QueryScalar) ([]contract.ShadowQueryScalarV3, error) {
	out := make([]contract.ShadowQueryScalarV3, 0, len(values))
	for _, v := range values {
		if err := v.Validate(); err != nil {
			return nil, err
		}
		var raw []byte
		switch v.Kind {
		case execution.QueryScalarString:
			raw, _ = json.Marshal(v.StringValue)
		case execution.QueryScalarNumber:
			raw, _ = json.Marshal(v.NumberValue)
		case execution.QueryScalarBoolean:
			raw, _ = json.Marshal(v.BoolValue)
		default:
			return nil, ErrFrozenConfigUnsupported
		}
		out = append(out, contract.ShadowQueryScalarV3{Kind: string(v.Kind), Value: raw})
	}
	return out, nil
}
func frozenQueryFunctionV3(f execution.QueryFunction) (contract.ShadowQueryFunctionV3, error) {
	args, err := frozenQueryScalarsV3(f.Arguments)
	if err != nil {
		return contract.ShadowQueryFunctionV3{}, err
	}
	return contract.ShadowQueryFunctionV3{Method: f.Method, Field: f.Field, Without: f.Without, Dimensions: append([]string{}, f.Dimensions...), Position: f.Position, Arguments: args, Window: f.Window, Subquery: f.Subquery, Step: f.Step}, nil
}
