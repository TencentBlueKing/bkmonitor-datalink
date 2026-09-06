// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package contract

import "math"

type KnownCountV1 struct {
	Known bool    `json:"known"`
	Value *uint64 `json:"value,omitempty"`
}

func KnownShadowCountV1(n uint64) KnownCountV1 { return KnownCountV1{Known: true, Value: &n} }
func (c KnownCountV1) Validate() error {
	if c.Known != (c.Value != nil) {
		return invalid("shadow.count", "unknown must omit value; known must supply value")
	}
	return nil
}

type ShadowAttemptObservationV1 struct {
	ExecutionRef     string       `json:"execution_ref"`
	CurrentExecution KnownCountV1 `json:"current_execution"`
	LogicalSlot      KnownCountV1 `json:"logical_slot"`
	// This is evidence provenance, not a durable attempt counter.
	ObservedFromSlotStart     bool `json:"observed_from_slot_start"`
	ContinuousThroughTerminal bool `json:"continuous_through_terminal"`
}

type ShadowInputCoverageV1 struct {
	QueryAttempts       ShadowAttemptObservationV1 `json:"query_attempts"`
	Completion          string                     `json:"completion"`
	Series              KnownCountV1               `json:"series"`
	Records             KnownCountV1               `json:"records"`
	SelectedPlanRecords KnownCountV1               `json:"selected_plan_records"`
	SourceWindow        SourceWindowV2             `json:"source_window"`
	ResultBytesDigest   string                     `json:"result_bytes_digest"`
}

// Final outcomes count Plan x record, not Levels or Kafka envelopes.
type ShadowRecordOutcomesV1 struct {
	PrimaryAbnormal KnownCountV1 `json:"primary_abnormal"`
	PrimaryRecovery KnownCountV1 `json:"primary_recovery"`
	NoEvent         KnownCountV1 `json:"no_event"`
	Excluded        KnownCountV1 `json:"excluded"`
	Unavailable     KnownCountV1 `json:"unavailable"`
	Terminal        KnownCountV1 `json:"terminal"`
}

type ShadowLevelCoverageV1 struct {
	LevelID              uint32       `json:"level_id"`
	Selected             KnownCountV1 `json:"selected"`
	Normal               KnownCountV1 `json:"normal"`
	Abnormal             KnownCountV1 `json:"abnormal"`
	Recovery             KnownCountV1 `json:"recovery"`
	Unavailable          KnownCountV1 `json:"unavailable"`
	Terminal             KnownCountV1 `json:"terminal"`
	Excluded             KnownCountV1 `json:"excluded"`
	PythonShortCircuited KnownCountV1 `json:"python_short_circuited"`
	// Orthogonal subsets: never add these to the outcome conservation sum.
	PartialAcceptedAbnormal KnownCountV1 `json:"partial_accepted_abnormal"`
	SuppressedAbnormal      KnownCountV1 `json:"suppressed_abnormal"`
	Primary                 KnownCountV1 `json:"primary"`
	SiblingDiagnostic       KnownCountV1 `json:"sibling_diagnostic"`
	LateAfterComplete       KnownCountV1 `json:"late_after_complete"`
}

type ChainCoverageReceiptV1 struct {
	Schema           Schema                  `json:"schema"`
	RequiredFeatures []string                `json:"required_features"`
	RecordType       string                  `json:"record_type"`
	EpochID          string                  `json:"validation_epoch_id"`
	Chain            string                  `json:"chain"`
	TenantID         string                  `json:"tenant_id"`
	BusinessID       string                  `json:"business_id"`
	StrategyID       string                  `json:"strategy_id"`
	Context          ShadowContextV1         `json:"comparison_context"`
	Input            ShadowInputCoverageV1   `json:"input"`
	Records          ShadowRecordOutcomesV1  `json:"record_final_outcomes"`
	Levels           []ShadowLevelCoverageV1 `json:"level_outcomes"`
	PhysicalProduced KnownCountV1            `json:"physical_events_produced"`
	PhysicalACKed    KnownCountV1            `json:"physical_events_acked"`
	TerminalFact     bool                    `json:"terminal_fact"`
	TerminalFactRef  string                  `json:"terminal_fact_ref"`
	CoverageComplete bool                    `json:"coverage_complete"`
	GapReasons       []string                `json:"gap_reasons"`
	ReasonCounts     []ReasonCountV1         `json:"reason_counts"`
}

func shadowCountsComplete(counts ...KnownCountV1) (bool, error) {
	all := true
	for _, c := range counts {
		if err := c.Validate(); err != nil {
			return false, err
		}
		all = all && c.Known
	}
	return all, nil
}

func shadowConservation(total KnownCountV1, parts ...KnownCountV1) error {
	all, err := shadowCountsComplete(append([]KnownCountV1{total}, parts...)...)
	if err != nil {
		return err
	}
	if !all {
		return nil
	}
	var sum uint64
	for _, c := range parts {
		if *c.Value > math.MaxUint64-sum {
			return invalid("shadow.count", "sum overflow")
		}
		sum += *c.Value
	}
	if sum != *total.Value {
		return invalid("shadow.conservation", "count units do not conserve")
	}
	return nil
}

func shadowSubset(part, whole KnownCountV1) error {
	if part.Known && whole.Known && *part.Value > *whole.Value {
		return invalid("shadow.subset", "subset exceeds total")
	}
	return nil
}

func ValidateChainCoverageReceiptV1(r *ChainCoverageReceiptV1) error {
	if r == nil {
		return invalid("shadow.receipt", "nil")
	}
	if err := shadowHeader(r.Schema, r.RequiredFeatures, ChainCoverageReceiptSchemaV1, r.RecordType, ShadowCoverage); err != nil {
		return err
	}
	if r.EpochID == "" || r.TenantID == "" || r.BusinessID == "" || r.StrategyID == "" || (r.Chain != ShadowGo && r.Chain != ShadowPython) {
		return invalid("shadow.receipt", "scope required")
	}
	if err := validateShadowContext(r.Context, r.Chain); err != nil {
		return err
	}
	a := r.Input.QueryAttempts
	if a.ExecutionRef == "" {
		return invalid("shadow.attempt", "execution provenance required")
	}
	counts := []KnownCountV1{a.CurrentExecution, a.LogicalSlot, r.Input.Series, r.Input.Records, r.Input.SelectedPlanRecords, r.Records.PrimaryAbnormal, r.Records.PrimaryRecovery, r.Records.NoEvent, r.Records.Excluded, r.Records.Unavailable, r.Records.Terminal, r.PhysicalProduced, r.PhysicalACKed}
	seen := map[uint32]bool{}
	for _, l := range r.Levels {
		if l.LevelID == 0 || seen[l.LevelID] {
			return invalid("shadow.levels", "duplicate or invalid Level")
		}
		seen[l.LevelID] = true
		counts = append(counts, l.Selected, l.Normal, l.Abnormal, l.Recovery, l.Unavailable, l.Terminal, l.Excluded, l.PythonShortCircuited, l.PartialAcceptedAbnormal, l.SuppressedAbnormal, l.Primary, l.SiblingDiagnostic, l.LateAfterComplete)
	}
	all, err := shadowCountsComplete(counts...)
	if err != nil {
		return err
	}
	if a.LogicalSlot.Known && (!a.ObservedFromSlotStart || !a.ContinuousThroughTerminal || !r.TerminalFact || len(r.GapReasons) != 0) {
		return invalid("shadow.attempt", "known Slot total requires continuous terminal observation")
	}
	if err := shadowSubset(a.CurrentExecution, a.LogicalSlot); err != nil {
		return err
	}
	if r.Levels == nil || len(r.Levels) == 0 || r.GapReasons == nil || r.ReasonCounts == nil {
		return invalid("shadow.receipt", "coverage arrays required")
	}
	if r.TerminalFact != (r.TerminalFactRef != "") {
		return invalid("shadow.terminal", "fact reference required exactly when terminal")
	}
	if !sha256Pattern.MatchString(r.Input.ResultBytesDigest) && r.CoverageComplete {
		return invalid("shadow.input", "result digest required for complete coverage")
	}
	switch r.Input.Completion {
	case "FULL", "PARTIAL", "UNAVAILABLE", "QUERY_FREE":
	default:
		return invalid("shadow.input", "unsupported completion")
	}
	if r.Input.Completion == "QUERY_FREE" && (!a.CurrentExecution.Known || *a.CurrentExecution.Value != 0) {
		return invalid("shadow.query_free", "must prove zero Query requests")
	}
	if err := shadowConservation(r.Input.SelectedPlanRecords, r.Records.PrimaryAbnormal, r.Records.PrimaryRecovery, r.Records.NoEvent, r.Records.Excluded, r.Records.Unavailable, r.Records.Terminal); err != nil {
		return err
	}
	if err := shadowSubset(r.Input.SelectedPlanRecords, r.Input.Records); err != nil {
		return err
	}
	if err := shadowSubset(r.PhysicalACKed, r.PhysicalProduced); err != nil {
		return err
	}
	for _, l := range r.Levels {
		if err := shadowConservation(l.Selected, l.Normal, l.Abnormal, l.Recovery, l.Unavailable, l.Terminal, l.Excluded, l.PythonShortCircuited); err != nil {
			return err
		}
		if err := shadowSubset(l.Selected, r.Input.SelectedPlanRecords); err != nil {
			return err
		}
		for _, pair := range [][2]KnownCountV1{{l.PartialAcceptedAbnormal, l.Abnormal}, {l.SuppressedAbnormal, l.Abnormal}, {l.Primary, l.Selected}, {l.SiblingDiagnostic, l.Selected}, {l.LateAfterComplete, l.Selected}} {
			if err := shadowSubset(pair[0], pair[1]); err != nil {
				return err
			}
		}
		if r.Input.Completion != "FULL" && ((l.Normal.Known && *l.Normal.Value != 0) || (l.Recovery.Known && *l.Recovery.Value != 0)) {
			return invalid("shadow.levels", "incomplete input cannot claim NORMAL or RECOVERY")
		}
		if r.Chain == ShadowGo && l.PythonShortCircuited.Known && *l.PythonShortCircuited.Value != 0 {
			return invalid("shadow.levels", "Go cannot claim Python short circuit")
		}
	}
	if r.CoverageComplete && (!r.TerminalFact || !all || len(r.GapReasons) != 0 || *r.PhysicalACKed.Value != *r.PhysicalProduced.Value) {
		return invalid("shadow.coverage", "terminal, known conserved counts and ACKs required")
	}
	if !r.CoverageComplete && len(r.GapReasons) == 0 {
		return invalid("shadow.coverage", "incomplete receipt requires explicit gap")
	}
	return nil
}
