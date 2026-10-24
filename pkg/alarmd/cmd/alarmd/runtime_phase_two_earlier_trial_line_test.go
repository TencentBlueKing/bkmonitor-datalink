// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lookback"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/readhold"
)

// unusedControl is a readhold.Control the line never reaches: a group the
// controller has not read is reported without a pending hold.
type unusedControl struct{ readhold.Control }

// A trial overtaken by a moved hold has the hold it was reserved under, the
// hold its Slot froze at and the hold pending on its earlier_trial line, so
// a descent a step per Slot is told apart from any other move without a
// second read; a trial ended otherwise has none of them.
func TestTheEarlierTrialLineOfAMovedHoldHasItsHolds(t *testing.T) {
	var out bytes.Buffer
	holds := &productionReadHolds{logger: observability.New("alarmd", &out)}
	var err error
	holds.controller, err = readhold.NewController(readhold.Options{Control: unusedControl{}, Prefix: "schedule", MaxHold: 10 * time.Minute,
		Owner: func(execution.QueryGroupIdentity) (readhold.Owner, error) { return readhold.Owner{}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	var options lookback.Options
	holds.bindLookback(&options)
	// The probation after a lowering is resumed from the controller's record.
	if options.LastLowering == nil {
		t.Fatal("the lookback is not told where a group taken over was lowered")
	}
	if _, lowered := options.LastLowering("qg"); lowered {
		t.Fatal("a group whose record is not loaded reads as lowered")
	}
	contract := execution.FrozenExecutionContractRef{Slot: execution.SlotIdentity{QueryGroup: "qg", EvaluationTime: 180}, ReadHoldMillis: 300_000}
	options.OnEarlierTrialEnd(lookback.EarlierTrialEnd{Contract: contract, Outcome: lookback.EarlierOvertaken, OvertakenBy: lookback.OvertakenHoldMoved,
		CandidateHold: 150 * time.Second, AheadSlots: 4, ReservedHold: 5 * time.Minute, FrozenHold: 9 * time.Minute})
	options.OnEarlierTrialEnd(lookback.EarlierTrialEnd{Contract: contract, Outcome: lookback.EarlierEqual, CandidateHold: 150 * time.Second})
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines %q, want two", lines)
	}
	for _, field := range []string{`"reserved_hold_ms":300000`, `"frozen_hold_ms":540000`, `"pending_hold_ms":-1`} {
		if !strings.Contains(lines[0], field) {
			t.Errorf("the hold_moved line %s lacks %s", lines[0], field)
		}
	}
	if strings.Contains(lines[1], "reserved_hold_ms") || strings.Contains(lines[1], "frozen_hold_ms") {
		t.Errorf("a matched trial's line %s has the moved hold's fields", lines[1])
	}
}

// A probe's end line names what it read - the trials' candidate (verify), a midpoint or the floor - beside the hold
// it read at and the clean rounds it needed, so a lowering's verification is read from the line alone.
func TestTheFloorProbeLineNamesItsKind(t *testing.T) {
	var out bytes.Buffer
	holds := &productionReadHolds{logger: observability.New("alarmd", &out)}
	var err error
	holds.controller, err = readhold.NewController(readhold.Options{Control: unusedControl{}, Prefix: "schedule", MaxHold: 10 * time.Minute,
		Owner: func(execution.QueryGroupIdentity) (readhold.Owner, error) { return readhold.Owner{}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	var options lookback.Options
	holds.bindLookback(&options)
	contract := execution.FrozenExecutionContractRef{Slot: execution.SlotIdentity{QueryGroup: "qg", EvaluationTime: 180}, ReadHoldMillis: 300_000}
	for _, kind := range lookback.FloorProbeKinds {
		options.OnFloorProbe(lookback.FloorProbeEnd{Contract: contract, Source: "time_series", Event: lookback.FloorProbeHoldMoved,
			Floor: 150 * time.Second, Kind: kind, Hold: 5 * time.Minute, Clean: 7, Rounds: 120, Started: 120})
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != len(lookback.FloorProbeKinds) {
		t.Fatalf("lines %q, want one per probe end", lines)
	}
	for index, kind := range lookback.FloorProbeKinds {
		for _, field := range []string{`"kind":"` + kind + `"`, `"probe_ms":150000`, `"read_hold_ms":300000`, `"rounds_required":120`} {
			if !strings.Contains(lines[index], field) {
				t.Errorf("the %s probe's line %s lacks %s", kind, lines[index], field)
			}
		}
	}
	// The kinds the line names are the controller's.
	if fmt.Sprint(lookback.FloorProbeKinds) != fmt.Sprint(readhold.FloorProbeKinds) {
		t.Fatalf("lookback kinds %v, controller kinds %v", lookback.FloorProbeKinds, readhold.FloorProbeKinds)
	}
	// A probe's start says the clean rounds it went on from, and the lookback hands its rounds on to be kept as
	// often as the controller keeps them.
	out.Reset()
	if options.FloorProbeResume == nil || options.OnFloorProbeProgress == nil {
		t.Fatal("the lookback is not told how far a probe had read, nor hands on its rounds")
	}
	options.OnFloorProbeStart(lookback.FloorProbeStart{Contract: contract, Source: "time_series", Kind: "verify", Floor: 150 * time.Second,
		Hold: 5 * time.Minute, Rounds: 480, Started: 120, ResumedFrom: 30})
	for _, field := range []string{`"floor_probe_started"`, `"kind":"verify"`, `"resumed_from":30`, `"rounds_required":480`, `"started_slot":120`} {
		if !strings.Contains(out.String(), field) {
			t.Errorf("the start line %s lacks %s", out.String(), field)
		}
	}
	if lookback.FloorProbeProgressEvery != readhold.ProbeProgressEvery {
		t.Fatalf("the lookback hands on every %d rounds, the controller keeps every %d", lookback.FloorProbeProgressEvery,
			readhold.ProbeProgressEvery)
	}
}

// A round found read early is a line with what it decided - its events, state mutations, its Levels' outcomes and
// the strategies it evaluated - or, not kept, why, with the Query Group's strategies from its schedule when that can
// be read.
func TestTheReadEarlyLineSaysWhatTheRoundDecided(t *testing.T) {
	var out bytes.Buffer
	holds := &productionReadHolds{logger: observability.New("alarmd", &out)}
	var err error
	holds.controller, err = readhold.NewController(readhold.Options{Control: unusedControl{}, Prefix: "schedule", MaxHold: 10 * time.Minute,
		Owner: func(execution.QueryGroupIdentity) (readhold.Owner, error) { return readhold.Owner{}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	var options lookback.Options
	holds.bindLookback(&options)
	contract := execution.FrozenExecutionContractRef{Slot: execution.SlotIdentity{QueryGroup: "qg", EvaluationTime: 180}, ReadHoldMillis: 60_000}
	options.OnWindowReadEarly(lookback.WindowReadEarly{Contract: contract, Source: "time_series", Path: lookback.ReadEarlyPathProbation,
		Hold: time.Minute, Rung: "x1.5", Changes: map[string]int{lookback.ChangeSeriesChanged: 1},
		Decision: &lookback.SlotDecision{Events: 2, StateMutations: 5, Levels: map[string]uint64{"ABNORMAL": 1}, Strategies: []string{"11", "12"},
			StrategyCount: 2, Completed: true}})
	options.OnWindowReadEarly(lookback.WindowReadEarly{Contract: contract, Source: "time_series", Path: lookback.ReadEarlyPathSample,
		Hold: time.Minute, DecisionMissing: lookback.DecisionNotInProbation})
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines %q, want two", lines)
	}
	for index, fields := range [][]string{
		{`"window_read_early"`, `"path":"probation"`, `"read_hold_ms":60000`, `"lowered_from_ms":-1`, `"series_changed":1`,
			`"decision_summary":"kept"`, `"events":2`, `"state_mutations":5`, `"levels_abnormal":1`, `"strategies":"11,12"`,
			`"strategy_count":2`, `"strategies_from":"decision"`},
		{`"path":"sample"`, `"lowered_from_ms":-1`, `"decision_summary":"unavailable"`, `"decision_missing":"not_in_probation"`,
			`"strategies_from":"unavailable"`},
	} {
		for _, field := range fields {
			if !strings.Contains(lines[index], field) {
				t.Errorf("line %d %s lacks %s", index, lines[index], field)
			}
		}
	}
}

// A limit that cuts a group's hold by more Slots than a replay covers is
// logged once, with the holds and the Slots it is expected to skip; a cut a
// replay covers, or the same cut again, is not.
func TestALimitCutPastTheReplayIsLoggedOnce(t *testing.T) {
	var out bytes.Buffer
	holds := &productionReadHolds{logger: observability.New("alarmd", &out)}
	holds.cfg.PhaseTwo.Scheduler.MaxReplaySlots = 3
	cut := readhold.LimitCut{QueryGroup: "qg", Slot: 180, From: 10 * time.Minute, To: 5 * time.Minute, Period: time.Minute}
	holds.limitCut(cut)
	holds.limitCut(cut)
	holds.limitCut(readhold.LimitCut{QueryGroup: "covered", Slot: 180, From: 10 * time.Minute, To: 7 * time.Minute, Period: time.Minute})
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("lines %q, want one", lines)
	}
	for _, field := range []string{`"limit_cut"`, `"query_group":"qg"`, `"from_hold_ms":600000`, `"to_hold_ms":300000`,
		`"max_replay_slots":3`, `"expected_skipped_slots":2`} {
		if !strings.Contains(lines[0], field) {
			t.Errorf("the line %s lacks %s", lines[0], field)
		}
	}
}
