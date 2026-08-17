// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package trigger

import (
	"errors"
	"sort"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/contract"
)

const (
	DecisionTrigger   = "TRIGGER"
	DecisionNoTrigger = "NO_TRIGGER"
)

var ErrUnsupportedPurpose = errors.New("trigger evaluator: unsupported purpose")

type Decision struct {
	InputID           string
	Outcome           string
	Level             int
	AnomalyTimestamps []int64
}

type stateKey struct {
	tenantID      string
	purpose       string
	strategyID    string
	itemID        string
	generation    string
	contentSHA256 string
	dimensionsMD5 string
	level         int
}

type Evaluator struct {
	mu      sync.Mutex
	results map[stateKey]map[int64]bool
}

func NewEvaluator() *Evaluator {
	return &Evaluator{results: make(map[stateKey]map[int64]bool)}
}

func (e *Evaluator) Process(strategy *contract.TriggerStrategyIR, outcome *contract.DetectionOutcome) (*Decision, error) {
	canDrive, err := outcome.CanDriveTrigger(strategy)
	if err != nil {
		return nil, err
	}
	if strategy.Purpose != contract.PurposeDetect {
		return nil, ErrUnsupportedPurpose
	}
	if !canDrive {
		return nil, nil
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	configs := make(map[int]contract.TriggerConfig, len(strategy.TriggerConfigs))
	for _, config := range strategy.TriggerConfigs {
		configs[config.Level] = config
	}
	currentAnomalies := make(map[int]struct{}, len(outcome.Evaluations))
	for _, evaluation := range outcome.Evaluations {
		key := newStateKey(strategy, outcome, evaluation.Level)
		results := e.results[key]
		if results == nil {
			results = make(map[int64]bool)
			e.results[key] = results
		}
		results[outcome.Record.SourceTime] = evaluation.Result == contract.EvaluationAnomalous
		prune(results, windowStart(outcome.Record.SourceTime, strategy.CheckWindowUnitSeconds, configs[evaluation.Level].CheckWindowSize))
		if evaluation.Result == contract.EvaluationAnomalous {
			currentAnomalies[evaluation.Level] = struct{}{}
		}
	}

	if outcome.Outcome == contract.OutcomeNormal {
		return nil, nil
	}
	var lastAnomalyTimestamps []int64
	for _, config := range strategy.TriggerConfigs {
		if _, ok := currentAnomalies[config.Level]; !ok {
			continue
		}
		key := newStateKey(strategy, outcome, config.Level)
		anomalyTimestamps := anomaliesInWindow(
			e.results[key],
			windowStart(outcome.Record.SourceTime, strategy.CheckWindowUnitSeconds, config.CheckWindowSize),
			outcome.Record.SourceTime,
		)
		lastAnomalyTimestamps = anomalyTimestamps
		if len(anomalyTimestamps) >= config.TriggerCount {
			return &Decision{
				InputID:           outcome.InputID,
				Outcome:           DecisionTrigger,
				Level:             config.Level,
				AnomalyTimestamps: anomalyTimestamps,
			}, nil
		}
	}
	return &Decision{
		InputID:           outcome.InputID,
		Outcome:           DecisionNoTrigger,
		AnomalyTimestamps: lastAnomalyTimestamps,
	}, nil
}

func newStateKey(strategy *contract.TriggerStrategyIR, outcome *contract.DetectionOutcome, level int) stateKey {
	return stateKey{
		tenantID:      strategy.TenantID,
		purpose:       strategy.Purpose,
		strategyID:    strategy.StrategyRef.StrategyID,
		itemID:        strategy.StrategyRef.ItemID,
		generation:    strategy.StrategyRef.Generation,
		contentSHA256: strategy.StrategyRef.ContentSHA256,
		dimensionsMD5: outcome.Record.DimensionsMD5,
		level:         level,
	}
}

func windowStart(sourceTime int64, unitSeconds, windowSize int) int64 {
	return sourceTime - (int64(unitSeconds)*int64(windowSize) - 1)
}

func prune(results map[int64]bool, start int64) {
	for timestamp := range results {
		if timestamp < start {
			delete(results, timestamp)
		}
	}
}

func anomaliesInWindow(results map[int64]bool, start, end int64) []int64 {
	timestamps := make([]int64, 0, len(results))
	for timestamp, anomalous := range results {
		if anomalous && timestamp >= start && timestamp <= end {
			timestamps = append(timestamps, timestamp)
		}
	}
	sort.Slice(timestamps, func(i, j int) bool { return timestamps[i] < timestamps[j] })
	return timestamps
}
