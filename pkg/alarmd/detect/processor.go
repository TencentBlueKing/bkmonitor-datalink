// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package detect

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/trigger"
)

type Processor struct {
	trigger *trigger.Processor
}

// ProcessingError carries safe coordinates for a Detect processing failure.
// Isolated is true only after an explicit Go terminal decision has been
// acknowledged, so the runtime may commit that input and continue.
type ProcessingError struct {
	Operation  string
	Reason     string
	Isolated   bool
	StrategyID string
	ItemID     string
	BatchID    string
	RecordID   string
	Records    int
	Err        error
}

func (e *ProcessingError) Error() string {
	return fmt.Sprintf("detect processor: %s: %v", e.Operation, e.Err)
}

func (e *ProcessingError) Unwrap() error {
	return e.Err
}

func NewProcessor(sink trigger.DecisionSink) *Processor {
	return &Processor{trigger: trigger.NewProcessor(sink)}
}

func (p *Processor) Process(ctx context.Context, key, payload []byte) error {
	if p == nil || p.trigger == nil {
		return errors.New("detect processor: trigger processor is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	input, err := contract.DecodeDetectInput(payload)
	if err != nil {
		return payloadError(payload, "decode", "invalid_input", false, err)
	}
	expectedKey, err := input.PartitionKey()
	if err != nil {
		return inputError(input, "partition", "invalid_input", false, err)
	}
	if !bytes.Equal(key, expectedKey) {
		return inputError(input, "partition", "invalid_input", false, errors.New("partition key mismatch"))
	}
	plan, err := loadThresholdPlan(input.StrategyIR)
	if err != nil {
		outcomes, outcomeErr := terminalOutcomes(input, contract.OutcomeUnsupported, "UNSUPPORTED_STRATEGY")
		if outcomeErr != nil {
			return inputError(input, "load_strategy", "internal", false, errors.Join(err, outcomeErr))
		}
		diagnostic := inputError(input, "load_strategy", "invalid_strategy", true, err)
		return p.processOutcomes(ctx, key, input, outcomes, diagnostic)
	}
	outcomes := make([]*contract.DetectionOutcome, 0, len(input.Records))
	for _, rawRecord := range input.Records {
		if err := ctx.Err(); err != nil {
			return err
		}
		outcome, err := plan.evaluate(input, rawRecord)
		if err != nil {
			isolated := inputError(input, "evaluate", "invalid_record", true, err)
			var record struct {
				RecordID string `json:"record_id"`
			}
			if json.Unmarshal(rawRecord, &record) == nil {
				isolated.RecordID = record.RecordID
			}
			terminal, outcomeErr := terminalOutcomes(input, contract.OutcomeError, "INVALID_INPUT")
			if outcomeErr != nil {
				return inputError(input, "evaluate", "internal", false, errors.Join(err, outcomeErr))
			}
			return p.processOutcomes(ctx, key, input, terminal, isolated)
		}
		outcomes = append(outcomes, outcome)
	}
	return p.processOutcomes(ctx, key, input, outcomes, nil)
}

func (p *Processor) processOutcomes(
	ctx context.Context,
	key []byte,
	input *contract.DetectInput,
	outcomes []*contract.DetectionOutcome,
	diagnostic *ProcessingError,
) error {
	err := p.trigger.ProcessOutcomes(
		ctx,
		key,
		input.PartitionHashVersion,
		input.StrategyIR,
		outcomes,
	)
	if err == nil {
		if diagnostic == nil {
			return nil
		}
		return diagnostic
	}
	return err
}

func terminalOutcomes(input *contract.DetectInput, outcome, errorCode string) ([]*contract.DetectionOutcome, error) {
	results := make([]*contract.DetectionOutcome, 0, len(input.Records))
	for _, rawRecord := range input.Records {
		decoder := json.NewDecoder(bytes.NewReader(rawRecord))
		decoder.UseNumber()
		var record struct {
			RecordID string `json:"record_id"`
			Time     int64  `json:"time"`
		}
		if err := decoder.Decode(&record); err != nil {
			return nil, fmt.Errorf("detect processor: decode terminal record: %w", err)
		}
		parts := strings.Split(record.RecordID, ".")
		if len(parts) != 2 {
			return nil, errors.New("detect processor: invalid terminal record_id")
		}
		inputID, err := contract.DeriveInputID(contract.InputIdentity{
			TenantID:              input.StrategyIR.TenantID,
			Purpose:               input.StrategyIR.Purpose,
			StrategyID:            input.StrategyIR.StrategyRef.StrategyID,
			ItemID:                input.StrategyIR.StrategyRef.ItemID,
			StrategyContentSHA256: input.StrategyIR.StrategyRef.ContentSHA256,
			RecordID:              record.RecordID,
		})
		if err != nil {
			return nil, err
		}
		result := &contract.DetectionOutcome{
			Schema:           contract.Schema{Name: "detection-outcome", Major: 1, Minor: 0},
			RequiredFeatures: []string{"full-level-evaluations-v1", "raw-json-v1"},
			InputID:          inputID,
			BatchID:          input.BatchID,
			TenantID:         input.StrategyIR.TenantID,
			Purpose:          input.StrategyIR.Purpose,
			StrategyRef:      input.StrategyIR.StrategyRef,
			Record: contract.DetectionRecord{
				RecordID:      record.RecordID,
				SourceTime:    record.Time,
				DimensionsMD5: parts[0],
				DataRaw:       append(json.RawMessage(nil), rawRecord...),
			},
			Evaluations: []contract.Evaluation{},
			Outcome:     outcome,
			ErrorCode:   json.RawMessage(strconv.Quote(errorCode)),
		}
		if err := result.Validate(input.StrategyIR); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

func inputError(input *contract.DetectInput, operation, reason string, isolated bool, err error) *ProcessingError {
	result := &ProcessingError{Operation: operation, Reason: reason, Isolated: isolated, Err: err}
	if input == nil {
		return result
	}
	result.BatchID = input.BatchID
	result.Records = len(input.Records)
	if input.StrategyIR != nil {
		result.StrategyID = input.StrategyIR.StrategyRef.StrategyID
		result.ItemID = input.StrategyIR.StrategyRef.ItemID
	}
	return result
}

func payloadError(payload []byte, operation, reason string, isolated bool, err error) *ProcessingError {
	var envelope struct {
		BatchID    string `json:"batch_id"`
		StrategyIR struct {
			StrategyRef contract.StrategyRef `json:"strategy_ref"`
		} `json:"strategy_ir"`
		Records []json.RawMessage `json:"records"`
	}
	_ = json.Unmarshal(payload, &envelope)
	return &ProcessingError{
		Operation: operation, Reason: reason, Isolated: isolated,
		StrategyID: envelope.StrategyIR.StrategyRef.StrategyID,
		ItemID:     envelope.StrategyIR.StrategyRef.ItemID,
		BatchID:    envelope.BatchID,
		Records:    len(envelope.Records),
		Err:        err,
	}
}

type thresholdPlan struct {
	strategy *contract.TriggerStrategyIR
	levels   map[int]thresholdLevel
}

type thresholdLevel struct {
	connector  string
	algorithms []thresholdAlgorithm
}

type thresholdAlgorithm struct {
	groups         [][]thresholdCondition
	valueScale     float64
	thresholdScale float64
}

type thresholdCondition struct {
	method    string
	threshold float64
}

type legacyStrategy struct {
	Items []struct {
		ID           json.Number `json:"id"`
		QueryConfigs []struct {
			Unit string `json:"unit"`
		} `json:"query_configs"`
		Algorithms []struct {
			Level      int             `json:"level"`
			Type       string          `json:"type"`
			UnitPrefix string          `json:"unit_prefix"`
			Config     json.RawMessage `json:"config"`
		} `json:"algorithms"`
	} `json:"items"`
	Detects []struct {
		Level     int    `json:"level"`
		Connector string `json:"connector"`
	} `json:"detects"`
}

func loadThresholdPlan(strategy *contract.TriggerStrategyIR) (*thresholdPlan, error) {
	legacyJSON, err := strategy.LegacyJSON()
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(legacyJSON))
	decoder.UseNumber()
	var legacy legacyStrategy
	if err := decoder.Decode(&legacy); err != nil {
		return nil, fmt.Errorf("detect processor: decode legacy strategy: %w", err)
	}
	connectors := make(map[int]string, len(legacy.Detects))
	for _, detect := range legacy.Detects {
		connectors[detect.Level] = detect.Connector
	}
	plan := &thresholdPlan{strategy: strategy, levels: make(map[int]thresholdLevel, len(strategy.RequiredLevels))}
	for _, item := range legacy.Items {
		if item.ID.String() != strategy.StrategyRef.ItemID {
			continue
		}
		valueUnit := ""
		if len(item.QueryConfigs) == 1 {
			valueUnit = item.QueryConfigs[0].Unit
		}
		for _, algorithm := range item.Algorithms {
			if algorithm.Type != "Threshold" {
				return nil, fmt.Errorf("detect processor: unsupported algorithm %q", algorithm.Type)
			}
			parsed, err := parseThresholdAlgorithm(algorithm.Config, valueUnit, algorithm.UnitPrefix)
			if err != nil {
				return nil, err
			}
			level := plan.levels[algorithm.Level]
			level.connector = connectors[algorithm.Level]
			level.algorithms = append(level.algorithms, parsed)
			plan.levels[algorithm.Level] = level
		}
		break
	}
	for _, level := range strategy.RequiredLevels {
		if len(plan.levels[level].algorithms) == 0 {
			return nil, fmt.Errorf("detect processor: threshold algorithm is missing for level %d", level)
		}
	}
	return plan, nil
}

func parseThresholdAlgorithm(raw json.RawMessage, valueUnit, thresholdSuffix string) (thresholdAlgorithm, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var outer []any
	if err := decoder.Decode(&outer); err != nil || len(outer) == 0 {
		return thresholdAlgorithm{}, errors.New("detect processor: invalid threshold config")
	}
	if _, flat := outer[0].(map[string]any); flat {
		outer = []any{outer}
	}
	valueScale, thresholdScale := thresholdUnitScales(valueUnit, thresholdSuffix)
	algorithm := thresholdAlgorithm{
		groups:         make([][]thresholdCondition, 0, len(outer)),
		valueScale:     valueScale,
		thresholdScale: thresholdScale,
	}
	for _, rawGroup := range outer {
		groupValues, ok := rawGroup.([]any)
		if !ok || len(groupValues) == 0 {
			return thresholdAlgorithm{}, errors.New("detect processor: invalid threshold group")
		}
		group := make([]thresholdCondition, 0, len(groupValues))
		for _, rawCondition := range groupValues {
			condition, ok := rawCondition.(map[string]any)
			if !ok {
				return thresholdAlgorithm{}, errors.New("detect processor: invalid threshold condition")
			}
			method, ok := condition["method"].(string)
			if !ok {
				return thresholdAlgorithm{}, errors.New("detect processor: threshold method is required")
			}
			switch method {
			case "gt", "gte", "eq", "neq", "lt", "lte":
			default:
				return thresholdAlgorithm{}, fmt.Errorf("detect processor: unsupported threshold method %q", method)
			}
			threshold, err := thresholdValue(condition["threshold"])
			if err != nil {
				return thresholdAlgorithm{}, errors.New("detect processor: threshold value must be numeric")
			}
			group = append(group, thresholdCondition{method: method, threshold: threshold})
		}
		algorithm.groups = append(algorithm.groups, group)
	}
	return algorithm, nil
}

func thresholdValue(raw any) (float64, error) {
	var (
		value float64
		err   error
	)
	switch raw := raw.(type) {
	case json.Number:
		value, err = raw.Float64()
	case string:
		value, err = strconv.ParseFloat(strings.TrimSpace(raw), 64)
	default:
		return 0, errors.New("threshold is not a number")
	}
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, errors.New("threshold is not finite")
	}
	return value, nil
}

func (p *thresholdPlan) evaluate(input *contract.DetectInput, rawRecord json.RawMessage) (*contract.DetectionOutcome, error) {
	decoder := json.NewDecoder(bytes.NewReader(rawRecord))
	decoder.UseNumber()
	var record struct {
		RecordID string          `json:"record_id"`
		Time     int64           `json:"time"`
		Value    json.RawMessage `json:"value"`
	}
	if err := decoder.Decode(&record); err != nil {
		return nil, fmt.Errorf("detect processor: decode record: %w", err)
	}
	value, valueValid := thresholdRecordValue(record.Value)
	parts := strings.Split(record.RecordID, ".")
	if len(parts) != 2 {
		return nil, errors.New("detect processor: invalid record_id")
	}
	sourceTime, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || sourceTime != record.Time {
		return nil, errors.New("detect processor: record time does not match record_id")
	}
	inputID, err := contract.DeriveInputID(contract.InputIdentity{
		TenantID:              p.strategy.TenantID,
		Purpose:               p.strategy.Purpose,
		StrategyID:            p.strategy.StrategyRef.StrategyID,
		ItemID:                p.strategy.StrategyRef.ItemID,
		StrategyContentSHA256: p.strategy.StrategyRef.ContentSHA256,
		RecordID:              record.RecordID,
	})
	if err != nil {
		return nil, err
	}
	evaluations := make([]contract.Evaluation, 0, len(p.strategy.RequiredLevels))
	anomalous := false
	for _, levelNumber := range p.strategy.RequiredLevels {
		level := p.levels[levelNumber]
		matched := valueValid && level.matches(value)
		evaluation := contract.Evaluation{Level: levelNumber, Result: contract.EvaluationNormal}
		if matched {
			anomalous = true
			evaluation.Result = contract.EvaluationAnomalous
			anomalyID := fmt.Sprintf("%s.%s.%s.%d", record.RecordID, p.strategy.StrategyRef.StrategyID, p.strategy.StrategyRef.ItemID, levelNumber)
			evaluation.Anomaly = json.RawMessage(`{"anomaly_id":` + strconv.Quote(anomalyID) + `}`)
		}
		evaluations = append(evaluations, evaluation)
	}
	outcome := contract.OutcomeNormal
	if anomalous {
		outcome = contract.OutcomeAnomalous
	}
	result := &contract.DetectionOutcome{
		Schema:           contract.Schema{Name: "detection-outcome", Major: 1, Minor: 0},
		RequiredFeatures: []string{"full-level-evaluations-v1", "raw-json-v1"},
		InputID:          inputID,
		BatchID:          input.BatchID,
		TenantID:         p.strategy.TenantID,
		Purpose:          p.strategy.Purpose,
		StrategyRef:      p.strategy.StrategyRef,
		Record: contract.DetectionRecord{
			RecordID:      record.RecordID,
			SourceTime:    sourceTime,
			DimensionsMD5: parts[0],
			DataRaw:       append(json.RawMessage(nil), rawRecord...),
		},
		Evaluations: evaluations,
		Outcome:     outcome,
	}
	if err := result.Validate(p.strategy); err != nil {
		return nil, err
	}
	return result, nil
}

// Python Threshold catches per-point algorithm exceptions and leaves that
// point non-anomalous. DetectInput is emitted only after the Python batch is
// finalized, so a missing or non-numeric value must not fail the Go consumer.
func thresholdRecordValue(raw json.RawMessage) (float64, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var number json.Number
	if err := decoder.Decode(&number); err != nil {
		return 0, false
	}
	value, err := number.Float64()
	return value, err == nil
}

func (l thresholdLevel) matches(value float64) bool {
	if l.connector == "or" {
		for _, algorithm := range l.algorithms {
			if algorithm.matches(value) {
				return true
			}
		}
		return false
	}
	for _, algorithm := range l.algorithms {
		if !algorithm.matches(value) {
			return false
		}
	}
	return true
}

func (a thresholdAlgorithm) matches(value float64) bool {
	value = roundThresholdValue(value * a.valueScale)
	for _, group := range a.groups {
		matched := true
		for _, condition := range group {
			condition.threshold = roundThresholdValue(condition.threshold * a.thresholdScale)
			if !condition.matches(value) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

type thresholdUnitSpec struct {
	suffixes      []string
	defaultIndex  int
	factor        float64
	suffixFactors map[string]float64
}

func thresholdUnitScales(unitID, thresholdSuffix string) (float64, float64) {
	spec, ok := thresholdUnitSpecFor(unitID)
	if !ok || len(spec.suffixes) == 0 {
		return 1, 1
	}
	valueScale := spec.scaleToBase(spec.defaultIndex)
	thresholdIndex := -1
	for index, suffix := range spec.suffixes {
		if suffix == thresholdSuffix {
			thresholdIndex = index
			break
		}
	}
	if thresholdIndex < 0 {
		return valueScale, 1
	}
	return valueScale, spec.scaleToBase(thresholdIndex)
}

func thresholdUnitSpecFor(unitID string) (thresholdUnitSpec, bool) {
	if parts := strings.Split(unitID, "||"); len(parts) == 2 {
		unitID = parts[1]
	}
	spec := thresholdUnitSpec{}
	switch unitID {
	case "percent":
		spec = thresholdUnitSpec{suffixes: []string{"%", "x100%"}, factor: 100}
	case "percentunit":
		spec = thresholdUnitSpec{suffixes: []string{"%", "x100%"}, defaultIndex: 1, factor: 100}
	case "bits", "bytes":
		spec = thresholdUnitSpec{suffixes: []string{"", "Ki", "Mi", "Gi", "Ti", "Pi", "Ei", "Zi", "Yi"}, factor: 1024}
	case "kbytes":
		spec = thresholdUnitSpec{suffixes: []string{"", "Ki", "Mi", "Gi", "Ti", "Pi", "Ei", "Zi", "Yi"}, defaultIndex: 1, factor: 1024}
	case "mbytes":
		spec = thresholdUnitSpec{suffixes: []string{"", "Ki", "Mi", "Gi", "Ti", "Pi", "Ei", "Zi", "Yi"}, defaultIndex: 2, factor: 1024}
	case "gbytes":
		spec = thresholdUnitSpec{suffixes: []string{"", "Ki", "Mi", "Gi", "Ti", "Pi", "Ei", "Zi", "Yi"}, defaultIndex: 3, factor: 1024}
	case "tbytes":
		spec = thresholdUnitSpec{suffixes: []string{"", "Ki", "Mi", "Gi", "Ti", "Pi", "Ei", "Zi", "Yi"}, defaultIndex: 4, factor: 1024}
	case "pbytes":
		spec = thresholdUnitSpec{suffixes: []string{"", "Ki", "Mi", "Gi", "Ti", "Pi", "Ei", "Zi", "Yi"}, defaultIndex: 5, factor: 1024}
	case "decbits", "decbytes", "pps", "bps", "Bps", "hertz":
		spec = thresholdUnitSpec{suffixes: []string{"", "k", "M", "G", "T", "P", "E", "Z", "Y"}, factor: 1000}
	case "deckbytes", "KBs", "Kbits":
		spec = thresholdUnitSpec{suffixes: []string{"", "k", "M", "G", "T", "P", "E", "Z", "Y"}, defaultIndex: 1, factor: 1000}
	case "decmbytes", "MBs", "Mbits":
		spec = thresholdUnitSpec{suffixes: []string{"", "k", "M", "G", "T", "P", "E", "Z", "Y"}, defaultIndex: 2, factor: 1000}
	case "decgbytes", "GBs", "Gbits":
		spec = thresholdUnitSpec{suffixes: []string{"", "k", "M", "G", "T", "P", "E", "Z", "Y"}, defaultIndex: 3, factor: 1000}
	case "dectbytes", "TBs", "Tbits":
		spec = thresholdUnitSpec{suffixes: []string{"", "k", "M", "G", "T", "P", "E", "Z", "Y"}, defaultIndex: 4, factor: 1000}
	case "decpbytes", "PBs", "Pbits":
		spec = thresholdUnitSpec{suffixes: []string{"", "k", "M", "G", "T", "P", "E", "Z", "Y"}, defaultIndex: 5, factor: 1000}
	case "ns", "µs", "ms", "s", "m", "h", "d":
		indexes := map[string]int{"ns": 0, "µs": 1, "ms": 2, "s": 3, "m": 4, "h": 5, "d": 6}
		spec = thresholdUnitSpec{
			suffixes:      []string{"ns", "µs", "ms", "s", "m", "h", "d"},
			defaultIndex:  indexes[unitID],
			factor:        1000,
			suffixFactors: map[string]float64{"m": 60, "h": 60, "d": 24},
		}
	case "cps", "ops", "reqps", "rps", "wps", "iops", "cpm", "opm", "rpm", "wpm":
		spec = thresholdUnitSpec{suffixes: []string{"", "K", "M", "B", "T"}, factor: 1000}
	default:
		return thresholdUnitSpec{}, false
	}
	return spec, true
}

func (s thresholdUnitSpec) scaleToBase(index int) float64 {
	scale := 1.0
	for index > 0 {
		factor := s.factor
		if value, ok := s.suffixFactors[s.suffixes[index]]; ok {
			factor = value
		}
		scale *= factor
		index--
	}
	return scale
}

func roundThresholdValue(value float64) float64 {
	return math.RoundToEven(value*1_000_000) / 1_000_000
}

func (c thresholdCondition) matches(value float64) bool {
	switch c.method {
	case "gt":
		return value > c.threshold
	case "gte":
		return value >= c.threshold
	case "eq":
		return value == c.threshold
	case "neq":
		return value != c.threshold
	case "lt":
		return value < c.threshold
	case "lte":
		return value <= c.threshold
	default:
		return false
	}
}
