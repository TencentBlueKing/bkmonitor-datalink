// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package execution

import (
	"errors"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The five ways a Plan result can disagree with the State and gap loads of
// its Slot each refuse under their own code, with the Plan's disposition and
// reason and the loads by kind in the text and the detail; the agreeing
// shapes pass. On the reference deployment the third one appears on the
// replica a rollout is replacing, and until now it reached the log as an
// internal error with no code.
func TestLoadedFactDispositionRefusalsNameThemselves(t *testing.T) {
	plan := PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "1001"}
	other := PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "other"}
	result := func(disposition PlanDisposition, reason ReasonCode) PlanEvaluationResult {
		return PlanEvaluationResult{Plan: plan, Disposition: disposition, ReasonCode: reason}
	}
	state := func(of PlanIdentity, status StateLoadStatus, reason ReasonCode) RuntimeStateView {
		return RuntimeStateView{Identity: StateKeyIdentity{Plan: of, StateGeneration: "gen", SeriesIdentityDigest: "series"}, Status: status, ReasonCode: reason}
	}
	gap := func(of PlanIdentity, status GapLoadStatus, reason ReasonCode) GapGuardSnapshot {
		return GapGuardSnapshot{Identity: PlanGapIdentity{Plan: of, StateGeneration: "gen"}, Status: status, ReasonCode: reason}
	}
	cases := []struct {
		name    string
		plan    PlanEvaluationResult
		states  []RuntimeStateView
		gaps    []GapGuardSnapshot
		code    string
		detail  string
		inText  []string
		refused *LoadedFactDispositionError
	}{
		{
			name: "retryable load answered by a decided Plan", plan: result(PlanDecided, "none"),
			states: []RuntimeStateView{state(plan, StateRetryableIO, "REDIS_UNAVAILABLE"), state(plan, StateRetryableIO, "REDIS_UNAVAILABLE"), state(other, StateRetryableIO, "REDIS_UNAVAILABLE")},
			gaps:   []GapGuardSnapshot{gap(plan, GapUnavailable, "SNAPSHOT_UNAVAILABLE"), gap(plan, GapFound, "")},
			code:   QueryFailureCodeRetryableLoadNotRetryPending,
			detail: "plan=decided-reason=none-states=2-gaps=1-terminal=0-load=redis_unavailable",
			inText: []string{"retryable State/Gap load requires retry-pending Plan", "strategy 1001", "DECIDED", "2 retryable State", "1 unavailable gap", "REDIS_UNAVAILABLE", "SNAPSHOT_UNAVAILABLE"},
		},
		{
			name: "retry-pending Plan with another reason", plan: result(PlanRetryPending, "QUERY_TIMEOUT"),
			states: []RuntimeStateView{state(plan, StateRetryableIO, "REDIS_UNAVAILABLE")},
			code:   QueryFailureCodeRetryPendingReasonMismatch,
			detail: "plan=retry_pending-reason=query_timeout-states=1-gaps=0-terminal=0-load=redis_unavailable",
		},
		{
			name: "retry-pending Plan with nothing retryable loaded", plan: result(PlanRetryPending, "REDIS_UNAVAILABLE"),
			states: []RuntimeStateView{state(plan, StateFoundReady, "")},
			code:   QueryFailureCodeRetryPendingWithoutRetryableLoad,
			detail: "plan=retry_pending-reason=redis_unavailable-states=0-gaps=0-terminal=0-load=none",
		},
		{
			name: "terminal load ignored by a decided Plan", plan: result(PlanDecided, "none"),
			gaps:   []GapGuardSnapshot{gap(plan, GapTerminal, "STATE_CONTRACT_INVALID")},
			code:   QueryFailureCodeTerminalLoadIgnored,
			detail: "plan=decided-reason=none-states=0-gaps=0-terminal=1-load=state_contract_invalid",
		},
		{
			name: "terminal Plan with another reason", plan: result(PlanTerminal, "PLAN_INVALID"),
			gaps:   []GapGuardSnapshot{gap(plan, GapTerminal, "STATE_CONTRACT_INVALID")},
			code:   QueryFailureCodeTerminalLoadReasonMismatch,
			detail: "plan=terminal-reason=plan_invalid-states=0-gaps=0-terminal=1-load=state_contract_invalid",
		},
	}
	for _, c := range cases {
		err := validateLoadedFactDisposition(c.plan, StatePreflightResult{Items: c.states}, GapLoadResult{Items: c.gaps})
		var refused *LoadedFactDispositionError
		if !errors.As(err, &refused) {
			t.Fatalf("%s: err = %v, want the named refusal", c.name, err)
		}
		if category, code := refused.QueryFailure(); category != "" || code != c.code {
			t.Fatalf("%s: QueryFailure() = (%q, %q), want the stage's category and %q", c.name, category, code, c.code)
		}
		if got := refused.QueryFailureDetail(); got != c.detail {
			t.Fatalf("%s: detail = %q, want %q", c.name, got, c.detail)
		}
		if !observability.ValidQueryFailureCode(c.code) {
			t.Fatalf("%s: code %q violates the log code grammar", c.name, c.code)
		}
		for _, want := range c.inText {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("%s: error text %q does not carry %q", c.name, err.Error(), want)
			}
		}
	}
	for _, c := range []struct {
		name   string
		plan   PlanEvaluationResult
		states []RuntimeStateView
		gaps   []GapGuardSnapshot
	}{
		{"nothing loaded, decided", result(PlanDecided, "none"), nil, nil},
		{"retryable load, retry-pending with its reason", result(PlanRetryPending, "REDIS_UNAVAILABLE"), []RuntimeStateView{state(plan, StateRetryableIO, "REDIS_UNAVAILABLE")}, nil},
		{"terminal load, terminal with its reason", result(PlanTerminal, "STATE_CONTRACT_INVALID"), nil, []GapGuardSnapshot{gap(plan, GapTerminal, "STATE_CONTRACT_INVALID")}},
		{"terminal load, retry-pending because a retryable load came with it", result(PlanRetryPending, "REDIS_UNAVAILABLE"), []RuntimeStateView{state(plan, StateRetryableIO, "REDIS_UNAVAILABLE")}, []GapGuardSnapshot{gap(plan, GapTerminal, "STATE_CONTRACT_INVALID")}},
		{"another Plan's loads are not this Plan's", result(PlanDecided, "none"), []RuntimeStateView{state(other, StateRetryableIO, "REDIS_UNAVAILABLE")}, []GapGuardSnapshot{gap(other, GapTerminal, "STATE_CONTRACT_INVALID")}},
	} {
		if err := validateLoadedFactDisposition(c.plan, StatePreflightResult{Items: c.states}, GapLoadResult{Items: c.gaps}); err != nil {
			t.Fatalf("%s: refused: %v", c.name, err)
		}
	}
}
