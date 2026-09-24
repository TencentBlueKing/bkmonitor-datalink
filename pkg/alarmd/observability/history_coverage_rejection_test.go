// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"bytes"
	"context"
	"log/slog"
	"reflect"
	"strings"
	"testing"
)

// soundCoverage is a fact set every rule accepts, with one named window.
func soundCoverage() *HistoryCoverageFacts {
	return &HistoryCoverageFacts{Levels: 3, Short: 1, WorstValid: 6, WorstRequired: 9, Guarded: 1, Fresh: 1, ShortFresh: 1,
		Windows: []HistoryWindowFact{namedWindow()}}
}

// Every rule normalize refuses under has a name, and each fixture here breaks
// exactly one of them: the rejection names that rule, a rule about one window
// names the window's series, and the counts do not travel with the shell.
// The table is checked against the closed list both ways, so a rule added to
// one and not the other goes red.
func TestEveryCoverageRejectionHasItsOwnRule(t *testing.T) {
	t.Parallel()
	breakers := map[CoverageRejectionRule]func(*HistoryCoverageFacts){
		// Zero windows with something counted on them; a set that is zero
		// everywhere is a run that summarised nothing, and is not refused.
		CoverageRejectLevelsZero:             func(f *HistoryCoverageFacts) { f.Levels = 0 },
		CoverageRejectShortOverLevels:        func(f *HistoryCoverageFacts) { f.Short = f.Levels + 1 },
		CoverageRejectEmptyOverShort:         func(f *HistoryCoverageFacts) { f.Empty = f.Short + 1 },
		CoverageRejectGuardedOverLevels:      func(f *HistoryCoverageFacts) { f.Guarded = f.Levels + 1 },
		CoverageRejectFreshOverLevels:        func(f *HistoryCoverageFacts) { f.Fresh = f.Levels + 1 },
		CoverageRejectShortFreshOverShort:    func(f *HistoryCoverageFacts) { f.Fresh, f.ShortFresh = 3, 2 },
		CoverageRejectShortFreshOverFresh:    func(f *HistoryCoverageFacts) { f.Fresh, f.ShortFresh, f.Short = 0, 1, 1 },
		CoverageRejectUnusableOverLevels:     func(f *HistoryCoverageFacts) { f.Unusable, f.UnusableReason = f.Levels+1, "UNAVAILABLE" },
		CoverageRejectUnusableReasonUnpaired: func(f *HistoryCoverageFacts) { f.Unusable, f.UnusableReason = 1, "" },
		CoverageRejectEmptyWithValidPoints:   func(f *HistoryCoverageFacts) { f.Empty, f.WorstValid = 1, 6 },
		CoverageRejectWindowsOverShort:       func(f *HistoryCoverageFacts) { f.Short, f.ShortFresh = 0, 0 },
		CoverageRejectWindowsOverBound: func(f *HistoryCoverageFacts) {
			f.Levels, f.Short, f.Fresh, f.ShortFresh = 20, 20, 0, 0
			for len(f.Windows) <= MaxHistoryWindows {
				f.Windows = append(f.Windows, namedWindow())
			}
		},
		CoverageRejectWindowUnnamed:         func(f *HistoryCoverageFacts) { f.Windows[0].Series = "" },
		CoverageRejectWindowRequiredZero:    func(f *HistoryCoverageFacts) { f.Windows[0].Required = 0 },
		CoverageRejectWindowNotShort:        func(f *HistoryCoverageFacts) { f.Windows[0].Valid = f.Windows[0].Required },
		CoverageRejectWindowHoleArithmetic:  func(f *HistoryCoverageFacts) { f.Windows[0].MissingTotal++ },
		CoverageRejectWindowHoleListOverrun: func(f *HistoryCoverageFacts) { f.Windows[0].Missing = append(f.Windows[0].Missing, 60, 0, -60) },
		CoverageRejectWindowGuardReasonFree: func(f *HistoryCoverageFacts) { f.Windows[0].Guarded = false },
	}
	if len(breakers) != len(CoverageRejectionRules) {
		t.Fatalf("%d breakers for %d rules: every rule in the closed list needs a fixture", len(breakers), len(CoverageRejectionRules))
	}
	for _, rule := range CoverageRejectionRules {
		breaker, listed := breakers[rule]
		if !listed {
			t.Fatalf("rule %s has no fixture", rule)
		}
		facts := soundCoverage()
		breaker(facts)
		got, rejected := normalizeHistoryCoverageFacts(facts)
		if got != nil || rejected == nil {
			t.Fatalf("%s: facts survived as %+v, rejection %+v", rule, got, rejected)
		}
		if rejected.Rule != rule {
			t.Errorf("fixture for %s was refused under %s: the fixture trips another rule first, or the rule is misnamed", rule, rejected.Rule)
		}
		// A rule about one window names the window's series -- except the
		// rule that the window has none, which has nothing to name.
		if (isWindowRule(rule) && rule != CoverageRejectWindowUnnamed) != (rejected.Series != "") {
			t.Errorf("%s: series %q, want it named exactly for the rules about one named window", rule, rejected.Series)
		}
	}
	// The accepted set carries no rejection, and a nil set neither.
	if got, rejected := normalizeHistoryCoverageFacts(soundCoverage()); got == nil || rejected != nil {
		t.Fatalf("sound facts: %+v / %+v", got, rejected)
	}
	if got, rejected := normalizeHistoryCoverageFacts(nil); got != nil || rejected != nil {
		t.Fatalf("nil facts: %+v / %+v, want neither facts nor a rejection", got, rejected)
	}
	if got, rejected := normalizeHistoryCoverageFacts(&HistoryCoverageFacts{}); got != nil || rejected != nil {
		t.Fatalf("all-zero facts: %+v / %+v, want dropped as no coverage, not refused", got, rejected)
	}
}

// The "summarised nothing" exit is the one place normalize drops a set without
// naming a rule, so it must hold only for a set that is zero in every field.
// Each field of the struct is set alone and fed through: a set with one thing
// in it is either accepted or refused, never dropped as no coverage. The
// fields come from the struct itself, so a field added to it and not to
// reportsNothing goes red here instead of falling through the exit -- as six
// did once: a reason with no unusable Level rode out as "no coverage", and the
// page showed every window full.
func TestNoSingleFieldFactSetIsSwallowedAsNoCoverage(t *testing.T) {
	t.Parallel()
	structType := reflect.TypeOf(HistoryCoverageFacts{})
	for index := 0; index < structType.NumField(); index++ {
		name := structType.Field(index).Name
		value := reflect.New(structType).Elem()
		target := value.Field(index)
		switch target.Kind() {
		case reflect.Uint32:
			target.SetUint(3)
		case reflect.Int64:
			target.SetInt(1_788_000_000)
		case reflect.String:
			target.SetString("QUERY_REFUSED")
		case reflect.Slice:
			target.Set(reflect.ValueOf([]HistoryWindowFact{namedWindow()}))
		default:
			t.Fatalf("%s: no lone value for kind %s; give the kind one here so the field is checked", name, target.Kind())
		}
		facts := value.Interface().(HistoryCoverageFacts)
		if kept, rejected := normalizeHistoryCoverageFacts(&facts); kept == nil && rejected == nil {
			t.Errorf("%s alone was swallowed as no coverage: reportsNothing does not read it", name)
		}
	}
}

func isWindowRule(rule CoverageRejectionRule) bool {
	switch rule {
	case CoverageRejectWindowUnnamed, CoverageRejectWindowRequiredZero, CoverageRejectWindowNotShort,
		CoverageRejectWindowHoleArithmetic, CoverageRejectWindowHoleListOverrun, CoverageRejectWindowGuardReasonFree:
		return true
	}
	return false
}

// The rejection rides the observation where the facts would have: normalize
// clears the facts and sets the rejection, and an accepted set sets neither.
func TestARefusedCoverageLeavesItsRuleOnTheObservation(t *testing.T) {
	t.Parallel()
	refused := NormalizeObservation(Observation{Component: ComponentScheduler, Stage: StageProgressCommitted, Result: ResultSuccess,
		HistoryCoverage: &HistoryCoverageFacts{Levels: 2, Short: 3}})
	if refused.HistoryCoverage != nil || refused.HistoryCoverageRejected == nil || refused.HistoryCoverageRejected.Rule != CoverageRejectShortOverLevels {
		t.Fatalf("refused observation carries coverage %+v rejection %+v", refused.HistoryCoverage, refused.HistoryCoverageRejected)
	}
	accepted := NormalizeObservation(Observation{Component: ComponentScheduler, Stage: StageProgressCommitted, Result: ResultSuccess,
		HistoryCoverage: soundCoverage()})
	if accepted.HistoryCoverage == nil || accepted.HistoryCoverageRejected != nil {
		t.Fatalf("accepted observation carries coverage %+v rejection %+v", accepted.HistoryCoverage, accepted.HistoryCoverageRejected)
	}
}

// A run that summarised no window and says why is accepted, not refused. It
// is the reading the two counts exist for -- every series resumed, or none of
// their State loadable -- and LEVELS_ZERO, which names a zero-window set that
// counts something on those windows, would otherwise refuse it.
//
// Asserted as accepted and not merely as "not swallowed": the rule that walks
// every field only says a set is not dropped, and a refusal is not a drop.
func TestARunThatSummarisedNothingAndSaysWhyIsAccepted(t *testing.T) {
	for name, facts := range map[string]HistoryCoverageFacts{
		"every series resumed":         {Resumed: 227},
		"no State loadable":            {Constrained: 249},
		"both, and nothing summarised": {Resumed: 12, Constrained: 7},
		"some summarised, most not":    {Levels: 22, Short: 1, WorstValid: 6, WorstRequired: 9, Resumed: 227},
	} {
		in := facts
		kept, rejected := normalizeHistoryCoverageFacts(&in)
		if rejected != nil {
			t.Errorf("%s: refused under %s, want the reading kept", name, rejected.Rule)
			continue
		}
		if kept == nil {
			t.Errorf("%s: dropped as no coverage, want the reading kept", name)
			continue
		}
		if kept.Resumed != facts.Resumed || kept.Constrained != facts.Constrained {
			t.Errorf("%s: kept = %+v, want the counts carried through", name, *kept)
		}
	}
	// A zero-window set that counts something on windows it says it did not
	// summarise is still the contradiction LEVELS_ZERO names, whatever the
	// two counts say beside it.
	contradiction := HistoryCoverageFacts{Resumed: 5, Short: 2}
	if _, rejected := normalizeHistoryCoverageFacts(&contradiction); rejected == nil ||
		rejected.Rule != CoverageRejectLevelsZero {
		t.Errorf("a short count with no window summarised was not refused under LEVELS_ZERO: %+v", rejected)
	}
}

// A round that summarised no window still says how much of its object it
// described. The counts go on the line under their own condition, because the
// block that carries the window facts is gated on a named window -- and the
// round these counts exist for has none, so it is silent there by
// construction.
func TestARoundThatSummarisedNoWindowStillSaysWhyOnTheLine(t *testing.T) {
	line := func(facts *HistoryCoverageFacts) string {
		buffer := &bytes.Buffer{}
		logger := &Logger{component: "worker", next: slog.New(slog.NewTextHandler(buffer, nil))}
		logger.logObservation(context.Background(), Observation{
			Component: ComponentEvaluation, Stage: StageSlotCompleted, Result: ResultSuccess,
			HistoryCoverage: facts,
		}, LogAdmission{})
		return buffer.String()
	}
	described := line(&HistoryCoverageFacts{Resumed: 227})
	for _, want := range []string{"history_resumed=227", "history_levels=0"} {
		if !strings.Contains(described, want) {
			t.Errorf("the line lacks %q:\n%s", want, described)
		}
	}
	// The other count is its own statement -- not "they were all carried
	// forward" but "not one of them could be read" -- and a condition written
	// on only the first would still pass every case above it.
	unreadable := line(&HistoryCoverageFacts{Constrained: 249})
	for _, want := range []string{"history_constrained=249", "history_levels=0"} {
		if !strings.Contains(unreadable, want) {
			t.Errorf("the line lacks %q:\n%s", want, unreadable)
		}
	}
	// A round with nothing to explain does not carry the pair at all, and
	// still carries its named window. The two blocks are independent: this
	// pins that, so folding them back together goes red rather than taking
	// the window facts down with it.
	quiet := line(&HistoryCoverageFacts{Levels: 9, Short: 1,
		Windows: []HistoryWindowFact{namedWindow()}})
	if strings.Contains(quiet, "history_resumed") || strings.Contains(quiet, "history_constrained") {
		t.Errorf("a round that summarised every window carries the pair:\n%s", quiet)
	}
	if !strings.Contains(quiet, "history_worst_series") {
		t.Errorf("the ordinary round lost its named window:\n%s", quiet)
	}
}
