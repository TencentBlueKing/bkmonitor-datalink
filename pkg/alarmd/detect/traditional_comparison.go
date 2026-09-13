package detect

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

type traditionalComparisonDetector struct{ kind string }

func (d traditionalComparisonDetector) Key() DetectorKey {
	return DetectorKey{Kind: d.kind, Version: 1}
}

func (d traditionalComparisonDetector) Evaluate(_ context.Context, algorithm strategy.CompiledAlgorithmPlan, input execution.SeriesEvaluationInputRequest, primary execution.RecordView) namedAlgorithmResult {
	c, ok := algorithm.TraditionalComparisonConfig()
	if !ok {
		return namedTerminal(contract.ReasonRecordInvalid)
	}
	current, canonical, err := numericRecordValue(primary, c.ValueField)
	if err != nil {
		return namedTerminal(contract.ReasonRecordInvalid)
	}
	history := make(map[int64]*float64)
	if current != 0 {
		history[0] = &current
	}
	for _, r := range algorithm.InputRequirements() {
		if r.Role != strategy.AlgorithmInputDependency {
			continue
		}
		var binding execution.NamedInputBinding
		for _, candidate := range input.Inputs {
			if string(candidate.RequirementID) == r.RequirementID {
				binding = candidate
				break
			}
		}
		if !namedInputTrusted(binding) {
			return unavailableFromBinding(binding)
		}
		for _, offset := range r.PointOffsetsSeconds {
			point, found, err := exactPoint(binding, primary.SourceTime()-offset)
			if err != nil {
				return namedTerminal(contract.ReasonRecordInvalid)
			}
			if !found {
				continue
			}
			if raw, ok := point.Value(c.ValueField); ok && strings.TrimSpace(string(raw)) == "null" {
				continue
			}
			value, _, err := numericRecordValue(point, c.ValueField)
			if err != nil {
				return namedTerminal(contract.ReasonRecordInvalid)
			}
			// Python's history loader only publishes truthy metric values. FULL empty
			// history remains missing; the currently admitted source is time_series.
			if value != 0 {
				v := value
				history[offset] = &v
			}
		}
	}
	status := evaluateTraditionalComparison(d.kind, c, current, history)
	return namedResult(status, canonical, algorithm.AlgorithmPlanID(), nil)
}

// Python round(float, ndigits) rounds the original binary float to decimal,
// not its already-rounded product with 10**ndigits.
func pythonRound(value float64, precision int) float64 {
	rounded, err := strconv.ParseFloat(strconv.FormatFloat(value, 'f', precision, 64), 64)
	if err != nil {
		return value
	}
	return rounded
}

func comparisonUnitRounds(unit string) bool {
	if parts := strings.Split(unit, "||"); len(parts) == 2 {
		unit = parts[1]
	}
	switch unit {
	case "", "none", "short", "celsius", "fahrenheit", "kelvin":
		return false
	}
	// Unknown prefixes in RingRatioAmplitude's first threshold call create a
	// custom unit with no conversion or rounding.
	switch unit {
	case "Ki", "Mi", "Gi", "Ti", "Pi", "Ei", "k", "M", "G", "T", "P", "E", "K", "B", "%", "x100%":
		return false
	}
	return true
}

func evaluateTraditionalComparison(kind string, c strategy.TraditionalComparisonConfig, current float64, history map[int64]*float64) pureDetectionStatus {
	number := func(n *json.Number) float64 {
		if n == nil {
			return 0
		}
		v, _ := strconv.ParseFloat(n.String(), 64)
		return v
	}
	convert := func(v float64, multiplier int64, unit string) float64 {
		v *= float64(multiplier)
		if comparisonUnitRounds(unit) {
			return pythonRound(v, c.Precision)
		}
		return v
	}
	data := func(v float64) float64 { return convert(v, c.DataMultiplier, c.DataUnit) }
	parameter := func(v float64) float64 { return convert(v, c.AlgorithmMultiplier, c.DataUnit) }
	currentValue := data(current)
	compare := func(left, right float64) bool {
		switch c.Method {
		case "gt":
			return left > right
		case "gte":
			return left >= right
		case "lt":
			return left < right
		case "lte":
			return left <= right
		case "eq":
			return left == right
		case "neq":
			return left != right
		}
		return false
	}
	result := func(matched bool) pureDetectionStatus {
		if matched {
			return pureDetectionAnomalous
		}
		return pureDetectionNormal
	}
	switch kind {
	case strategy.DetectorKindSimpleYearRound:
		previous := history[604800]
		if previous == nil {
			return pureDetectionUnknown
		}
		v := data(*previous)
		floor, ceil := number(c.Floor), number(c.Ceil)
		var f, p *float64
		if floor > 0 {
			f = &floor
		}
		if ceil > 0 {
			p = &ceil
		}
		status, _ := evaluateSimpleRingRatio(simpleRingRatioInput{current: currentValue, previous: &v, floorPercent: f, ceilPercent: p})
		return status
	case strategy.DetectorKindAdvancedRingRatio, strategy.DetectorKindAdvancedYearRound:
		step := c.AggregationInterval
		if kind == strategy.DetectorKindAdvancedYearRound {
			step = 86400
		}
		aggregate := func(count int) *float64 {
			values := []float64{}
			for i := 1; i <= count; i++ {
				if point := history[int64(i)*step]; point != nil {
					v := *point
					if kind == strategy.DetectorKindAdvancedYearRound {
						v = math.Abs(v)
					}
					values = append(values, v)
				}
			}
			if len(values) == 0 {
				return nil
			}
			v := values[len(values)-1]
			if c.FetchType == "avg" {
				v = 0
				for _, value := range values {
					v += value
				}
				v = pythonRound(v/float64(len(values)), c.Precision)
			}
			v = data(v)
			return &v
		}
		for _, side := range []struct {
			percent *json.Number
			count   int
			floor   bool
		}{{c.Floor, c.FloorInterval, true}, {c.Ceil, c.CeilInterval, false}} {
			percent := number(side.percent)
			if percent == 0 {
				continue
			}
			previous := aggregate(side.count)
			if previous == nil {
				return pureDetectionUnknown
			}
			if currentValue == 0 && *previous == 0 {
				continue
			}
			if side.floor && currentValue <= *previous*(100-percent)*0.01 || !side.floor && currentValue >= *previous*(100+percent)*0.01 {
				return pureDetectionAnomalous
			}
		}
		return pureDetectionNormal
	case strategy.DetectorKindRingRatioAmplitude:
		previous := history[c.AggregationInterval]
		if previous == nil {
			return pureDetectionUnknown
		}
		threshold := number(c.Threshold)
		return result(currentValue >= convert(threshold, c.ThresholdMultiplier, c.AlgorithmUnit) && data(*previous) >= parameter(threshold) && data(math.Abs(*previous-current)) >= data(*previous)*number(c.Ratio)+parameter(number(c.Shock)))
	case strategy.DetectorKindYearRoundRange:
		// The source fetcher compacts missing days; expression indices still run
		// to configured days. A match before the first out-of-range index wins.
		for day := 1; day <= c.Days; day++ {
			if previous := history[int64(day)*86400]; previous != nil {
				if compare(math.Abs(currentValue), math.Abs(data(*previous))*number(c.Ratio)+parameter(number(c.Shock))) {
					return pureDetectionAnomalous
				}
			}
		}
		count := 0
		for day := 1; day <= c.Days; day++ {
			if history[int64(day)*86400] != nil {
				count++
			}
		}
		if count < c.Days {
			return pureDetectionUnknown
		}
		return pureDetectionNormal
	case strategy.DetectorKindYearRoundAmplitude:
		previous := history[c.AggregationInterval]
		if previous == nil {
			return pureDetectionUnknown
		}
		// Offset zero is fetched from the history store too, so its zero-filtering
		// behavior differs from the other algorithms' primary value.
		present := history[0]
		if present == nil {
			return pureDetectionUnknown
		}
		left := data(math.Abs(*present - *previous))
		for day := 1; day <= c.Days; day++ {
			a, b := history[int64(day)*86400], history[int64(day)*86400+c.AggregationInterval]
			if a == nil || b == nil {
				return pureDetectionUnknown
			}
			if compare(left, data(math.Abs(*a-*b))*number(c.Ratio)+parameter(number(c.Shock))) {
				return pureDetectionAnomalous
			}
		}
		return pureDetectionNormal
	}
	return pureDetectionUnknown
}
