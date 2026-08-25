// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package strategy

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestCompilerCompilesEffectiveTimeRequirements(t *testing.T) {
	compiler := newTestCompiler(t)
	always := mustCompilePlan(t, compiler, validPlan()).Levels()[0]
	if requirement := always.EffectiveTimeRequirement(); requirement.Kind() != EffectiveTimeAlways || len(requirement.Digest()) != 64 {
		t.Fatalf("ALWAYS requirement = %+v", requirement)
	}

	staticPlan := validPlan()
	staticPlan.StrategyIR.Levels[0].TriggerPlan.Config = triggerConfigWithUptime("BUSINESS_LOCAL", map[string]any{
		"time_ranges":      []any{map[string]any{"start": "09:00", "end": "17:00"}},
		"active_calendars": []any{},
		"calendars":        []any{},
	})
	staticLevel := mustCompilePlan(t, compiler, staticPlan).Levels()[0]
	static := staticLevel.EffectiveTimeRequirement()
	if static.Kind() != EffectiveTimeStaticSchedule || static.TimezoneRef() != "BUSINESS_LOCAL" || len(static.TimeRanges()) != 1 {
		t.Fatalf("STATIC_SCHEDULE requirement = %+v", static)
	}
	if staticLevel.Fingerprints() != always.Fingerprints() {
		t.Fatal("uptime requirement unexpectedly changed Trigger state fingerprint")
	}

	calendarPlan := validPlan()
	calendarPlan.StrategyIR.Levels[0].TriggerPlan.Config = triggerConfigWithUptime("BUSINESS_LOCAL", map[string]any{
		"time_ranges":      []any{},
		"active_calendars": []any{int64(9), int64(3)},
		"calendars":        []any{int64(8)},
	})
	calendar := mustCompilePlan(t, compiler, calendarPlan).Levels()[0].EffectiveTimeRequirement()
	if calendar.Kind() != EffectiveTimeCalendar || !equalInt64s(calendar.ActiveCalendarIDs(), []int64{3, 9}) || !equalInt64s(calendar.InactiveCalendarIDs(), []int64{8}) {
		t.Fatalf("CALENDAR requirement = %+v", calendar)
	}
	calendarIDs := calendar.ActiveCalendarIDs()
	calendarIDs[0] = 99
	if calendar.ActiveCalendarIDs()[0] != 3 {
		t.Fatal("caller mutation changed immutable calendar requirement")
	}
}

func TestCompilerIncludesTimeRangeContentInEffectiveTimeDigest(t *testing.T) {
	compiler := newTestCompiler(t)
	first := validPlan()
	first.StrategyIR.Levels[0].TriggerPlan.Config = triggerConfigWithUptime("BUSINESS_LOCAL", map[string]any{
		"time_ranges": []any{map[string]any{"start": "09:00", "end": "17:00"}},
	})
	second := validPlan()
	second.StrategyIR.Levels[0].TriggerPlan.Config = triggerConfigWithUptime("BUSINESS_LOCAL", map[string]any{
		"time_ranges": []any{map[string]any{"start": "10:00", "end": "18:00"}},
	})

	firstRequirement := mustCompilePlan(t, compiler, first).Levels()[0].EffectiveTimeRequirement()
	secondRequirement := mustCompilePlan(t, compiler, second).Levels()[0].EffectiveTimeRequirement()
	if firstRequirement.Digest() == secondRequirement.Digest() {
		t.Fatal("different time ranges produced the same effective-time requirement digest")
	}
	firstFact, err := newEffectiveTimeFact(EffectiveTimeActive, firstRequirement.Digest(), 100, 200)
	if err != nil {
		t.Fatalf("newEffectiveTimeFact(first) error = %v", err)
	}
	secondFact, err := newEffectiveTimeFact(EffectiveTimeActive, secondRequirement.Digest(), 100, 200)
	if err != nil {
		t.Fatalf("newEffectiveTimeFact(second) error = %v", err)
	}
	if firstFact.FactDigest() == secondFact.FactDigest() {
		t.Fatal("different time ranges produced the same effective-time fact digest")
	}
}

func TestCompilerIsolatesInvalidEffectiveTimeRequirement(t *testing.T) {
	compiler := newTestCompiler(t)
	plan := validPlan()
	plan.StrategyIR.Levels[0].TriggerPlan.Config = triggerConfigWithUptime("BUSINESS_LOCAL", map[string]any{
		"time_ranges": []any{map[string]any{"start": "25:00", "end": "17:00"}},
	})
	result := mustCompileResult(t, compiler, plan)
	if terminals := result.LevelTerminals(); len(terminals) != 1 || terminals[0].LevelID != 1 {
		t.Fatalf("invalid uptime terminals = %+v", terminals)
	}
}

func TestCompilerRequiresBusinessLocalTimezoneReference(t *testing.T) {
	plan := validPlan()
	plan.StrategyIR.Levels[0].TriggerPlan.Config = triggerConfigWithUptime("UTC", map[string]any{
		"time_ranges": []any{map[string]any{"start": "09:00", "end": "17:00"}},
	})
	result := mustCompileResult(t, newTestCompiler(t), plan)
	if terminals := result.LevelTerminals(); len(terminals) != 1 || terminals[0].ReasonCode == "" {
		t.Fatalf("invalid timezone terminals = %+v", terminals)
	}
}

func TestStaticScheduleProviderReturnsBoundedFacts(t *testing.T) {
	plan := validPlan()
	plan.StrategyIR.Levels[0].TriggerPlan.Config = triggerConfigWithUptime("BUSINESS_LOCAL", map[string]any{
		"time_ranges": []any{map[string]any{"start": "09:00", "end": "17:00"}},
	})
	requirement := mustCompilePlan(t, newTestCompiler(t), plan).Levels()[0].EffectiveTimeRequirement()
	provider := NewStaticScheduleProvider(TimezoneResolverFunc(func(_ context.Context, ref, _, _ string) (*time.Location, error) {
		if ref != "BUSINESS_LOCAL" {
			t.Fatalf("timezone ref = %q", ref)
		}
		return time.UTC, nil
	}))
	requests := []EffectiveTimeRequest{
		{TenantID: "default", BusinessID: "2", EvaluationTime: time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC).Unix(), Requirement: requirement},
		{TenantID: "default", BusinessID: "2", EvaluationTime: time.Date(2026, 8, 26, 18, 0, 0, 0, time.UTC).Unix(), Requirement: requirement},
	}
	facts, err := provider.Resolve(context.Background(), requests)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(facts) != 2 || facts[0].Status() != EffectiveTimeActive || facts[1].Status() != EffectiveTimeInactive {
		t.Fatalf("Resolve() facts = %+v", facts)
	}
	for index, fact := range facts {
		if len(fact.FactDigest()) != 64 || len(fact.FactRevision()) != 64 || fact.ValidUntil() <= requests[index].EvaluationTime {
			t.Fatalf("Resolve() fact[%d] = %+v", index, fact)
		}
	}
}

func TestStaticScheduleProviderSeparatesUnknownFromRetryableFailure(t *testing.T) {
	plan := validPlan()
	plan.StrategyIR.Levels[0].TriggerPlan.Config = triggerConfigWithUptime("BUSINESS_LOCAL", map[string]any{
		"time_ranges": []any{map[string]any{"start": "09:00", "end": "17:00"}},
	})
	requirement := mustCompilePlan(t, newTestCompiler(t), plan).Levels()[0].EffectiveTimeRequirement()
	request := []EffectiveTimeRequest{{
		TenantID: "default", BusinessID: "2", EvaluationTime: 1725000000, Requirement: requirement,
	}}
	unknown := NewStaticScheduleProvider(TimezoneResolverFunc(func(context.Context, string, string, string) (*time.Location, error) {
		return nil, ErrEffectiveTimeUnknown
	}))
	facts, err := unknown.Resolve(context.Background(), request)
	if err != nil || len(facts) != 1 || facts[0].Status() != EffectiveTimeUnknown {
		t.Fatalf("unknown Resolve() = %+v, %v", facts, err)
	}

	retryable := errors.New("redis unavailable")
	failing := NewStaticScheduleProvider(TimezoneResolverFunc(func(context.Context, string, string, string) (*time.Location, error) {
		return nil, retryable
	}))
	if _, err := failing.Resolve(context.Background(), request); !errors.Is(err, retryable) {
		t.Fatalf("retryable Resolve() error = %v", err)
	}
}

func triggerConfigWithUptime(timezoneRef string, uptime map[string]any) json.RawMessage {
	return mustJSON(map[string]any{
		"window_size": 1, "required_anomalies": 1, "step_seconds": 60,
		"timezone_ref": timezoneRef, "uptime": uptime,
	})
}

func equalInt64s(left, right []int64) bool {
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
