// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

var ErrStateInvariant = errors.New("state: runtime state invariant violated")

type LevelFactResult uint8

const (
	LevelFactUnavailable LevelFactResult = iota
	LevelFactNormal
	LevelFactAnomalous
	LevelFactError
)

type PointStatus string

const (
	PointApplied     PointStatus = "APPLIED"
	PointNoop        PointStatus = "NOOP"
	PointUnavailable PointStatus = "UNAVAILABLE"
	PointTerminal    PointStatus = "TERMINAL"
)

type HistoryCompleteness string

const (
	HistoryFull    HistoryCompleteness = "FULL"
	HistoryWarming HistoryCompleteness = "WARMING"
	HistoryGapped  HistoryCompleteness = "GAPPED"
)

type LevelRequirement struct {
	LevelID            uint32
	DetectFingerprint  string
	RequiredPoints     uint32
	RetentionPoints    uint32
	EvaluationInterval time.Duration
	LatenessTolerance  time.Duration
}

type StatePoint struct {
	RecordID   string
	SourceTime int64
	Levels     []PointLevelFact
}

type PointLevelFact struct {
	LevelID           uint32
	DetectFingerprint string
	Result            LevelFactResult
}

type PointResult struct {
	RecordID   string
	SourceTime int64
	Status     PointStatus
	ReasonCode string
	Late       bool
}

type WindowSummary struct {
	Completeness   HistoryCompleteness
	WindowStart    int64
	WindowEnd      int64
	ValidPositions uint32
	AnomalyCount   uint32
	AnomalyDigest  [32]byte
}

type HistoryView struct {
	window      *Window
	levelIndex  int
	requirement LevelRequirement
}

type pointCandidate struct {
	index        int
	recordID     string
	sourceTime   int64
	recordDigest [16]byte
	valid        []byte
	anomalous    []byte
	hasValid     bool
}

func NewWindow(requirements []LevelRequirement) (*Window, error) {
	window := &Window{}
	if err := window.Align(requirements); err != nil {
		return nil, err
	}
	window.changed = false
	return window, nil
}

func (window *Window) Align(requirements []LevelRequirement) error {
	if window == nil {
		return fmt.Errorf("state: window is required")
	}
	normalized, requirementByLevel, err := normalizeRequirements(requirements)
	if err != nil {
		return err
	}
	oldIndex := make(map[uint32]int, len(window.levels))
	for index, level := range window.levels {
		oldIndex[level.levelID] = index
	}
	newLevels := make([]levelState, len(normalized))
	for index, requirement := range normalized {
		fingerprint, decodeErr := decodeDigest32(requirement.DetectFingerprint)
		if decodeErr != nil {
			return fmt.Errorf("state: level %d fingerprint: %w", requirement.LevelID, decodeErr)
		}
		newLevels[index] = levelState{levelID: requirement.LevelID, detectFingerprint: fingerprint}
	}
	if sameLevelLayout(window.levels, newLevels) {
		window.requirements = requirementByLevel
		return nil
	}
	bitmapBytes := (len(newLevels) + 7) / 8
	newPoints := make([]pointState, 0, len(window.points))
	for _, point := range window.points {
		updated := pointState{
			sourceTime: point.sourceTime, recordDigest: point.recordDigest,
			valid: make([]byte, bitmapBytes), anomalous: make([]byte, bitmapBytes),
		}
		for newPosition, level := range newLevels {
			oldPosition, exists := oldIndex[level.levelID]
			if !exists || window.levels[oldPosition].detectFingerprint != level.detectFingerprint || !bitSet(point.valid, oldPosition) {
				continue
			}
			setBit(updated.valid, newPosition, true)
			setBit(updated.anomalous, newPosition, bitSet(point.anomalous, oldPosition))
		}
		if anyBit(updated.valid) {
			newPoints = append(newPoints, updated)
		}
	}
	window.levels = newLevels
	window.points = newPoints
	window.requirements = requirementByLevel
	window.changed = true
	return nil
}

func (window *Window) setObserver(observer Observer) {
	if window != nil {
		window.observer = observer
	}
}

func (window *Window) Apply(points []StatePoint) ([]PointResult, error) {
	return window.ApplyContext(context.Background(), points)
}

// ApplyContext is the M4 observation callpoint used by M7. It emits one
// aggregate result for the complete points slice and never emits per-point
// logs or high-cardinality identities.
func (window *Window) ApplyContext(ctx context.Context, points []StatePoint) (results []PointResult, err error) {
	started := time.Now()
	var observer Observer
	if window != nil {
		observer = window.observer
	}
	defer func() {
		observation := Observation{
			Stage: StageDependencyLoaded, Operation: OperationTransition,
			Result: OperationSucceeded, Codec: CodecNoneV1,
			TouchedPoints: len(points), Duration: time.Since(started),
		}
		if err != nil {
			observation.Result = OperationFailed
			switch {
			case errors.Is(err, ErrStateBudget):
				observation.BudgetViolations = 1
			default:
				observation.InvariantViolations = 1
			}
		} else {
			for _, result := range results {
				switch result.Status {
				case PointApplied:
					observation.AppliedPoints++
				case PointNoop:
					observation.NoopPoints++
				case PointUnavailable:
					observation.UnavailablePoints++
				case PointTerminal:
					observation.TerminalPoints++
				}
				if result.Late && (result.Status == PointApplied || result.Status == PointNoop) {
					observation.LateAcceptedPoints++
				}
				if result.ReasonCode == contract.ReasonLateOutOfWindow {
					observation.LateOutOfWindowPoints++
					observation.ReasonCode = contract.ReasonLateOutOfWindow
				}
			}
			if observation.TerminalPoints > 0 || observation.UnavailablePoints > 0 {
				observation.Result = OperationPartial
			}
		}
		observeState(ctx, observer, observation)
	}()
	if window == nil {
		return nil, fmt.Errorf("%w: nil window", ErrStateInvariant)
	}
	results = make([]PointResult, len(points))
	loadedLatest, hasLoaded := window.latestSourceTime()
	finalLatest := loadedLatest
	hasFinal := hasLoaded
	candidates := make([]pointCandidate, len(points))
	for index, point := range points {
		candidate, err := window.preparePoint(index, point)
		if err != nil {
			return nil, err
		}
		candidates[index] = candidate
		results[index] = PointResult{
			RecordID: point.RecordID, SourceTime: point.SourceTime, Status: PointUnavailable,
			Late: hasLoaded && point.SourceTime < loadedLatest,
		}
		if candidate.hasValid && (!hasFinal || candidate.sourceTime > finalLatest) {
			finalLatest = candidate.sourceTime
			hasFinal = true
		}
	}
	order := make([]int, 0, len(candidates))
	for index := range candidates {
		if candidates[index].hasValid {
			order = append(order, index)
		}
	}
	sort.SliceStable(order, func(left, right int) bool {
		return candidates[order[left]].sourceTime < candidates[order[right]].sourceTime
	})
	for _, candidateIndex := range order {
		candidate := candidates[candidateIndex]
		if window.outOfRetention(candidate, finalLatest) {
			results[candidate.index].Status = PointTerminal
			results[candidate.index].ReasonCode = contract.ReasonLateOutOfWindow
			continue
		}
		status, reason := window.applyCandidate(candidate)
		results[candidate.index].Status = status
		results[candidate.index].ReasonCode = reason
	}
	if hasFinal {
		window.prune(finalLatest)
	}
	return results, nil
}

func (window *Window) History(levelID uint32) (HistoryView, bool) {
	if window == nil {
		return HistoryView{}, false
	}
	position := sort.Search(len(window.levels), func(index int) bool { return window.levels[index].levelID >= levelID })
	if position == len(window.levels) || window.levels[position].levelID != levelID {
		return HistoryView{}, false
	}
	requirement, exists := window.requirements[levelID]
	if !exists {
		return HistoryView{}, false
	}
	return HistoryView{window: window, levelIndex: position, requirement: requirement}, true
}

func (window *Window) Changed() bool {
	return window != nil && window.changed
}

func (window *Window) MarkPersisted() {
	if window != nil {
		window.changed = false
	}
}

func (view HistoryView) Summarize(endTime int64, requiredPositions uint32) WindowSummary {
	return view.SummarizeContext(context.Background(), endTime, requiredPositions)
}

// SummarizeContext is the M4 observation callpoint used by M6. One bounded
// completeness counter is emitted per summary; Level and RuntimeKey identity
// are intentionally absent.
func (view HistoryView) SummarizeContext(ctx context.Context, endTime int64, requiredPositions uint32) (summary WindowSummary) {
	started := time.Now()
	defer func() {
		observation := Observation{
			Stage: StageDependencyLoaded, Operation: OperationSample,
			Result: OperationSucceeded, Codec: CodecNoneV1,
			Duration: time.Since(started),
		}
		switch summary.Completeness {
		case HistoryFull:
			observation.FullSummaries = 1
		case HistoryGapped:
			observation.GappedSummaries = 1
		default:
			observation.WarmingSummaries = 1
		}
		var observer Observer
		if view.window != nil {
			observer = view.window.observer
		}
		observeState(ctx, observer, observation)
	}()
	if requiredPositions == 0 {
		requiredPositions = view.requirement.RequiredPoints
	}
	interval := int64(view.requirement.EvaluationInterval / time.Second)
	summary = WindowSummary{Completeness: HistoryWarming, WindowEnd: endTime}
	if requiredPositions == 0 || requiredPositions > view.requirement.RetentionPoints || interval <= 0 || endTime < 0 ||
		uint64(requiredPositions-1) > uint64(math.MaxInt64/interval) {
		return summary
	}
	offset := int64(requiredPositions-1) * interval
	if offset > endTime {
		summary.WindowStart = 0
		return summary
	}
	summary.WindowStart = endTime - offset
	seenValid := false
	hasMissingBeforeFirst := false
	hasGapAfterFirst := false
	hash := sha256.New()
	var encoded [8]byte
	for expected := summary.WindowStart; ; expected += interval {
		point, exists := view.pointAt(expected)
		if !exists || !bitSet(point.valid, view.levelIndex) {
			if seenValid {
				hasGapAfterFirst = true
			} else {
				hasMissingBeforeFirst = true
			}
		} else {
			seenValid = true
			summary.ValidPositions++
			if bitSet(point.anomalous, view.levelIndex) {
				summary.AnomalyCount++
				binary.BigEndian.PutUint64(encoded[:], uint64(expected))
				_, _ = hash.Write(encoded[:])
			}
		}
		if expected == endTime {
			break
		}
	}
	copy(summary.AnomalyDigest[:], hash.Sum(nil))
	switch {
	case hasGapAfterFirst:
		summary.Completeness = HistoryGapped
	case hasMissingBeforeFirst || summary.ValidPositions < requiredPositions:
		summary.Completeness = HistoryWarming
	default:
		summary.Completeness = HistoryFull
	}
	return summary
}

func (view HistoryView) CountAnomalies(fromTime, untilTime int64) uint32 {
	var count uint32
	view.ForEachAnomaly(fromTime, untilTime, func(int64) bool {
		count++
		return true
	})
	return count
}

func (view HistoryView) ForEachAnomaly(fromTime, untilTime int64, visit func(sourceTime int64) bool) {
	if visit == nil || view.window == nil || fromTime > untilTime {
		return
	}
	start := sort.Search(len(view.window.points), func(index int) bool { return view.window.points[index].sourceTime >= fromTime })
	for index := start; index < len(view.window.points); index++ {
		point := view.window.points[index]
		if point.sourceTime > untilTime {
			return
		}
		if bitSet(point.valid, view.levelIndex) && bitSet(point.anomalous, view.levelIndex) && !visit(point.sourceTime) {
			return
		}
	}
}

func normalizeRequirements(requirements []LevelRequirement) ([]LevelRequirement, map[uint32]LevelRequirement, error) {
	normalized := append([]LevelRequirement(nil), requirements...)
	sort.Slice(normalized, func(left, right int) bool { return normalized[left].LevelID < normalized[right].LevelID })
	byLevel := make(map[uint32]LevelRequirement, len(normalized))
	for index, requirement := range normalized {
		if requirement.LevelID == 0 || (index > 0 && requirement.LevelID == normalized[index-1].LevelID) {
			return nil, nil, fmt.Errorf("state: Level IDs must be positive and unique")
		}
		if _, err := decodeDigest32(requirement.DetectFingerprint); err != nil {
			return nil, nil, fmt.Errorf("state: level %d fingerprint: %w", requirement.LevelID, err)
		}
		if requirement.RequiredPoints == 0 || requirement.RetentionPoints < requirement.RequiredPoints ||
			requirement.EvaluationInterval <= 0 || requirement.EvaluationInterval%time.Second != 0 || requirement.LatenessTolerance < 0 {
			return nil, nil, fmt.Errorf("state: invalid Level %d window requirement", requirement.LevelID)
		}
		interval := int64(requirement.EvaluationInterval / time.Second)
		if uint64(requirement.RetentionPoints-1) > uint64(math.MaxInt64/interval) ||
			int64(requirement.RetentionPoints-1)*interval > math.MaxInt64-int64(requirement.LatenessTolerance/time.Second) {
			return nil, nil, fmt.Errorf("%w: Level %d retention horizon", ErrStateBudget, requirement.LevelID)
		}
		byLevel[requirement.LevelID] = requirement
	}
	return normalized, byLevel, nil
}

func sameLevelLayout(left, right []levelState) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (window *Window) preparePoint(index int, point StatePoint) (pointCandidate, error) {
	candidate := pointCandidate{index: index, recordID: point.RecordID, sourceTime: point.SourceTime}
	digest, err := decodeDigest32(point.RecordID)
	if err != nil || point.SourceTime < 0 {
		return pointCandidate{}, fmt.Errorf("%w: invalid StatePoint identity", ErrStateInvariant)
	}
	copy(candidate.recordDigest[:], digest[:16])
	bitmapBytes := (len(window.levels) + 7) / 8
	candidate.valid = make([]byte, bitmapBytes)
	candidate.anomalous = make([]byte, bitmapBytes)
	seen := make(map[uint32]struct{}, len(point.Levels))
	for _, fact := range point.Levels {
		if _, duplicate := seen[fact.LevelID]; duplicate {
			return pointCandidate{}, fmt.Errorf("%w: duplicate Level fact %d", ErrStateInvariant, fact.LevelID)
		}
		seen[fact.LevelID] = struct{}{}
		position := sort.Search(len(window.levels), func(i int) bool { return window.levels[i].levelID >= fact.LevelID })
		if position == len(window.levels) || window.levels[position].levelID != fact.LevelID {
			return pointCandidate{}, fmt.Errorf("%w: unknown Level fact %d", ErrStateInvariant, fact.LevelID)
		}
		fingerprint, decodeErr := decodeDigest32(fact.DetectFingerprint)
		if decodeErr != nil || fingerprint != window.levels[position].detectFingerprint {
			return pointCandidate{}, fmt.Errorf("%w: Level %d detect fingerprint mismatch", ErrStateInvariant, fact.LevelID)
		}
		switch fact.Result {
		case LevelFactNormal:
			setBit(candidate.valid, position, true)
			candidate.hasValid = true
		case LevelFactAnomalous:
			setBit(candidate.valid, position, true)
			setBit(candidate.anomalous, position, true)
			candidate.hasValid = true
		case LevelFactUnavailable, LevelFactError:
		default:
			return pointCandidate{}, fmt.Errorf("%w: Level %d fact result %d", ErrStateInvariant, fact.LevelID, fact.Result)
		}
	}
	return candidate, nil
}

func (window *Window) applyCandidate(candidate pointCandidate) (PointStatus, string) {
	position := sort.Search(len(window.points), func(index int) bool { return window.points[index].sourceTime >= candidate.sourceTime })
	if position < len(window.points) && window.points[position].sourceTime == candidate.sourceTime {
		point := &window.points[position]
		if point.recordDigest != candidate.recordDigest {
			return PointTerminal, contract.ReasonRecordIdentityConflict
		}
		for bit := range window.levels {
			if !bitSet(candidate.valid, bit) || !bitSet(point.valid, bit) {
				continue
			}
			if bitSet(candidate.anomalous, bit) != bitSet(point.anomalous, bit) {
				return PointTerminal, contract.ReasonRecordIdentityConflict
			}
		}
		changed := false
		for bit := range window.levels {
			if !bitSet(candidate.valid, bit) || bitSet(point.valid, bit) {
				continue
			}
			setBit(point.valid, bit, true)
			setBit(point.anomalous, bit, bitSet(candidate.anomalous, bit))
			changed = true
		}
		if !changed {
			return PointNoop, ""
		}
		window.changed = true
		return PointApplied, ""
	}
	point := pointState{
		sourceTime: candidate.sourceTime, recordDigest: candidate.recordDigest,
		valid: append([]byte(nil), candidate.valid...), anomalous: append([]byte(nil), candidate.anomalous...),
	}
	window.points = append(window.points, pointState{})
	copy(window.points[position+1:], window.points[position:])
	window.points[position] = point
	window.changed = true
	return PointApplied, ""
}

func (window *Window) outOfRetention(candidate pointCandidate, latest int64) bool {
	if candidate.sourceTime >= latest {
		return false
	}
	for bit, level := range window.levels {
		if !bitSet(candidate.valid, bit) {
			continue
		}
		requirement := window.requirements[level.levelID]
		if candidate.sourceTime >= retentionCutoff(latest, requirement) {
			return false
		}
	}
	return true
}

func (window *Window) prune(latest int64) {
	changed := false
	kept := window.points[:0]
	for _, point := range window.points {
		for bit, level := range window.levels {
			if !bitSet(point.valid, bit) || point.sourceTime >= retentionCutoff(latest, window.requirements[level.levelID]) {
				continue
			}
			setBit(point.valid, bit, false)
			setBit(point.anomalous, bit, false)
			changed = true
		}
		if anyBit(point.valid) {
			kept = append(kept, point)
		} else {
			changed = true
		}
	}
	window.points = kept
	window.changed = window.changed || changed
}

func retentionCutoff(latest int64, requirement LevelRequirement) int64 {
	interval := int64(requirement.EvaluationInterval / time.Second)
	horizon := int64(requirement.RetentionPoints-1)*interval + int64(requirement.LatenessTolerance/time.Second)
	if horizon >= latest {
		return 0
	}
	return latest - horizon
}

func (window *Window) latestSourceTime() (int64, bool) {
	if len(window.points) == 0 {
		return 0, false
	}
	return window.points[len(window.points)-1].sourceTime, true
}

func (view HistoryView) pointAt(sourceTime int64) (pointState, bool) {
	position := sort.Search(len(view.window.points), func(index int) bool { return view.window.points[index].sourceTime >= sourceTime })
	if position == len(view.window.points) || view.window.points[position].sourceTime != sourceTime {
		return pointState{}, false
	}
	return view.window.points[position], true
}

func bitSet(bitmap []byte, position int) bool {
	return position >= 0 && position/8 < len(bitmap) && bitmap[position/8]&(1<<uint(position%8)) != 0
}

func setBit(bitmap []byte, position int, value bool) {
	mask := byte(1 << uint(position%8))
	if value {
		bitmap[position/8] |= mask
	} else {
		bitmap[position/8] &^= mask
	}
}

func anyBit(bitmap []byte) bool {
	for _, value := range bitmap {
		if value != 0 {
			return true
		}
	}
	return false
}
