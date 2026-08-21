// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package comparator joins authoritative TriggerInput records with Go and
// Python terminal decisions. It deliberately contains no transport, timeout,
// watermark, persistence or metric policy.
package comparator

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

type DecisionSide uint8

const (
	DecisionSideGo DecisionSide = iota + 1
	DecisionSidePython
)

type Disposition uint8

const (
	DispositionAccepted Disposition = iota + 1
	DispositionReplay
	DispositionConflict
	DispositionInvalid
)

type JoinStatus uint8

const (
	JoinPendingInput JoinStatus = iota + 1
	JoinPendingBoth
	JoinPendingGo
	JoinPendingPython
	JoinComplete
	JoinConflict
	JoinInvalid
)

type Eligibility uint8

const (
	EligibilityNone Eligibility = iota
	EligibilityEligible
	EligibilityWarmup
	EligibilityCoverageGap
	EligibilitySourceError
	EligibilityUnsupported
	EligibilityEpochUnstable
)

type Verdict uint8

const (
	VerdictNone Verdict = iota
	VerdictMatch
	VerdictHardDiff
)

type Gates struct {
	StableEpoch          bool
	CoverageComplete     bool
	EpochStartSourceTime *int64
}

type Update struct {
	InputID     string
	Disposition Disposition
}

type Assessment struct {
	InputID        string
	DecisionID     string
	Join           JoinStatus
	Eligibility    Eligibility
	Verdict        Verdict
	Authoritative  bool
	HasGo          bool
	HasPython      bool
	SourceConflict bool
	GoConflict     bool
	PythonConflict bool
	GoInvalid      bool
	PythonInvalid  bool
}

type Joiner struct {
	mu         sync.Mutex
	runEpoch   string
	maxEntries int
	entries    map[string]*entry
}

type entry struct {
	source         *sourceObservation
	goDecision     *decisionObservation
	pythonDecision *decisionObservation
	sourceConflict bool
	goConflict     bool
	pythonConflict bool
	goInvalid      bool
	pythonInvalid  bool
}

type sourceObservation struct {
	input       *contract.TriggerInput
	outcome     *contract.DetectionOutcome
	fingerprint [sha256.Size]byte
	maxWindow   int64
}

type decisionObservation struct {
	batch       decisionBatchIdentity
	decision    contract.TriggerDecision
	fingerprint [sha256.Size]byte
}

type decisionBatchIdentity struct {
	PartitionHashVersion string
	TenantID             string
	Purpose              string
	StrategyRef          contract.StrategyRef
	DecisionAlgorithm    string
}

type auditObservation struct {
	assessment        Assessment
	input             *contract.TriggerInput
	outcome           contract.DetectionOutcome
	sourceFingerprint [sha256.Size]byte
	goDecision        *decisionObservation
	pythonDecision    *decisionObservation
}

func NewJoiner(runEpoch string, maxEntries int) (*Joiner, error) {
	if runEpoch == "" {
		return nil, fmt.Errorf("comparator: run epoch must be non-empty")
	}
	if maxEntries <= 0 {
		return nil, fmt.Errorf("comparator: max entries must be positive")
	}
	return &Joiner{
		runEpoch:   runEpoch,
		maxEntries: maxEntries,
		entries:    make(map[string]*entry),
	}, nil
}

func (j *Joiner) ObserveTriggerInput(runEpoch string, key, payload []byte) ([]Update, error) {
	input, err := contract.DecodeTriggerInput(payload)
	if err != nil {
		return nil, err
	}
	expectedKey, err := input.PartitionKey()
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(key, expectedKey) {
		return nil, fmt.Errorf("comparator: trigger input partition key mismatch")
	}
	observations := make([]sourceObservation, len(input.DetectionOutcomes))
	for index, outcome := range input.DetectionOutcomes {
		fingerprint, err := sourceFingerprint(input, outcome)
		if err != nil {
			return nil, err
		}
		observations[index] = sourceObservation{
			input:       input,
			outcome:     outcome,
			fingerprint: fingerprint,
			maxWindow:   maximumWindow(input.StrategyIR),
		}
	}

	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.requireEpoch(runEpoch); err != nil {
		return nil, err
	}
	if err := j.requireCapacity(inputIDsFromSources(observations)); err != nil {
		return nil, err
	}
	updates := make([]Update, 0, len(observations))
	for index := range observations {
		observation := &observations[index]
		item := j.getOrCreate(observation.outcome.InputID)
		disposition := DispositionAccepted
		switch {
		case item.source == nil:
			item.source = observation
			j.validateStoredDecisions(item)
		case item.source.fingerprint == observation.fingerprint:
			disposition = DispositionReplay
		default:
			item.sourceConflict = true
			disposition = DispositionConflict
		}
		updates = append(updates, Update{InputID: observation.outcome.InputID, Disposition: disposition})
	}
	return updates, nil
}

func (j *Joiner) ObserveDecisionBatch(runEpoch string, side DecisionSide, key, payload []byte) ([]Update, error) {
	if side != DecisionSideGo && side != DecisionSidePython {
		return nil, fmt.Errorf("comparator: unknown decision side")
	}
	batch, err := contract.DecodeTriggerDecisionBatch(payload)
	if err != nil {
		return nil, err
	}
	expectedKey, err := batch.PartitionKey()
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(key, expectedKey) {
		return nil, fmt.Errorf("comparator: decision partition key mismatch")
	}
	identity := decisionBatchIdentity{
		PartitionHashVersion: batch.PartitionHashVersion,
		TenantID:             batch.TenantID,
		Purpose:              batch.Purpose,
		StrategyRef:          batch.StrategyRef,
		DecisionAlgorithm:    batch.DecisionAlgorithm,
	}
	observations := make([]decisionObservation, len(batch.Decisions))
	for index, decision := range batch.Decisions {
		cloned := cloneDecision(decision)
		fingerprint, err := semanticFingerprint(struct {
			Batch    decisionBatchIdentity    `json:"batch"`
			Decision contract.TriggerDecision `json:"decision"`
		}{Batch: identity, Decision: cloned})
		if err != nil {
			return nil, err
		}
		observations[index] = decisionObservation{
			batch:       identity,
			decision:    cloned,
			fingerprint: fingerprint,
		}
	}

	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.requireEpoch(runEpoch); err != nil {
		return nil, err
	}
	if err := j.requireCapacity(inputIDsFromDecisions(observations)); err != nil {
		return nil, err
	}
	updates := make([]Update, 0, len(observations))
	for index := range observations {
		observation := &observations[index]
		item := j.getOrCreate(observation.decision.InputID)
		disposition := j.storeDecision(item, side, observation)
		updates = append(updates, Update{InputID: observation.decision.InputID, Disposition: disposition})
	}
	return updates, nil
}

func (j *Joiner) Assess(runEpoch, inputID string, gates Gates) (Assessment, bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.requireEpoch(runEpoch); err != nil {
		return Assessment{}, false, err
	}
	item, ok := j.entries[inputID]
	if !ok {
		return Assessment{}, false, nil
	}
	assessment, err := assessEntry(inputID, item, gates)
	return assessment, true, err
}

func (j *Joiner) auditObservation(runEpoch, inputID string, gates Gates) (auditObservation, bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.requireEpoch(runEpoch); err != nil {
		return auditObservation{}, false, err
	}
	item, ok := j.entries[inputID]
	if !ok || item.source == nil {
		return auditObservation{}, false, nil
	}
	assessment, err := assessEntry(inputID, item, gates)
	if err != nil {
		return auditObservation{}, false, err
	}
	return auditObservation{
		assessment:        assessment,
		input:             item.source.input,
		outcome:           *item.source.outcome,
		sourceFingerprint: item.source.fingerprint,
		goDecision:        cloneDecisionObservation(item.goDecision),
		pythonDecision:    cloneDecisionObservation(item.pythonDecision),
	}, true, nil
}

func (j *Joiner) firstSourceTime(runEpoch string, inputIDs []string) (int64, bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.requireEpoch(runEpoch); err != nil {
		return 0, false, err
	}
	var first int64
	found := false
	for _, inputID := range inputIDs {
		item := j.entries[inputID]
		if item == nil || item.source == nil {
			continue
		}
		sourceTime := item.source.outcome.Record.SourceTime
		if !found || sourceTime < first {
			first, found = sourceTime, true
		}
	}
	return first, found, nil
}

func assessEntry(inputID string, item *entry, gates Gates) (Assessment, error) {
	assessment := Assessment{
		InputID:        inputID,
		Authoritative:  item.source != nil,
		HasGo:          item.goDecision != nil,
		HasPython:      item.pythonDecision != nil,
		SourceConflict: item.sourceConflict,
		GoConflict:     item.goConflict,
		PythonConflict: item.pythonConflict,
		GoInvalid:      item.goInvalid,
		PythonInvalid:  item.pythonInvalid,
	}
	if item.goDecision != nil {
		assessment.DecisionID = item.goDecision.decision.DecisionID
	} else if item.pythonDecision != nil {
		assessment.DecisionID = item.pythonDecision.decision.DecisionID
	}
	switch {
	case item.sourceConflict || item.goConflict || item.pythonConflict:
		assessment.Join = JoinConflict
	case item.goInvalid || item.pythonInvalid:
		assessment.Join = JoinInvalid
	case item.source == nil:
		assessment.Join = JoinPendingInput
	case item.goDecision == nil && item.pythonDecision == nil:
		assessment.Join = JoinPendingBoth
	case item.goDecision == nil:
		assessment.Join = JoinPendingGo
	case item.pythonDecision == nil:
		assessment.Join = JoinPendingPython
	default:
		assessment.Join = JoinComplete
	}
	if assessment.Join != JoinComplete {
		return assessment, nil
	}
	eligibilityValue, err := eligibility(item.source, gates)
	if err != nil {
		return Assessment{}, err
	}
	assessment.Eligibility = eligibilityValue
	if assessment.Eligibility != EligibilityEligible {
		return assessment, nil
	}
	if item.goDecision.fingerprint == item.pythonDecision.fingerprint {
		assessment.Verdict = VerdictMatch
	} else {
		assessment.Verdict = VerdictHardDiff
	}
	return assessment, nil
}

func (j *Joiner) requireEpoch(runEpoch string) error {
	if runEpoch != j.runEpoch {
		return fmt.Errorf("comparator: run epoch mismatch")
	}
	return nil
}

func (j *Joiner) requireCapacity(inputIDs []string) error {
	newEntries := 0
	for _, inputID := range inputIDs {
		if _, exists := j.entries[inputID]; !exists {
			newEntries++
		}
	}
	if len(j.entries)+newEntries > j.maxEntries {
		return fmt.Errorf("comparator: entry capacity exceeded")
	}
	return nil
}

func (j *Joiner) getOrCreate(inputID string) *entry {
	item := j.entries[inputID]
	if item == nil {
		item = &entry{}
		j.entries[inputID] = item
	}
	return item
}

func (j *Joiner) storeDecision(item *entry, side DecisionSide, observation *decisionObservation) Disposition {
	var current **decisionObservation
	var conflict *bool
	var invalid *bool
	if side == DecisionSideGo {
		current, conflict, invalid = &item.goDecision, &item.goConflict, &item.goInvalid
	} else {
		current, conflict, invalid = &item.pythonDecision, &item.pythonConflict, &item.pythonInvalid
	}
	if *current != nil {
		if (*current).fingerprint == observation.fingerprint {
			return DispositionReplay
		}
		*conflict = true
		return DispositionConflict
	}
	*current = observation
	if item.source != nil && validateDecisionAgainstInput(item.source, observation) != nil {
		*invalid = true
		return DispositionInvalid
	}
	return DispositionAccepted
}

func (j *Joiner) validateStoredDecisions(item *entry) {
	if item.goDecision != nil && validateDecisionAgainstInput(item.source, item.goDecision) != nil {
		item.goInvalid = true
	}
	if item.pythonDecision != nil && validateDecisionAgainstInput(item.source, item.pythonDecision) != nil {
		item.pythonInvalid = true
	}
}

func validateDecisionAgainstInput(source *sourceObservation, observation *decisionObservation) error {
	strategy := source.input.StrategyIR
	want := decisionBatchIdentity{
		PartitionHashVersion: source.input.PartitionHashVersion,
		TenantID:             strategy.TenantID,
		Purpose:              strategy.Purpose,
		StrategyRef:          strategy.StrategyRef,
		DecisionAlgorithm:    contract.DecisionAlgorithmV1,
	}
	if observation.batch != want {
		return fmt.Errorf("comparator: decision envelope contradicts authoritative input")
	}
	return source.input.ValidateTriggerDecision(observation.decision)
}

func eligibility(source *sourceObservation, gates Gates) (Eligibility, error) {
	switch source.outcome.Outcome {
	case contract.OutcomeError:
		return EligibilitySourceError, nil
	case contract.OutcomeUnsupported:
		return EligibilityUnsupported, nil
	}
	if !gates.StableEpoch {
		return EligibilityEpochUnstable, nil
	}
	if !gates.CoverageComplete {
		return EligibilityCoverageGap, nil
	}
	if source.outcome.Outcome == contract.OutcomeAnomalous {
		if gates.EpochStartSourceTime == nil {
			return EligibilityNone, fmt.Errorf("comparator: epoch start source time is required for anomalous input")
		}
		epochStart := *gates.EpochStartSourceTime
		if epochStart < 0 {
			return EligibilityNone, fmt.Errorf("comparator: epoch start source time must be non-negative")
		}
		if source.maxWindow <= 0 || epochStart > math.MaxInt64-(source.maxWindow-1) {
			return EligibilityNone, fmt.Errorf("comparator: warmup boundary exceeds int64")
		}
		warmupEnd := epochStart + source.maxWindow - 1
		if source.outcome.Record.SourceTime < warmupEnd {
			return EligibilityWarmup, nil
		}
	}
	return EligibilityEligible, nil
}

func maximumWindow(strategy *contract.TriggerStrategyIR) int64 {
	var maximum int64
	for _, config := range strategy.TriggerConfigs {
		window := int64(strategy.CheckWindowUnitSeconds) * int64(config.CheckWindowSize)
		if window > maximum {
			maximum = window
		}
	}
	return maximum
}

func sourceFingerprint(input *contract.TriggerInput, outcome *contract.DetectionOutcome) ([sha256.Size]byte, error) {
	cloned := *outcome
	cloned.BatchID = ""
	return semanticFingerprint(struct {
		PartitionHashVersion string                      `json:"partition_hash_version"`
		StrategyIR           *contract.TriggerStrategyIR `json:"strategy_ir"`
		Outcome              contract.DetectionOutcome   `json:"outcome"`
	}{
		PartitionHashVersion: input.PartitionHashVersion,
		StrategyIR:           input.StrategyIR,
		Outcome:              cloned,
	})
}

func semanticFingerprint(value any) ([sha256.Size]byte, error) {
	var zero [sha256.Size]byte
	payload, err := json.Marshal(value)
	if err != nil {
		return zero, fmt.Errorf("comparator: encode fingerprint: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		return zero, fmt.Errorf("comparator: normalize fingerprint: %w", err)
	}
	canonical, err := json.Marshal(normalized)
	if err != nil {
		return zero, fmt.Errorf("comparator: encode normalized fingerprint: %w", err)
	}
	return sha256.Sum256(canonical), nil
}

func cloneDecision(decision contract.TriggerDecision) contract.TriggerDecision {
	cloned := decision
	if decision.Level != nil {
		level := *decision.Level
		cloned.Level = &level
	}
	cloned.AnomalyTimestamps = append([]int64{}, decision.AnomalyTimestamps...)
	return cloned
}

func cloneDecisionObservation(observation *decisionObservation) *decisionObservation {
	if observation == nil {
		return nil
	}
	cloned := *observation
	cloned.decision = cloneDecision(observation.decision)
	return &cloned
}

func inputIDsFromSources(observations []sourceObservation) []string {
	inputIDs := make([]string, 0, len(observations))
	for index := range observations {
		inputIDs = append(inputIDs, observations[index].outcome.InputID)
	}
	return inputIDs
}

func inputIDsFromDecisions(observations []decisionObservation) []string {
	inputIDs := make([]string, 0, len(observations))
	for index := range observations {
		inputIDs = append(inputIDs, observations[index].decision.InputID)
	}
	return inputIDs
}
