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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
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
	return e.processValidated(newValidatedStrategyHandle(strategy), outcome)
}

func (e *Evaluator) processValidated(strategy *StrategyHandle, outcome *contract.DetectionOutcome) (*Decision, error) {
	transaction := e.begin()
	decision, err := transaction.processValidated(strategy, outcome)
	if err != nil {
		transaction.discard()
		return nil, err
	}
	transaction.commit()
	return decision, nil
}

type evaluationTransaction struct {
	evaluator *Evaluator
	results   map[stateKey]map[int64]bool
	latest    map[stateKey]int64
	closed    bool
}

func (e *Evaluator) begin() *evaluationTransaction {
	e.mu.Lock()
	return &evaluationTransaction{
		evaluator: e,
		results:   make(map[stateKey]map[int64]bool),
		latest:    make(map[stateKey]int64),
	}
}

// record stores one outcome's per-level results in the batch overlay without
// deciding. Deciding here would read a window that is still missing the rest of
// the micro-batch, so a batch delivered out of source-time order would disagree
// with the legacy Python Trigger, which always reads the full CheckResult store.
func (t *evaluationTransaction) record(strategy *StrategyHandle, outcome *contract.DetectionOutcome) error {
	if strategy.purpose != contract.PurposeDetect {
		return ErrUnsupportedPurpose
	}
	for _, evaluation := range outcome.Evaluations {
		key := newStateKey(strategy, outcome, evaluation.Level)
		t.resultsFor(key)[outcome.Record.SourceTime] = evaluation.Result == contract.EvaluationAnomalous
		if latest, ok := t.latest[key]; !ok || outcome.Record.SourceTime > latest {
			t.latest[key] = outcome.Record.SourceTime
		}
	}
	return nil
}

// evict bounds overlay memory after every decision in the batch is materialized.
// Window filtering already excludes out-of-window points, so eviction only has
// to keep the newest window per state key.
func (t *evaluationTransaction) evict(strategy *StrategyHandle) {
	windowSizes := make(map[int]int, len(strategy.triggerConfigs))
	for _, config := range strategy.triggerConfigs {
		windowSizes[config.Level] = config.CheckWindowSize
	}
	for key, latest := range t.latest {
		windowSize, ok := windowSizes[key.level]
		if !ok {
			continue
		}
		prune(t.results[key], windowStart(latest, strategy.checkWindowUnitSeconds, windowSize))
	}
}

func (t *evaluationTransaction) processValidated(strategy *StrategyHandle, outcome *contract.DetectionOutcome) (*Decision, error) {
	if err := t.record(strategy, outcome); err != nil {
		return nil, err
	}
	decision, err := t.decide(strategy, outcome)
	if err != nil {
		return nil, err
	}
	t.evict(strategy)
	return decision, nil
}

// decide reads the completed batch overlay, so every record of one micro-batch
// is evaluated on event time regardless of its position in the array.
func (t *evaluationTransaction) decide(strategy *StrategyHandle, outcome *contract.DetectionOutcome) (*Decision, error) {
	if strategy.purpose != contract.PurposeDetect {
		return nil, ErrUnsupportedPurpose
	}
	if outcome.Outcome == contract.OutcomeNormal {
		return nil, nil
	}
	currentAnomalies := make(map[int]struct{}, len(outcome.Evaluations))
	for _, evaluation := range outcome.Evaluations {
		if evaluation.Result == contract.EvaluationAnomalous {
			currentAnomalies[evaluation.Level] = struct{}{}
		}
	}
	var lastAnomalyTimestamps []int64
	for _, config := range strategy.triggerConfigs {
		if _, ok := currentAnomalies[config.Level]; !ok {
			continue
		}
		key := newStateKey(strategy, outcome, config.Level)
		anomalyTimestamps := anomaliesInWindow(
			t.resultsFor(key),
			windowStart(outcome.Record.SourceTime, strategy.checkWindowUnitSeconds, config.CheckWindowSize),
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

func (t *evaluationTransaction) resultsFor(key stateKey) map[int64]bool {
	if results, ok := t.results[key]; ok {
		return results
	}
	results := make(map[int64]bool, len(t.evaluator.results[key]))
	for timestamp, anomalous := range t.evaluator.results[key] {
		results[timestamp] = anomalous
	}
	t.results[key] = results
	return results
}

func (t *evaluationTransaction) commit() {
	if t.closed {
		return
	}
	for key, results := range t.results {
		t.evaluator.results[key] = results
	}
	t.closed = true
	t.evaluator.mu.Unlock()
}

func (t *evaluationTransaction) discard() {
	if t.closed {
		return
	}
	t.closed = true
	t.evaluator.mu.Unlock()
}

func newStateKey(strategy *StrategyHandle, outcome *contract.DetectionOutcome, level int) stateKey {
	return stateKey{
		tenantID:      strategy.tenantID,
		purpose:       strategy.purpose,
		strategyID:    strategy.strategyRef.StrategyID,
		itemID:        strategy.strategyRef.ItemID,
		generation:    strategy.strategyRef.Generation,
		contentSHA256: strategy.strategyRef.ContentSHA256,
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
