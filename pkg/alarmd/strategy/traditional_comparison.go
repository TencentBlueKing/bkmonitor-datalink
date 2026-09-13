package strategy

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

const (
	DetectorKindSimpleYearRound    = "SimpleYearRound"
	DetectorKindAdvancedRingRatio  = "AdvancedRingRatio"
	DetectorKindAdvancedYearRound  = "AdvancedYearRound"
	DetectorKindRingRatioAmplitude = "RingRatioAmplitude"
	DetectorKindYearRoundAmplitude = "YearRoundAmplitude"
	DetectorKindYearRoundRange     = "YearRoundRange"
)

func IsTraditionalComparison(kind string) bool {
	switch kind {
	case DetectorKindSimpleYearRound, DetectorKindAdvancedRingRatio, DetectorKindAdvancedYearRound,
		DetectorKindRingRatioAmplitude, DetectorKindYearRoundAmplitude, DetectorKindYearRoundRange:
		return true
	}
	return false
}

// TraditionalComparisonParameters preserves the source expression's operands.
// Pointers distinguish an absent side from an explicitly configured zero.
type TraditionalComparisonParameters struct {
	Floor         *json.Number `json:"floor,omitempty"`
	Ceil          *json.Number `json:"ceil,omitempty"`
	FloorInterval int          `json:"floor_interval,omitempty"`
	CeilInterval  int          `json:"ceil_interval,omitempty"`
	FetchType     string       `json:"fetch_type,omitempty"`
	Days          int          `json:"days,omitempty"`
	Method        string       `json:"method,omitempty"`
	Ratio         *json.Number `json:"ratio,omitempty"`
	Shock         *json.Number `json:"shock,omitempty"`
	Threshold     *json.Number `json:"threshold,omitempty"`
}

type TraditionalComparisonConfig struct {
	TraditionalComparisonParameters
	ValueField          string `json:"value_field"`
	DataUnit            string `json:"data_unit"`
	AlgorithmUnit       string `json:"algorithm_unit"`
	Precision           int    `json:"precision"`
	AggregationInterval int64  `json:"aggregation_interval"`
	DataMultiplier      int64  `json:"data_multiplier"`
	AlgorithmMultiplier int64  `json:"algorithm_multiplier"`
	ThresholdMultiplier int64  `json:"threshold_multiplier"`
}

func (plan CompiledAlgorithmPlan) TraditionalComparisonConfig() (TraditionalComparisonConfig, bool) {
	if plan.config.TraditionalComparison == nil {
		return TraditionalComparisonConfig{}, false
	}
	c := *plan.config.TraditionalComparison
	// Config views cannot mutate the frozen plan through optional operands.
	clone := func(p *json.Number) *json.Number {
		if p == nil {
			return nil
		}
		n := *p
		return &n
	}
	c.Floor, c.Ceil, c.Ratio, c.Shock, c.Threshold = clone(c.Floor), clone(c.Ceil), clone(c.Ratio), clone(c.Shock), clone(c.Threshold)
	return c, true
}

func TraditionalHistoryOffsets(kind string, c TraditionalComparisonParameters, interval int64) ([]int64, error) {
	c = normalizeTraditionalParameters(kind, c)
	if interval <= 0 || interval > math.MaxInt64/4097 {
		return nil, fmt.Errorf("invalid aggregation interval")
	}
	n := c.FloorInterval
	if c.CeilInterval > n {
		n = c.CeilInterval
	}
	// Bound source-controlled fanout before allocating. The plan's existing byte
	// and compute budgets enforce the deployment-specific tighter limits.
	if n < 0 || n > 4096 || c.FloorInterval < 0 || c.CeilInterval < 0 || c.Days < 0 || c.Days > 4096 {
		return nil, fmt.Errorf("history interval exceeds limit")
	}
	var offsets []int64
	switch kind {
	case DetectorKindSimpleYearRound:
		offsets = []int64{604800}
	case DetectorKindRingRatioAmplitude:
		offsets = []int64{interval}
	case DetectorKindAdvancedRingRatio, DetectorKindAdvancedYearRound:
		if n == 0 {
			return nil, fmt.Errorf("history interval required")
		}
		step := interval
		if kind == DetectorKindAdvancedYearRound {
			step = 86400
		}
		for i := 1; i <= n; i++ {
			offsets = append(offsets, int64(i)*step)
		}
	case DetectorKindYearRoundRange, DetectorKindYearRoundAmplitude:
		if c.Days == 0 {
			return nil, fmt.Errorf("days required")
		}
		if kind == DetectorKindYearRoundAmplitude {
			offsets = append(offsets, interval)
		}
		for i := 1; i <= c.Days; i++ {
			offsets = append(offsets, int64(i)*86400)
			if kind == DetectorKindYearRoundAmplitude {
				offsets = append(offsets, int64(i)*86400+interval)
			}
		}
	default:
		return nil, fmt.Errorf("unsupported traditional comparison")
	}
	sort.Slice(offsets, func(i, j int) bool { return offsets[i] < offsets[j] })
	unique := offsets[:0]
	for _, v := range offsets {
		if len(unique) == 0 || unique[len(unique)-1] != v {
			unique = append(unique, v)
		}
	}
	return unique, nil
}

func normalizeTraditionalParameters(kind string, c TraditionalComparisonParameters) TraditionalComparisonParameters {
	if kind != DetectorKindAdvancedRingRatio && kind != DetectorKindAdvancedYearRound {
		return c
	}
	if c.FetchType == "" {
		c.FetchType = "avg"
	}
	zero := func(n *json.Number) bool {
		if n == nil {
			return true
		}
		v, err := strconv.ParseFloat(n.String(), 64)
		return err == nil && v == 0
	}
	if zero(c.Floor) || c.FloorInterval == 0 {
		c.Floor = nil
		c.FloorInterval = 0
	}
	if zero(c.Ceil) || c.CeilInterval == 0 {
		c.Ceil = nil
		c.CeilInterval = 0
	}
	return c
}

func TraditionalHistoryName(offset int64) string { return "history_" + strconv.FormatInt(offset, 10) }

// Consecutive ring points share one bounded query; day/week points remain
// discrete so the provider never scans all intervening historical samples.
func TraditionalHistoryGroups(kind string, offsets []int64) [][]int64 {
	if kind == DetectorKindAdvancedRingRatio {
		return [][]int64{offsets}
	}
	groups := make([][]int64, len(offsets))
	for i, offset := range offsets {
		groups[i] = []int64{offset}
	}
	return groups
}

func TraditionalHistoryDataset(kind string, offsets []int64) string {
	if kind == DetectorKindAdvancedRingRatio {
		return "ring_history_" + strconv.Itoa(len(offsets))
	}
	return TraditionalHistoryName(offsets[0])
}

type traditionalComparisonCompiler struct{ kind string }

func (c traditionalComparisonCompiler) Capability() AlgorithmCapability {
	return AlgorithmCapability{Kind: c.kind, Version: 1, EvaluationScope: contract.EvaluationScopeSeries, InputShape: "NAMED_SERIES", RequiredHistoryKind: algorithmHistoryPlanLocalInput, StateSchemaVersion: "traditional-comparison-v1", Deterministic: true, FixedComputeCost: 2, CostPerRecord: 2}
}

func (compiler traditionalComparisonCompiler) Compile(_ context.Context, ctx AlgorithmCompileContext, raw contract.AlgorithmIRV2) (AlgorithmCompileResult, error) {
	var wire struct {
		TraditionalComparisonParameters
		DataUnit        string                      `json:"data_unit"`
		AlgorithmUnit   string                      `json:"algorithm_unit"`
		Precision       int                         `json:"precision"`
		InputProjection AlgorithmInputProjection    `json:"input_projection"`
		Requirements    []AlgorithmInputRequirement `json:"requirements"`
	}
	if err := decodeStrict(raw.Config, &wire); err != nil {
		return AlgorithmCompileResult{}, configErrorf("traditional comparison: %v", err)
	}
	for _, n := range []*json.Number{wire.Floor, wire.Ceil, wire.Ratio, wire.Shock, wire.Threshold} {
		if n != nil {
			v, err := strconv.ParseFloat(n.String(), 64)
			if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
				return AlgorithmCompileResult{}, configErrorf("non-finite comparison operand")
			}
		}
	}
	for _, n := range []*json.Number{wire.Floor, wire.Ceil} {
		if n != nil {
			v, _ := strconv.ParseFloat(n.String(), 64)
			if v < 0 {
				return AlgorithmCompileResult{}, configErrorf("negative ratio side")
			}
		}
	}
	if wire.FloorInterval < 0 || wire.CeilInterval < 0 || wire.Days < 0 {
		return AlgorithmCompileResult{}, configErrorf("negative history interval")
	}
	params := normalizeTraditionalParameters(raw.Type, wire.TraditionalComparisonParameters)
	count := 1
	switch raw.Type {
	case DetectorKindAdvancedRingRatio, DetectorKindAdvancedYearRound:
		count = params.FloorInterval
		if params.CeilInterval > count {
			count = params.CeilInterval
		}
	case DetectorKindYearRoundRange:
		count = params.Days
	case DetectorKindYearRoundAmplitude:
		if params.Days > 4096 {
			return AlgorithmCompileResult{}, configErrorf("history point budget exceeded")
		}
		count = 2*params.Days + 1
	}
	if uint64(count) > uint64(ctx.Limits.MaxRequiredHistoryPoints) {
		return AlgorithmCompileResult{}, configErrorf("history point budget exceeded")
	}
	offsets, err := TraditionalHistoryOffsets(raw.Type, params, int64(ctx.ExecutionSemantics.AggregationInterval))
	if err != nil {
		return AlgorithmCompileResult{}, configErrorf("%v", err)
	}
	// Reuse the primary projection contract, then check each exact dependency.
	if uint64(len(offsets)) > uint64(ctx.Limits.MaxRequiredHistoryPoints) {
		return AlgorithmCompileResult{}, configErrorf("history point budget exceeded")
	}
	groups := TraditionalHistoryGroups(raw.Type, offsets)
	if len(wire.Requirements) != len(groups)+1 {
		return AlgorithmCompileResult{}, configErrorf("history requirements differ from configuration")
	}
	if _, err = validateG4Inputs(ctx, wire.InputProjection, wire.Requirements[:1], ""); err != nil {
		return AlgorithmCompileResult{}, configErrorf("%v", err)
	}
	requirements, err := canonicalAlgorithmRequirements(wire.Requirements)
	if err != nil {
		return AlgorithmCompileResult{}, configErrorf("%v", err)
	}
	for i, group := range groups {
		r := requirements[i+1]
		name := TraditionalHistoryDataset(raw.Type, group)
		points := make([]AlgorithmNamedInputPoint, len(group))
		for i, offset := range group {
			points[i] = AlgorithmNamedInputPoint{Name: TraditionalHistoryName(offset), OffsetSeconds: offset}
		}
		interval := int64(ctx.ExecutionSemantics.AggregationInterval)
		if r.Role != AlgorithmInputDependency || r.DatasetName != name || r.LogicalQueryRef != requirements[0].LogicalQueryRef || r.RelativeWindow.StartOffsetSeconds != -(group[len(group)-1]+interval) || r.RelativeWindow.EndOffsetSeconds != -group[0] || r.ReadinessClass != AlgorithmReadinessFinalizedRequired || r.StepMillis != requirements[0].StepMillis || r.AlignmentMillis != requirements[0].AlignmentMillis || !equalAlgorithmProjection(r.InputProjection, wire.InputProjection) || !equalAlgorithmOffsets(r.PointOffsetsSeconds, group) || !equalNamedPoints(r.NamedPoints, points) {
			return AlgorithmCompileResult{}, configErrorf("invalid exact history requirement")
		}
	}
	if wire.Precision != 6 {
		return AlgorithmCompileResult{}, configErrorf("unsupported point precision")
	}
	normalizer, multiplier, ok := compileUnitNormalizer(wire.DataUnit, wire.AlgorithmUnit)
	if !ok {
		return AlgorithmCompileResult{}, configErrorf("unsupported comparison unit")
	}
	thresholdMultiplier := int64(1)
	if n, _, ok := compileUnitNormalizer(wire.AlgorithmUnit, ""); ok {
		thresholdMultiplier = n.sourceMultiplier
	}
	nonnegative := func(n *json.Number) bool {
		if n == nil {
			return false
		}
		v, ok := parseDecimalRational(n.String(), true)
		return ok && v.Sign() >= 0
	}
	finiteNumber := func(n *json.Number) bool {
		if n == nil {
			return false
		}
		v, err := strconv.ParseFloat(n.String(), 64)
		return err == nil && !math.IsInf(v, 0) && !math.IsNaN(v)
	}
	switch raw.Type {
	case DetectorKindSimpleYearRound, DetectorKindAdvancedRingRatio, DetectorKindAdvancedYearRound:
		if params.Floor != nil && !nonnegative(params.Floor) || params.Ceil != nil && !nonnegative(params.Ceil) {
			return AlgorithmCompileResult{}, configErrorf("invalid ratio side")
		}
		enabled := func(n *json.Number) bool {
			if n == nil {
				return false
			}
			v, _ := strconv.ParseFloat(n.String(), 64)
			return v > 0
		}
		if !enabled(params.Floor) && !enabled(params.Ceil) {
			return AlgorithmCompileResult{}, configErrorf("ratio side required")
		}
		if raw.Type != DetectorKindSimpleYearRound && (params.FetchType != "avg" && params.FetchType != "last" || enabled(params.Floor) && params.FloorInterval == 0 || enabled(params.Ceil) && params.CeilInterval == 0) {
			return AlgorithmCompileResult{}, configErrorf("invalid aggregate window")
		}
	default:
		if !finiteNumber(params.Ratio) || !finiteNumber(params.Shock) || raw.Type == DetectorKindRingRatioAmplitude && !finiteNumber(params.Threshold) {
			return AlgorithmCompileResult{}, configErrorf("invalid amplitude operands")
		}
		if raw.Type != DetectorKindRingRatioAmplitude && params.Method != "gt" && params.Method != "gte" && params.Method != "lt" && params.Method != "lte" && params.Method != "eq" {
			return AlgorithmCompileResult{}, configErrorf("invalid comparison method")
		}
	}
	config := &TraditionalComparisonConfig{TraditionalComparisonParameters: params, ValueField: wire.InputProjection.ValueFields[0], DataUnit: wire.DataUnit, AlgorithmUnit: wire.AlgorithmUnit, Precision: wire.Precision, AggregationInterval: int64(ctx.ExecutionSemantics.AggregationInterval), DataMultiplier: normalizer.sourceMultiplier, AlgorithmMultiplier: multiplier, ThresholdMultiplier: thresholdMultiplier}
	return g4CompileResult(compiledAlgorithmConfig{TraditionalComparison: config}, wire.InputProjection, requirements, "traditional-comparison-compiler-v1", "python-ordered-history-v1", len(offsets)+1), nil
}
