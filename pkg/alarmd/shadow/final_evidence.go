// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package shadow

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// BusinessACK is an assertion from the actual sink success boundary. Building
// evidence neither sends an event nor turns an enqueue attempt into an ACK.
type BusinessACK struct {
	EventID        string `json:"event_id"`
	SemanticDigest string `json:"semantic_digest"`
	Confirmed      bool   `json:"confirmed"`
}

type GoEvidenceInput struct {
	EpochID           string                        `json:"validation_epoch_id"`
	IdentityVersion   string                        `json:"identity_version"`
	ProjectionVersion string                        `json:"projection_version"`
	Event             contract.TriggerEventV1       `json:"event"`
	ACK               BusinessACK                   `json:"business_ack"`
	Config            contract.ComparisonConfigV1   `json:"comparison_config"`
	Context           contract.ShadowContextV1      `json:"comparison_context"`
	Completeness      contract.ShadowCompletenessV1 `json:"completeness"`
}

// BuildGoFinalEvidence is a pure, bounded copy of an already ACKed envelope.
// It intentionally does not infer business completion/State/Progress from ACK.
// The first adapter supports the shared scalar Threshold projection only;
// unsupported semantic adapters return an error rather than fabricate facts.
func BuildGoFinalEvidence(input GoEvidenceInput, maxBytes int) (*contract.FinalResultEvidenceV1, error) {
	if !input.ACK.Confirmed || input.ACK.EventID != input.Event.EventID || input.ACK.SemanticDigest != input.Event.EventSemanticDigest {
		return nil, errors.New("alarmd shadow: ACK does not identify this event")
	}
	projected, err := ProjectTriggerEventV1(ChainGo, input.Event)
	if err != nil {
		return nil, err
	}
	canonicalConfig, configDigest, err := contract.CanonicalComparisonConfigV1(input.Config)
	if err != nil {
		return nil, err
	}
	if configDigest != input.Context.ComparisonConfigDigest {
		return nil, errors.New("alarmd shadow: frozen config digest mismatch")
	}
	// Sub-projection fingerprints use the same normalized semantic object.
	if err := json.Unmarshal(canonicalConfig, &input.Config); err != nil {
		return nil, err
	}
	if len(input.Config.Levels) != len(input.Event.LevelResults) {
		return nil, errors.New("alarmd shadow: incomplete Level config closure")
	}
	var primary contract.LevelResultV1
	var configLevel contract.ShadowLevelConfigV1
	for _, native := range input.Event.LevelResults {
		found := false
		for _, level := range input.Config.Levels {
			if native.LevelID != level.LevelID {
				continue
			}
			found = true
			if native.Priority != level.Priority || native.DecisionWindow.Trigger.WindowSize != level.Trigger.WindowPoints || native.DecisionWindow.Trigger.RequiredAnomalies != level.Trigger.RequiredAnomalies || native.DecisionWindow.Recovery.Enabled != level.Recovery.Enabled || native.DecisionWindow.Recovery.RequiredConsecutiveWindows != level.Recovery.ConsecutiveWindows {
				return nil, errors.New("alarmd shadow: native window and config disagree")
			}
			if native.LevelID == projected.Primary.LevelID {
				primary = native
				configLevel = level
			}
		}
		if !found {
			return nil, errors.New("alarmd shadow: native Level missing from config")
		}
	}
	// Value is the real normalized primary evidence, never the human-readable
	// display string or an independently re-queried observation.
	raw := strings.TrimSpace(string(primary.DetectEvidence.NormalizedValue))
	if strings.HasPrefix(raw, "\"") {
		if err := json.Unmarshal(primary.DetectEvidence.NormalizedValue, &raw); err != nil {
			return nil, err
		}
	}
	value, err := contract.NormalizeShadowDecimalV1(raw)
	if err != nil {
		return nil, err
	}
	detectDigest, err := contract.DeriveCanonicalDigestV2("shadow-comparable-detect-v1", configLevel.Detectors)
	if err != nil {
		return nil, err
	}
	triggerDigest, err := contract.DeriveCanonicalDigestV2("shadow-comparable-trigger-v1", struct {
		Trigger  contract.ShadowTriggerConfigV1  `json:"trigger"`
		Recovery contract.ShadowRecoveryConfigV1 `json:"recovery"`
	}{configLevel.Trigger, configLevel.Recovery})
	if err != nil {
		return nil, err
	}
	e := &contract.FinalResultEvidenceV1{Schema: contract.Schema{Name: contract.FinalResultEvidenceSchemaV1, Major: 1}, RequiredFeatures: []string{}, RecordType: contract.ShadowFinalResult, EpochID: input.EpochID, Chain: contract.ShadowGo, ResultKind: input.Event.EventKind,
		Subject: contract.ShadowSubjectV1{IdentityVersion: input.IdentityVersion, TenantID: projected.Subject.TenantID, BusinessID: projected.Subject.BusinessID, StrategyID: projected.Subject.StrategyID, DimensionIdentityDigest: projected.Subject.DimensionIdentityDigest, SourceTime: projected.Subject.SourceTime},
		Native:  contract.ShadowNativeRefV1{EventID: input.Event.EventID, SemanticDigest: input.Event.EventSemanticDigest, LevelResults: input.Event.LevelResults},
		Primary: contract.PrimaryComparableProjectionV1{Version: input.ProjectionVersion, SelectionMappingVersion: input.Config.SelectionMappingVersion, LevelID: primary.LevelID, Priority: primary.Priority, Result: primary.Result, Values: map[string]string{"value": value}, Unit: input.Config.Numeric.TargetUnit, WindowStart: primary.DecisionWindow.Trigger.WindowStart, WindowEnd: primary.DecisionWindow.Trigger.WindowEnd, DetectConfigDigest: detectDigest, TriggerConfigDigest: triggerDigest},
		Context: input.Context, Completeness: input.Completeness, Delivery: contract.ShadowDeliveryV1{FinalAdmission: "PRODUCED", BusinessACK: true, ReasonCode: "BUSINESS_ACK_CONFIRMED"}, GoEvent: &input.Event}
	e.Primary.Digest, err = contract.ShadowProjectionDigestV1(e.Primary)
	if err != nil {
		return nil, err
	}
	e.Native.EvidenceDigest, err = contract.ShadowNativeDigestV1(e.Native)
	if err != nil {
		return nil, err
	}
	b, err := contract.EncodeFinalResultEvidenceV1(e, maxBytes)
	if err != nil {
		return nil, err
	}
	cloned, err := contract.DecodeShadowResultRecordV1(b, maxBytes)
	if err != nil {
		return nil, err
	}
	return cloned.Evidence, nil
}

// BuildCoverageReceipt accepts only caller-observed counts and existing
// terminal facts. It performs no Query, retry accounting or completion writes.
func BuildCoverageReceipt(r contract.ChainCoverageReceiptV1, maxBytes int) (*contract.ChainCoverageReceiptV1, error) {
	r.Schema = contract.Schema{Name: contract.ChainCoverageReceiptSchemaV1, Major: 1}
	r.RequiredFeatures = []string{}
	r.RecordType = contract.ShadowCoverage
	b, err := contract.EncodeChainCoverageReceiptV1(&r, maxBytes)
	if err != nil {
		return nil, err
	}
	cloned, err := contract.DecodeShadowResultRecordV1(b, maxBytes)
	if err != nil {
		return nil, err
	}
	return cloned.Receipt, nil
}
