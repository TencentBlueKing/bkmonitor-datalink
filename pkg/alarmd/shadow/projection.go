// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package shadow contains only the pure G1 comparison projection used by
// cross-chain Golden tests. It does not publish, consume or retain Shadow data.
package shadow

import (
	"errors"
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

type Chain string

const (
	ChainPython Chain = "PYTHON"
	ChainGo     Chain = "GO"
)

type Subject struct {
	TenantID                string `json:"tenant_id"`
	BusinessID              string `json:"business_id"`
	StrategyID              string `json:"strategy_id"`
	DimensionIdentityDigest string `json:"dimension_identity_digest"`
	SourceTime              int64  `json:"source_time"`
}

type LevelView struct {
	LevelID  uint32 `json:"level_id"`
	Priority uint32 `json:"priority"`
	Result   string `json:"result"`
}

// EventView is an in-process comparison input, not a Kafka wire contract.
// Native identities are retained for chain-local provenance only.
type EventView struct {
	Chain                Chain       `json:"chain"`
	NativeEventID        string      `json:"native_event_id"`
	NativeSemanticDigest string      `json:"native_semantic_digest"`
	Subject              Subject     `json:"subject"`
	EventKind            string      `json:"event_kind"`
	PrimaryLevelID       uint32      `json:"primary_level_id"`
	Levels               []LevelView `json:"levels"`
}

type Projection struct {
	Chain                Chain
	NativeEventID        string
	NativeSemanticDigest string
	Subject              Subject
	EventKind            string
	Primary              LevelView
	Siblings             []LevelView
}

// Project selects exactly one strict primary projection. Every other dynamic
// Level remains a sibling diagnostic and cannot create a cross-chain missing.
func Project(view EventView) (Projection, error) {
	if view.Chain != ChainPython && view.Chain != ChainGo {
		return Projection{}, errors.New("alarmd shadow projection: unknown chain")
	}
	if view.Subject.TenantID == "" || view.Subject.BusinessID == "" || view.Subject.StrategyID == "" ||
		view.Subject.DimensionIdentityDigest == "" || view.Subject.SourceTime < 0 {
		return Projection{}, errors.New("alarmd shadow projection: incomplete subject")
	}
	if view.EventKind != contract.TriggerEventAbnormal && view.EventKind != contract.TriggerEventRecovery {
		return Projection{}, errors.New("alarmd shadow projection: unsupported event kind")
	}
	if view.PrimaryLevelID == 0 || len(view.Levels) == 0 {
		return Projection{}, errors.New("alarmd shadow projection: primary Level and Level results are required")
	}

	projection := Projection{
		Chain: view.Chain, NativeEventID: view.NativeEventID, NativeSemanticDigest: view.NativeSemanticDigest,
		Subject: view.Subject, EventKind: view.EventKind,
		Siblings: make([]LevelView, 0, len(view.Levels)-1),
	}
	seen := make(map[uint32]struct{}, len(view.Levels))
	primaryFound := false
	for _, level := range view.Levels {
		if level.LevelID == 0 {
			return Projection{}, errors.New("alarmd shadow projection: Level ID must be positive")
		}
		if _, ok := seen[level.LevelID]; ok {
			return Projection{}, fmt.Errorf("alarmd shadow projection: duplicate Level ID %d", level.LevelID)
		}
		seen[level.LevelID] = struct{}{}
		switch level.Result {
		case contract.LevelResultNormal, contract.LevelResultAbnormal, contract.LevelResultRecovery:
		default:
			return Projection{}, fmt.Errorf("alarmd shadow projection: unsupported Level result %q", level.Result)
		}
		if level.LevelID == view.PrimaryLevelID {
			projection.Primary = level
			primaryFound = true
			continue
		}
		projection.Siblings = append(projection.Siblings, level)
	}
	if !primaryFound {
		return Projection{}, errors.New("alarmd shadow projection: primary Level result is missing")
	}
	if (view.EventKind == contract.TriggerEventAbnormal && projection.Primary.Result != contract.LevelResultAbnormal) ||
		(view.EventKind == contract.TriggerEventRecovery && projection.Primary.Result != contract.LevelResultRecovery) {
		return Projection{}, errors.New("alarmd shadow projection: primary result does not match event kind")
	}
	return projection, nil
}

// ProjectTriggerEventV1 reuses the official TriggerEvent contract and only
// adapts it to the pure comparison view.
func ProjectTriggerEventV1(chain Chain, event contract.TriggerEventV1) (Projection, error) {
	if err := contract.ValidateTriggerEventV1(&event); err != nil {
		return Projection{}, fmt.Errorf("alarmd shadow projection: validate TriggerEvent: %w", err)
	}
	levels := make([]LevelView, len(event.LevelResults))
	for index, level := range event.LevelResults {
		levels[index] = LevelView{LevelID: level.LevelID, Priority: level.Priority, Result: level.Result}
	}
	return Project(EventView{
		Chain: chain, NativeEventID: event.EventID, NativeSemanticDigest: event.EventSemanticDigest,
		Subject: Subject{
			TenantID: event.TenantID, BusinessID: event.BusinessID, StrategyID: event.PlanRef.StrategyID,
			DimensionIdentityDigest: event.RecordRef.DimensionIdentityDigest, SourceTime: event.RecordRef.SourceTime,
		},
		EventKind: event.EventKind, PrimaryLevelID: event.PrimaryLevelID, Levels: levels,
	})
}

type Verdict string

const (
	VerdictMatch    Verdict = "MATCH"
	VerdictHardDiff Verdict = "HARD_DIFF"
)

type DifferenceReason string

const (
	ReasonPrimaryLevelDiff DifferenceReason = "PRIMARY_LEVEL_DIFF"
)

type Comparison struct {
	Subject Subject
	Verdict Verdict
	Reason  DifferenceReason
}

// CompareAbnormal compares one already paired subject. Chain-native event IDs
// and semantic digests are deliberately excluded from cross-chain equality.
func CompareAbnormal(python, goEvent Projection) (Comparison, error) {
	if python.Chain != ChainPython || goEvent.Chain != ChainGo {
		return Comparison{}, errors.New("alarmd shadow comparison: Python and Go projections are required")
	}
	if python.EventKind != contract.TriggerEventAbnormal || goEvent.EventKind != contract.TriggerEventAbnormal {
		return Comparison{}, errors.New("alarmd shadow comparison: ABNORMAL projections are required")
	}
	if python.Subject != goEvent.Subject {
		return Comparison{}, errors.New("alarmd shadow comparison: subject mismatch")
	}
	comparison := Comparison{Subject: python.Subject, Verdict: VerdictMatch}
	if python.Primary.LevelID != goEvent.Primary.LevelID {
		comparison.Verdict = VerdictHardDiff
		comparison.Reason = ReasonPrimaryLevelDiff
		return comparison, nil
	}
	return comparison, nil
}

type Episode struct {
	Open           bool
	Anchor         Subject
	PrimaryLevelID uint32
}

type EpisodeTransition string

const (
	EpisodeOpened    EpisodeTransition = "OPENED"
	EpisodeClosed    EpisodeTransition = "CLOSED"
	EpisodeUnchanged EpisodeTransition = "UNCHANGED"
)

// ApplyMatchedEvent is a pure value transition for Golden verification. Only
// the envelope primary participates; sibling Level results are never examined.
func ApplyMatchedEvent(current Episode, event Projection) (Episode, EpisodeTransition, error) {
	switch event.EventKind {
	case contract.TriggerEventAbnormal:
		if current.Open {
			if !sameEpisodeScope(current.Anchor, event.Subject) {
				return Episode{}, "", errors.New("alarmd shadow episode: projection belongs to another episode scope")
			}
			return current, EpisodeUnchanged, nil
		}
		return Episode{Open: true, Anchor: event.Subject, PrimaryLevelID: event.Primary.LevelID}, EpisodeOpened, nil
	case contract.TriggerEventRecovery:
		if !current.Open || !sameEpisodeScope(current.Anchor, event.Subject) || current.PrimaryLevelID != event.Primary.LevelID {
			return current, EpisodeUnchanged, nil
		}
		return Episode{}, EpisodeClosed, nil
	default:
		return Episode{}, "", errors.New("alarmd shadow episode: unsupported event kind")
	}
}

func sameEpisodeScope(left, right Subject) bool {
	return left.TenantID == right.TenantID && left.BusinessID == right.BusinessID && left.StrategyID == right.StrategyID &&
		left.DimensionIdentityDigest == right.DimensionIdentityDigest
}
