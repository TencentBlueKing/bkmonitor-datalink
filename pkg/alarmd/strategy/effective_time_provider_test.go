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
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBusinessLocalTimezoneResolverUsesPluggableSource(t *testing.T) {
	t.Parallel()

	resolver := NewBusinessLocalTimezoneResolver(BusinessTimezoneSourceFunc(
		func(_ context.Context, tenantID, businessID string) (string, bool, error) {
			if tenantID != "tenant-a" || businessID != "42" {
				t.Fatalf("identity = %q/%q", tenantID, businessID)
			}
			return "Asia/Tokyo", true, nil
		},
	))
	location, err := resolver.ResolveTimezone(context.Background(), "BUSINESS_LOCAL", "tenant-a", "42")
	if err != nil || location.String() != "Asia/Tokyo" {
		t.Fatalf("ResolveTimezone() = %v, %v", location, err)
	}
}

func TestBusinessLocalTimezoneResolverSeparatesUnknownFromSourceFailure(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		source BusinessTimezoneSourceFunc
	}{
		{name: "missing", source: func(context.Context, string, string) (string, bool, error) { return "", false, nil }},
		{name: "invalid IANA", source: func(context.Context, string, string) (string, bool, error) { return "Invalid/Timezone", true, nil }},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			resolver := NewBusinessLocalTimezoneResolver(test.source)
			if _, err := resolver.ResolveTimezone(context.Background(), "BUSINESS_LOCAL", "tenant-a", "42"); !errors.Is(err, ErrEffectiveTimeUnknown) {
				t.Fatalf("ResolveTimezone() error = %v", err)
			}
		})
	}

	want := errors.New("business cache unavailable")
	resolver := NewBusinessLocalTimezoneResolver(BusinessTimezoneSourceFunc(
		func(context.Context, string, string) (string, bool, error) { return "", false, want },
	))
	if _, err := resolver.ResolveTimezone(context.Background(), "BUSINESS_LOCAL", "tenant-a", "42"); !errors.Is(err, want) {
		t.Fatalf("ResolveTimezone() error = %v", err)
	} else {
		assertRetryableEffectiveTimeDependency(t, err)
	}
}

func TestBusinessLocalTimezoneResolverDoesNotMarkLocalErrorsRetryable(t *testing.T) {
	t.Parallel()

	resolver := NewBusinessLocalTimezoneResolver(nil)
	_, err := resolver.ResolveTimezone(context.Background(), "BUSINESS_LOCAL", "tenant-a", "42")
	assertNotRetryableEffectiveTimeDependency(t, err)

	resolver = NewBusinessLocalTimezoneResolver(BusinessTimezoneSourceFunc(
		func(context.Context, string, string) (string, bool, error) { return "", false, nil },
	))
	_, err = resolver.ResolveTimezone(context.Background(), "BUSINESS_LOCAL", "tenant-a", "42")
	if !errors.Is(err, ErrEffectiveTimeUnknown) {
		t.Fatalf("ResolveTimezone() error = %v", err)
	}
	assertNotRetryableEffectiveTimeDependency(t, err)
}

func TestCalendarScheduleProviderResolvesAlwaysWithoutDependencies(t *testing.T) {
	t.Parallel()

	requirement := mustCompilePlan(t, newTestCompiler(t), validPlan()).Levels()[0].EffectiveTimeRequirement()
	evaluationTime := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC).Unix()
	provider := NewCalendarScheduleProvider(nil, nil)
	facts, err := provider.Resolve(context.Background(), []EffectiveTimeRequest{{
		TenantID: "tenant-a", BusinessID: "42", EvaluationTime: evaluationTime, Requirement: requirement,
	}})
	if err != nil || len(facts) != 1 || facts[0].Status() != EffectiveTimeActive {
		t.Fatalf("Resolve(ALWAYS) = %+v, %v", facts, err)
	}
}

func TestCalendarScheduleProviderBatchesFactsAndAppliesActivePriority(t *testing.T) {
	t.Parallel()

	requirement := calendarRequirement(t, []int64{3, 9}, []int64{8})
	evaluationTime := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC).Unix()
	var gotRequests []CalendarFactRequest
	source := CalendarFactSourceFunc(func(_ context.Context, requests []CalendarFactRequest) ([]CalendarFact, error) {
		gotRequests = append([]CalendarFactRequest(nil), requests...)
		facts := make([]CalendarFact, 0, len(requests))
		for _, request := range requests {
			facts = append(facts, CalendarFact{
				Request: request, Known: true, Matched: request.CalendarID != 3,
				Revision:  fmt.Sprintf("%064x", request.CalendarID),
				ValidFrom: evaluationTime - 60, ValidUntil: evaluationTime + 300,
			})
		}
		return facts, nil
	})
	provider := NewCalendarScheduleProvider(fixedTimezoneResolver(time.UTC), source)
	requests := []EffectiveTimeRequest{
		{TenantID: "tenant-a", BusinessID: "42", EvaluationTime: evaluationTime, Requirement: requirement},
		{TenantID: "tenant-a", BusinessID: "42", EvaluationTime: evaluationTime, Requirement: requirement},
	}
	facts, err := provider.Resolve(context.Background(), requests)
	if err != nil {
		t.Fatal(err)
	}
	wantRequests := []CalendarFactRequest{
		{TenantID: "tenant-a", CalendarID: 3, EvaluationTime: evaluationTime},
		{TenantID: "tenant-a", CalendarID: 8, EvaluationTime: evaluationTime},
		{TenantID: "tenant-a", CalendarID: 9, EvaluationTime: evaluationTime},
	}
	if !reflect.DeepEqual(gotRequests, wantRequests) {
		t.Fatalf("calendar requests = %+v, want %+v", gotRequests, wantRequests)
	}
	if len(facts) != 2 || facts[0].Status() != EffectiveTimeActive || facts[1].Status() != EffectiveTimeActive {
		t.Fatalf("Resolve() facts = %+v", facts)
	}
	for _, fact := range facts {
		if fact.ValidUntil() != evaluationTime+300 || fact.RequirementDigest() != requirement.Digest() || len(fact.FactRevision()) != 64 {
			t.Fatalf("calendar fact = %+v", fact)
		}
	}
}

func TestCalendarScheduleProviderDistinguishesKnownEmptyUnknownAndDependencyFailure(t *testing.T) {
	t.Parallel()

	requirement := calendarRequirement(t, []int64{3}, nil)
	evaluationTime := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC).Unix()
	request := []EffectiveTimeRequest{{
		TenantID: "tenant-a", BusinessID: "42", EvaluationTime: evaluationTime, Requirement: requirement,
	}}
	calendarRequest := CalendarFactRequest{TenantID: "tenant-a", CalendarID: 3, EvaluationTime: evaluationTime}

	knownEmpty := NewCalendarScheduleProvider(fixedTimezoneResolver(time.UTC), CalendarFactSourceFunc(
		func(context.Context, []CalendarFactRequest) ([]CalendarFact, error) {
			return []CalendarFact{{
				Request: calendarRequest, Known: true, Matched: false, Revision: strings.Repeat("a", 64),
				ValidFrom: evaluationTime - 1, ValidUntil: evaluationTime + 60,
			}}, nil
		},
	))
	facts, err := knownEmpty.Resolve(context.Background(), request)
	if err != nil || facts[0].Status() != EffectiveTimeInactive {
		t.Fatalf("known-empty Resolve() = %+v, %v", facts, err)
	}

	unknown := NewCalendarScheduleProvider(fixedTimezoneResolver(time.UTC), CalendarFactSourceFunc(
		func(context.Context, []CalendarFactRequest) ([]CalendarFact, error) { return nil, nil },
	))
	facts, err = unknown.Resolve(context.Background(), request)
	if err != nil || facts[0].Status() != EffectiveTimeUnknown {
		t.Fatalf("unknown Resolve() = %+v, %v", facts, err)
	}

	want := errors.New("calendar cache unavailable")
	failing := NewCalendarScheduleProvider(fixedTimezoneResolver(time.UTC), CalendarFactSourceFunc(
		func(context.Context, []CalendarFactRequest) ([]CalendarFact, error) { return nil, want },
	))
	if _, err := failing.Resolve(context.Background(), request); !errors.Is(err, want) {
		t.Fatalf("dependency Resolve() error = %v", err)
	} else {
		assertRetryableEffectiveTimeDependency(t, err)
	}
}

func TestCalendarScheduleProviderDoesNotMarkLocalRequirementErrorRetryable(t *testing.T) {
	t.Parallel()

	provider := NewCalendarScheduleProvider(nil, nil)
	_, err := provider.Resolve(context.Background(), []EffectiveTimeRequest{{
		TenantID: "tenant-a", BusinessID: "42", EvaluationTime: 1725000000,
		Requirement: EffectiveTimeRequirement{kind: "INVALID"},
	}})
	if err == nil {
		t.Fatal("Resolve() accepted an invalid local requirement")
	}
	assertNotRetryableEffectiveTimeDependency(t, err)
}

func TestCalendarScheduleProviderDoesNotUseFactsOutsideValidInterval(t *testing.T) {
	t.Parallel()

	requirement := calendarRequirement(t, nil, []int64{8})
	evaluationTime := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC).Unix()
	provider := NewCalendarScheduleProvider(fixedTimezoneResolver(time.UTC), CalendarFactSourceFunc(
		func(_ context.Context, requests []CalendarFactRequest) ([]CalendarFact, error) {
			return []CalendarFact{{
				Request: requests[0], Known: true, Matched: true, Revision: strings.Repeat("a", 64),
				ValidFrom: evaluationTime - 120, ValidUntil: evaluationTime - 60,
			}}, nil
		},
	))
	facts, err := provider.Resolve(context.Background(), []EffectiveTimeRequest{{
		TenantID: "tenant-a", BusinessID: "42", EvaluationTime: evaluationTime, Requirement: requirement,
	}})
	if err != nil || facts[0].Status() != EffectiveTimeUnknown {
		t.Fatalf("historical Resolve() = %+v, %v", facts, err)
	}
}

func calendarRequirement(t testing.TB, active, inactive []int64) EffectiveTimeRequirement {
	t.Helper()
	plan := validPlan()
	plan.StrategyIR.Levels[0].TriggerPlan.Config = triggerConfigWithUptime("BUSINESS_LOCAL", map[string]any{
		"time_ranges":      []any{map[string]any{"start": "00:00", "end": "23:59"}},
		"active_calendars": active,
		"calendars":        inactive,
	})
	return mustCompilePlan(t, newTestCompiler(t), plan).Levels()[0].EffectiveTimeRequirement()
}

func fixedTimezoneResolver(location *time.Location) TimezoneResolver {
	return TimezoneResolverFunc(func(context.Context, string, string, string) (*time.Location, error) {
		return location, nil
	})
}

func assertRetryableEffectiveTimeDependency(t testing.TB, err error) {
	t.Helper()
	var dependency *EffectiveTimeDependencyError
	if !errors.As(err, &dependency) || !dependency.RetryableEffectiveTimeDependency() {
		t.Fatalf("error is not a retryable EffectiveTime dependency: %T %v", err, err)
	}
}

func assertNotRetryableEffectiveTimeDependency(t testing.TB, err error) {
	t.Helper()
	var dependency interface{ RetryableEffectiveTimeDependency() bool }
	if errors.As(err, &dependency) && dependency.RetryableEffectiveTimeDependency() {
		t.Fatalf("error unexpectedly marked as retryable EffectiveTime dependency: %T %v", err, err)
	}
}
