package detect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// RecordValueView is the shared immutable value boundary for the phase-one
// wire adapter and the phase-two internal Dataset.
type RecordValueView interface {
	Value(string) (json.RawMessage, bool)
}

type PreparedPlan struct{ bound boundPlan }

func (evaluator *Evaluator) PreparePlan(plan *strategy.CompiledPlan) (PreparedPlan, error) {
	if evaluator == nil || evaluator.registry == nil || plan == nil {
		return PreparedPlan{}, errors.New("alarmd detect: evaluator and compiled plan are required")
	}
	if plan.EvaluationSemantics().EvaluationScope != contract.EvaluationScopeSeries {
		return PreparedPlan{}, errors.New("alarmd detect: prepared plan is not SERIES")
	}
	levels := plan.Levels()
	for i := range levels {
		if i > 0 && levels[i-1].Definition().LevelID >= levels[i].Definition().LevelID {
			return PreparedPlan{}, fmt.Errorf("alarmd detect: compiled levels are not ordered and unique")
		}
	}
	bound := boundPlan{execution: PlanExecution{Plan: plan}, levels: make([]boundLevel, len(levels))}
	projectionKeys := make(map[projectionKey]struct{})
	for levelIndex, level := range levels {
		bound.levels[levelIndex] = boundLevel{compiled: level, detectors: make([]boundDetector, len(level.Detectors()))}
		for detectorIndex, spec := range level.Detectors() {
			detector, ok := evaluator.registry.resolve(DetectorKey{Kind: spec.Kind(), Version: spec.Version()})
			if !ok {
				return PreparedPlan{}, errors.New("alarmd detect: compiled detector is unavailable")
			}
			normalizer, ok := plan.Normalizer(spec.NormalizerRef())
			if !ok {
				return PreparedPlan{}, errors.New("alarmd detect: compiled normalizer is unavailable")
			}
			bound.levels[levelIndex].detectors[detectorIndex] = boundDetector{spec: spec, detector: detector, normalizer: normalizer}
			bound.detectorCount++
			projectionKeys[projectionKey{valueRef: spec.ValueRef(), normalizerRef: spec.NormalizerRef()}] = struct{}{}
		}
	}
	bound.projectionCount = uint64(len(projectionKeys))
	return PreparedPlan{bound: bound}, nil
}

func (evaluator *Evaluator) EvaluatePreparedRecord(ctx context.Context, prepared PreparedPlan, record RecordValueView) ([]LevelFact, []ProjectedValue, uint64, error) {
	if record == nil || prepared.bound.execution.Plan == nil {
		return nil, nil, 0, errors.New("alarmd detect: prepared plan and record are required")
	}
	projections := make([]projectionEntry, 0)
	facts := make([]LevelFact, 0, len(prepared.bound.levels))
	var evaluations uint64
	for _, level := range prepared.bound.levels {
		fact, count, err := evaluator.evaluateLevel(ctx, prepared.bound.execution.Plan, record, level, &projections)
		if err != nil {
			return nil, nil, 0, err
		}
		facts, evaluations = append(facts, fact), evaluations+count
	}
	values := make([]ProjectedValue, len(projections))
	for i := range projections {
		values[i] = projections[i].view
	}
	return facts, values, evaluations, nil
}

var _ RecordValueView = recordValueMap(nil)

type recordValueMap map[string]json.RawMessage

func (m recordValueMap) Value(name string) (json.RawMessage, bool) {
	value, ok := m[name]
	return value, ok
}
