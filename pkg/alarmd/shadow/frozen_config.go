package shadow

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// ErrFrozenConfigUnsupported is a comparison capability boundary. It never
// changes the business result, query completion, or execution permissions.
var ErrFrozenConfigUnsupported = errors.New("shadow: frozen comparison shape unsupported")

// BuildFrozenComparisonConfigV2 reads one actual frozen Plan and its bound
// dependencies. It performs no lookup and creates no execution/ACK facts.
func BuildFrozenComparisonConfigV2(due execution.DuePlan, requirements []execution.DataRequirement, queries map[execution.LogicalQueryRef]execution.QueryPlanFacts) (contract.ComparisonConfigV2, error) {
	fail := func(field string) (contract.ComparisonConfigV2, error) {
		return contract.ComparisonConfigV2{}, fmt.Errorf("%w: %s", ErrFrozenConfigUnsupported, field)
	}
	if due.Identity.Validate() != nil || due.CompiledPlan == nil {
		return fail("plan identity")
	}
	plan := due.CompiledPlan
	ref := plan.PlanRef()
	if ref.StrategyID != due.Identity.StrategyID {
		return fail("compiled plan binding")
	}
	revision, err := execution.DerivePlanScheduleRevision(due.ScheduleSpec)
	if err != nil || revision != due.ScheduleRevision {
		return fail("schedule binding")
	}
	semantics := plan.EvaluationSemantics()
	if semantics.EvaluationScope != contract.EvaluationScopeSeries {
		return fail("evaluation scope")
	}
	if int64(semantics.EvaluationInterval) != due.ScheduleSpec.EvaluationIntervalSeconds {
		return fail("schedule interval")
	}
	var selected *execution.DataRequirement
	levelsCovered := map[uint32]bool{}
	for _, requirement := range requirements {
		bound := requirement
		bound.Consumers = nil
		for _, consumer := range requirement.Consumers {
			if consumer.Consumer.Plan == due.Identity {
				bound.Consumers = append(bound.Consumers, consumer)
			}
		}
		if len(bound.Consumers) == 0 {
			continue
		}
		if bound.Validate(map[execution.PlanIdentity]execution.DuePlan{due.Identity: due}) != nil {
			return fail("requirement binding")
		}
		if selected != nil {
			previous, current := *selected, bound
			previous.Consumers = nil
			current.Consumers = nil
			if !reflect.DeepEqual(previous, current) {
				return fail("multiple requirements")
			}
		}
		if bound.Role != execution.InputRolePrimary || len(bound.NamedPoints) != 0 || len(bound.PointOffsetsSeconds) != 0 || bound.RelativeWindow.EndOffsetSeconds != 0 || bound.RelativeWindow.StartOffsetSeconds != -int64(semantics.QueryWindow) {
			return fail("primary window")
		}
		for _, consumer := range bound.Consumers {
			if consumer.Consumer.HasLevel {
				levelsCovered[consumer.Consumer.LevelID] = true
			} else {
				for _, level := range plan.Levels() {
					levelsCovered[level.Definition().LevelID] = true
				}
			}
		}
		selected = &bound
	}
	if selected == nil {
		return fail("missing primary requirement")
	}
	query, ok := queries[selected.LogicalQueryRef]
	if !ok || query.Validate() != nil || execution.LogicalQueryRef(query.QueryRevision) != selected.LogicalQueryRef || query.TenantID != due.Identity.TenantID || query.BusinessID != due.Identity.BusinessID {
		return fail("query binding")
	}
	if query.StepMillis != selected.StepMillis || query.AlignmentMillis != selected.AlignmentMillis || query.StepMillis != int64(semantics.AggregationInterval)*1000 {
		return fail("query cadence")
	}
	datasetDigest, err := contract.DeriveCanonicalDigestV2("strategy-dataset-contract-v1", query.Normalization.DatasetContract)
	if err != nil || datasetDigest != plan.DatasetContractDigest() {
		return fail("dataset binding")
	}
	projection := plan.Projection()
	if !slices.Equal(projection.ValueFields, selected.InputProjection.ValueFields) || !slices.Equal(projection.DimensionFields, selected.InputProjection.DimensionFields) || !slices.Equal(query.Normalization.DatasetContract.IdentityFields, selected.InputProjection.IdentityFields) {
		return fail("projection binding")
	}
	if projection.MultiValueAlignment != "SINGLE_VALUE" || len(projection.ValueFields) != 1 || projection.ValueFields[0] != "value" || projection.MissingValuePolicy != contract.MissingValuePolicyRequired || projection.BusinessIdentityField != "bk_biz_id" {
		return fail("projection semantics")
	}
	if len(query.QueryList) != 1 || !slices.Equal(query.QueryList[0].Dimensions, projection.DimensionFields) {
		return fail("aggregation dimensions")
	}
	selector, err := frozenSelectorV2(query)
	if err != nil {
		return fail("query shape")
	}
	c := contract.ComparisonConfigV2{
		SchemaVersion: "comparison-config-v2", SelectionMappingVersion: "effective-order-v1", Selector: selector,
		Projection:    contract.ShadowProjectionConfigV2{ValueFields: append([]string{}, projection.ValueFields...), IdentityFields: append([]string{}, selected.InputProjection.IdentityFields...), RequiredDimensions: append([]string{}, projection.DimensionFields...)},
		EffectiveTime: "ALWAYS", Schedule: contract.ShadowScheduleConfigV2{IntervalSeconds: semantics.EvaluationInterval, WindowSeconds: semantics.QueryWindow, AlignmentSeconds: int64(due.ScheduleSpec.Alignment), Timezone: due.ScheduleSpec.Timezone},
	}
	for _, level := range plan.LevelsByPriority() {
		c.SelectionOrder = append(c.SelectionOrder, level.Definition().LevelID)
	}
	numericSet := false
	for _, level := range plan.Levels() {
		if !levelsCovered[level.Definition().LevelID] || level.EffectiveTimeRequirement().Kind() != strategy.EffectiveTimeAlways {
			return fail("level coverage/effective time")
		}
		algorithms := level.Algorithms()
		detectors := level.Detectors()
		if len(algorithms) != len(detectors) || len(detectors) == 0 {
			return fail("detector closure")
		}
		trigger, recovery := level.Trigger(), level.Recovery()
		l := contract.ShadowLevelConfigV2{LevelID: level.Definition().LevelID, Priority: level.Definition().Priority, Connector: level.Connector(), Trigger: contract.ShadowTriggerConfigV2{WindowPoints: trigger.WindowSize, RequiredAnomalies: trigger.RequiredAnomalies, StepSeconds: trigger.StepSeconds}, Recovery: contract.ShadowRecoveryConfigV2{Enabled: recovery.Enabled, ConsecutiveWindows: recovery.ConsecutiveWindows, Mode: "CONTINUOUS_TRIGGER_MISS", InputRequirement: "DATA_DRIVEN"}}
		for i, detector := range detectors {
			provenance, _ := algorithms[i].SourceProvenance()
			if detector.Kind() != "Threshold" || detector.Version() != 1 || provenance.SourceAlgorithmFamily != "" || provenance.SourceMappingVersion != "" || provenance.CanonicalQueryDigest != "" {
				return fail("source mapping")
			}
			tree := detector.Predicate().Facts()
			if tree.Kind != strategy.PredicateAny || len(tree.Children) != 1 || tree.Children[0].Kind != strategy.PredicateAll || len(tree.Children[0].Children) != 1 {
				return fail("complex predicate")
			}
			leaf := tree.Children[0].Children[0]
			if leaf.Kind != strategy.PredicateCompare || len(leaf.Children) != 0 || detector.ValueRef() != projection.ValueFields[0] {
				return fail("scalar predicate")
			}
			normalizer, ok := plan.Normalizer(detector.NormalizerRef())
			if !ok {
				return fail("normalizer binding")
			}
			numeric := contract.ShadowNumericConfigV2{SourceUnit: normalizer.SourceUnit(), TargetUnit: normalizer.TargetUnit(), Multiplier: strconv.FormatInt(normalizer.SourceMultiplier(), 10), DecimalPlaces: normalizer.DecimalPlaces(), Rounding: normalizer.Rounding()}
			if numericSet && numeric != c.Numeric {
				return fail("multiple normalizers")
			}
			c.Numeric = numeric
			numericSet = true
			operator := leaf.Operator
			if operator == "NEQ" {
				operator = "NE"
			}
			l.Detectors = append(l.Detectors, contract.ShadowDetectorConfigV2{Kind: "Threshold", MappingVersion: "canonical-threshold-v2", Operator: operator, Threshold: leaf.NormalizedThreshold})
		}
		c.Levels = append(c.Levels, l)
	}
	b, _, err := contract.CanonicalComparisonConfigV2(c)
	if err != nil {
		return fail("canonical config")
	}
	normalized, err := contract.DecodeComparisonConfigV2(b, len(b))
	if err != nil {
		return fail("canonical codec")
	}
	return *normalized, nil
}

func frozenSelectorV2(query execution.QueryPlanFacts) (contract.ShadowSelectorConfigV2, error) {
	fail := func() (contract.ShadowSelectorConfigV2, error) {
		return contract.ShadowSelectorConfigV2{}, ErrFrozenConfigUnsupported
	}
	if len(query.QueryList) != 1 || query.DownSampleRange != execution.DownSampleNone || query.Timezone == "" {
		return fail()
	}
	q := query.QueryList[0]
	n := query.Normalization
	if n.DatasetContract.CollectionTimeField != "" || n.Version != "uq-threshold-normalization-v1" || n.CanonicalValueField != "value" || n.DatasetContract.SourceTimeField != "_time" || n.DatasetContract.ReceivedTimeField != "_received_time" {
		return fail()
	}
	if q.Driver != "influxdb" || q.TimeField != "time" || q.IsRegexp || q.Offset != "" || q.OffsetForward != "false" || q.QueryString != "" || q.ReferenceName == "" || query.MetricMerge != q.ReferenceName || len(q.Functions) != 1 {
		return fail()
	}
	f := q.Functions[0]
	aggregation := map[string]string{"mean": "AVG", "max": "MAX", "min": "MIN", "sum": "SUM", "count": "COUNT"}[f.Method]
	if aggregation == "" || f.Field != "" || f.Without || f.Position != 0 || len(f.Arguments) != 0 || f.Window != "" || f.Subquery || f.Step != "" || !slices.Equal(f.Dimensions, q.Dimensions) {
		return fail()
	}
	expectedTime := execution.QueryFunction{Method: map[string]string{"AVG": "avg_over_time", "MAX": "max_over_time", "MIN": "min_over_time", "SUM": "sum_over_time", "COUNT": "count_over_time"}[aggregation], Window: strconv.FormatInt(query.StepMillis/1000, 10) + "s"}
	// nil/empty slices carry the same empty function semantics.
	ta := q.TimeAggregation
	ta.Dimensions = nil
	ta.Arguments = nil
	if len(q.TimeAggregation.Dimensions) != 0 || len(q.TimeAggregation.Arguments) != 0 || !reflect.DeepEqual(ta, expectedTime) || query.StepMillis%1000 != 0 {
		return fail()
	}
	expectedColumns := append([]string{"_time", q.ReferenceName}, q.Dimensions...)
	if !slices.Equal(q.KeepColumns, expectedColumns) {
		return fail()
	}
	c := contract.ShadowSelectorConfigV2{Table: q.TableID, Metric: q.FieldName, Aggregation: aggregation, StepMillis: query.StepMillis, QueryAlignmentMillis: query.AlignmentMillis, Timezone: query.Timezone, NotTimeAlign: query.NotTimeAlign, Filters: []contract.ShadowFilterV2{}, FilterConnectors: append([]string{}, q.Conditions.Connectors...), DataSource: q.DataSource, Expression: "value"}
	for _, condition := range q.Conditions.Fields {
		if condition.Wildcard != "" || condition.Prefix != "" || condition.Suffix != "" {
			return fail()
		}
		filter := contract.ShadowFilterV2{Field: condition.Field, Operator: condition.Operator, Values: []string{}}
		for _, value := range condition.Values {
			if value.Kind != execution.QueryScalarString {
				return fail()
			}
			filter.Values = append(filter.Values, value.StringValue)
		}
		c.Filters = append(c.Filters, filter)
	}
	return c, nil
}
