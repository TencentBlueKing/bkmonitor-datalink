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
	"strconv"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/trigger"
)

type Processor struct {
	trigger *trigger.Processor
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
		return fmt.Errorf("decode detect input: %w", err)
	}
	expectedKey, err := input.PartitionKey()
	if err != nil {
		return fmt.Errorf("derive detect input partition key: %w", err)
	}
	if !bytes.Equal(key, expectedKey) {
		return errors.New("detect processor: partition key mismatch")
	}
	plan, err := loadThresholdPlan(input.StrategyIR)
	if err != nil {
		return err
	}
	outcomes := make([]*contract.DetectionOutcome, 0, len(input.Records))
	for _, rawRecord := range input.Records {
		if err := ctx.Err(); err != nil {
			return err
		}
		outcome, err := plan.evaluate(input, rawRecord)
		if err != nil {
			return err
		}
		outcomes = append(outcomes, outcome)
	}
	triggerPayload, err := json.Marshal(struct {
		Schema               contract.Schema              `json:"schema"`
		RequiredFeatures     []string                     `json:"required_features"`
		PartitionHashVersion string                       `json:"partition_hash_version"`
		StrategyIR           *contract.TriggerStrategyIR  `json:"strategy_ir"`
		DetectionOutcomes    []*contract.DetectionOutcome `json:"detection_outcomes"`
	}{
		Schema:               contract.Schema{Name: "trigger-input", Major: 1, Minor: 0},
		RequiredFeatures:     []string{},
		PartitionHashVersion: contract.PartitionHashVersionV1,
		StrategyIR:           input.StrategyIR,
		DetectionOutcomes:    outcomes,
	})
	if err != nil {
		return fmt.Errorf("encode trigger input: %w", err)
	}
	return p.trigger.Process(ctx, key, triggerPayload)
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
	groups [][]thresholdCondition
}

type thresholdCondition struct {
	method    string
	threshold float64
}

type legacyStrategy struct {
	Items []struct {
		ID         json.Number `json:"id"`
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
		for _, algorithm := range item.Algorithms {
			if algorithm.Type != "Threshold" {
				return nil, fmt.Errorf("detect processor: unsupported algorithm %q", algorithm.Type)
			}
			if algorithm.UnitPrefix != "" {
				return nil, errors.New("detect processor: threshold unit_prefix is not supported")
			}
			parsed, err := parseThresholdAlgorithm(algorithm.Config)
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

func parseThresholdAlgorithm(raw json.RawMessage) (thresholdAlgorithm, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var outer []any
	if err := decoder.Decode(&outer); err != nil || len(outer) == 0 {
		return thresholdAlgorithm{}, errors.New("detect processor: invalid threshold config")
	}
	if _, flat := outer[0].(map[string]any); flat {
		outer = []any{outer}
	}
	algorithm := thresholdAlgorithm{groups: make([][]thresholdCondition, 0, len(outer))}
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
			number, ok := condition["threshold"].(json.Number)
			if !ok {
				return thresholdAlgorithm{}, errors.New("detect processor: threshold value must be numeric")
			}
			threshold, err := number.Float64()
			if err != nil {
				return thresholdAlgorithm{}, errors.New("detect processor: threshold value must be numeric")
			}
			group = append(group, thresholdCondition{method: method, threshold: threshold})
		}
		algorithm.groups = append(algorithm.groups, group)
	}
	return algorithm, nil
}

func (p *thresholdPlan) evaluate(input *contract.DetectInput, rawRecord json.RawMessage) (*contract.DetectionOutcome, error) {
	decoder := json.NewDecoder(bytes.NewReader(rawRecord))
	decoder.UseNumber()
	var record struct {
		RecordID string      `json:"record_id"`
		Time     int64       `json:"time"`
		Value    json.Number `json:"value"`
	}
	if err := decoder.Decode(&record); err != nil {
		return nil, fmt.Errorf("detect processor: decode record: %w", err)
	}
	value, err := record.Value.Float64()
	if err != nil {
		return nil, errors.New("detect processor: record value must be numeric")
	}
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
		matched := level.matches(value)
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
	for _, group := range a.groups {
		matched := true
		for _, condition := range group {
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
