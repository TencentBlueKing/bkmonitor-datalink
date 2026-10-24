// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package strategy

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// windowRequirement is a time range with an inactive calendar beside it, so
// the range is kept as written rather than normalized to always.
func windowRequirement(t testing.TB, start, end string) EffectiveTimeRequirement {
	t.Helper()
	plan := validPlan()
	plan.StrategyIR.Levels[0].TriggerPlan.Config = triggerConfigWithUptime("BUSINESS_LOCAL", map[string]any{
		"time_ranges": []any{map[string]any{"start": start, "end": end}},
		"calendars":   []int64{8},
	})
	return mustCompilePlan(t, newTestCompiler(t), plan).Levels().At(0).EffectiveTimeRequirement()
}

// resolveWindowAt is the provider's answer at one instant, with the calendar
// known, not matched and valid for a day, so the answer is the schedule's.
func resolveWindowAt(t testing.TB, requirement EffectiveTimeRequirement, at time.Time) (string, time.Time) {
	t.Helper()
	source := CalendarFactSourceFunc(func(_ context.Context, requests []CalendarFactRequest) ([]CalendarFact, error) {
		facts := make([]CalendarFact, 0, len(requests))
		for _, request := range requests {
			facts = append(facts, CalendarFact{Request: request, Known: true, Matched: false,
				Revision: fmt.Sprintf("%064x", request.CalendarID), ValidFrom: at.Unix() - 60, ValidUntil: at.Unix() + 86400})
		}
		return facts, nil
	})
	facts, err := NewCalendarScheduleProvider(fixedTimezoneResolver(time.UTC), source).Resolve(context.Background(),
		[]EffectiveTimeRequest{{TenantID: "tenant-a", BusinessID: "42", EvaluationTime: at.Unix(), Requirement: requirement}})
	if err != nil || len(facts) != 1 {
		t.Fatalf("Resolve at %s = %+v, %v", at.Format(time.TimeOnly), facts, err)
	}
	return facts[0].Status(), time.Unix(facts[0].ValidUntil(), 0).UTC()
}

// A window works at minute precision and its end minute is inclusive through
// second 59: 09:00-17:59 covers 17:59:59 and not 18:00:00, and the boundary
// it gives inside its last minute is 18:00:00.
func TestAWindowEndsAfterTheLastSecondOfItsEndMinute(t *testing.T) {
	requirement := windowRequirement(t, "09:00", "17:59")
	day := func(clock string) time.Time {
		at, _ := time.Parse(time.DateTime, "2026-08-26 "+clock)
		return at
	}
	for clock, want := range map[string]string{
		"08:59:59": EffectiveTimeInactive, "09:00:00": EffectiveTimeActive,
		"17:59:59": EffectiveTimeActive, "18:00:00": EffectiveTimeInactive,
	} {
		if status, _ := resolveWindowAt(t, requirement, day(clock)); status != want {
			t.Errorf("09:00-17:59 at %s = %s, want %s", clock, status, want)
		}
	}
	if _, boundary := resolveWindowAt(t, requirement, day("17:59:30")); !boundary.Equal(day("18:00:00")) {
		t.Errorf("boundary at 17:59:30 = %s, want 18:00:00", boundary)
	}
}

// A window across midnight keeps the same rule on its end: 22:00-01:59
// covers 01:59:59 and not 02:00:00.
func TestAWindowAcrossMidnightEndsAfterItsEndMinute(t *testing.T) {
	requirement := windowRequirement(t, "22:00", "01:59")
	day := func(clock string) time.Time {
		at, _ := time.Parse(time.DateTime, "2026-08-27 "+clock)
		return at
	}
	for clock, want := range map[string]string{
		"21:59:59": EffectiveTimeInactive, "22:00:00": EffectiveTimeActive, "23:59:59": EffectiveTimeActive,
		"00:00:00": EffectiveTimeActive, "01:59:59": EffectiveTimeActive, "02:00:00": EffectiveTimeInactive,
	} {
		if status, _ := resolveWindowAt(t, requirement, day(clock)); status != want {
			t.Errorf("22:00-01:59 at %s = %s, want %s", clock, status, want)
		}
	}
	if _, boundary := resolveWindowAt(t, requirement, day("01:59:30")); !boundary.Equal(day("02:00:00")) {
		t.Errorf("boundary at 01:59:30 = %s, want 02:00:00", boundary)
	}
}

// A whole-day window kept as a range covers 23:59:59, and nothing opens a
// gap between 23:59 and 00:00.
func TestAWholeDayWindowHasNoGapAtMidnight(t *testing.T) {
	requirement := windowRequirement(t, "00:00", "23:59")
	if requirement.Kind() != EffectiveTimeCalendar || len(requirement.TimeRanges()) != 1 {
		t.Fatalf("00:00-23:59 with a calendar is %s with %d ranges, want a calendar requirement keeping its range",
			requirement.Kind(), len(requirement.TimeRanges()))
	}
	for _, at := range []time.Time{
		time.Date(2026, 8, 26, 23, 59, 0, 0, time.UTC), time.Date(2026, 8, 26, 23, 59, 59, 0, time.UTC),
		time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC), time.Date(2026, 8, 27, 0, 0, 1, 0, time.UTC),
	} {
		if status, _ := resolveWindowAt(t, requirement, at); status != EffectiveTimeActive {
			t.Errorf("00:00-23:59 at %s = %s, want active", at.Format(time.DateTime), status)
		}
	}
}
