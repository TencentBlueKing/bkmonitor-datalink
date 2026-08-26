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
	"sort"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

const businessLocalTimezoneRef = "BUSINESS_LOCAL"

// EffectiveTimeDependencyError marks a public EffectiveTime source failure as
// retryable while preserving the source error for errors.Is/errors.As.
type EffectiveTimeDependencyError struct {
	operation string
	cause     error
}

func (e *EffectiveTimeDependencyError) Error() string {
	return fmt.Sprintf("effective time: %s: %v", e.operation, e.cause)
}

func (e *EffectiveTimeDependencyError) Unwrap() error {
	return e.cause
}

func (e *EffectiveTimeDependencyError) RetryableEffectiveTimeDependency() bool {
	return true
}

func newEffectiveTimeDependencyError(operation string, cause error) error {
	return &EffectiveTimeDependencyError{operation: operation, cause: cause}
}

type BusinessTimezoneSource interface {
	ResolveBusinessTimezone(context.Context, string, string) (string, bool, error)
}

type BusinessTimezoneSourceFunc func(context.Context, string, string) (string, bool, error)

func (f BusinessTimezoneSourceFunc) ResolveBusinessTimezone(ctx context.Context, tenantID, businessID string) (string, bool, error) {
	return f(ctx, tenantID, businessID)
}

type BusinessLocalTimezoneResolver struct {
	source BusinessTimezoneSource
}

func NewBusinessLocalTimezoneResolver(source BusinessTimezoneSource) *BusinessLocalTimezoneResolver {
	return &BusinessLocalTimezoneResolver{source: source}
}

func (r *BusinessLocalTimezoneResolver) ResolveTimezone(
	ctx context.Context, ref, tenantID, businessID string,
) (*time.Location, error) {
	if ref != businessLocalTimezoneRef {
		return nil, fmt.Errorf("effective time: unsupported timezone ref %q", ref)
	}
	if r == nil || r.source == nil {
		return nil, errors.New("effective time: business timezone source is unavailable")
	}
	name, found, err := r.source.ResolveBusinessTimezone(ctx, tenantID, businessID)
	if err != nil {
		return nil, newEffectiveTimeDependencyError("resolve business timezone", err)
	}
	if !found || name == "" {
		return nil, fmt.Errorf("effective time: business timezone is missing: %w", ErrEffectiveTimeUnknown)
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("effective time: invalid business timezone %q: %w", name, ErrEffectiveTimeUnknown)
	}
	return location, nil
}

type CalendarFactRequest struct {
	TenantID       string
	CalendarID     int64
	EvaluationTime int64
}

// CalendarFact is the decoded, freshness-bounded result of a calendar source.
// Known=true and Matched=false represents an authoritative empty result. A
// source adapter must leave Known=false when it cannot prove that the fact
// covers EvaluationTime.
type CalendarFact struct {
	Request    CalendarFactRequest
	Known      bool
	Matched    bool
	Revision   string
	ValidFrom  int64
	ValidUntil int64
}

type CalendarFactSource interface {
	ResolveCalendarFacts(context.Context, []CalendarFactRequest) ([]CalendarFact, error)
}

type CalendarFactSourceFunc func(context.Context, []CalendarFactRequest) ([]CalendarFact, error)

func (f CalendarFactSourceFunc) ResolveCalendarFacts(ctx context.Context, requests []CalendarFactRequest) ([]CalendarFact, error) {
	return f(ctx, requests)
}

// CalendarScheduleProvider resolves every EffectiveTime requirement kind. It
// owns only the pure schedule/calendar composition; Redis keys, payloads and
// freshness are responsibilities of CalendarFactSource implementations.
type CalendarScheduleProvider struct {
	timezones TimezoneResolver
	calendars CalendarFactSource
}

func NewCalendarScheduleProvider(timezones TimezoneResolver, calendars CalendarFactSource) *CalendarScheduleProvider {
	return &CalendarScheduleProvider{timezones: timezones, calendars: calendars}
}

func (p *CalendarScheduleProvider) Resolve(ctx context.Context, requests []EffectiveTimeRequest) ([]EffectiveTimeFact, error) {
	facts := make([]EffectiveTimeFact, len(requests))
	calendarRequests := make(map[CalendarFactRequest]struct{})
	scheduleStatus := make([]string, len(requests))
	scheduleValidUntil := make([]int64, len(requests))
	var timezones TimezoneResolver
	if p != nil {
		timezones = p.timezones
	}

	for index, request := range requests {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch request.Requirement.kind {
		case EffectiveTimeAlways:
			fact, err := newEffectiveTimeFact(
				EffectiveTimeActive, request.Requirement.digest, request.Requirement.digest,
				request.EvaluationTime, request.EvaluationTime+24*60*60,
			)
			if err != nil {
				return nil, err
			}
			facts[index] = fact
		case EffectiveTimeStaticSchedule, EffectiveTimeCalendar:
			status, validUntil, unknown, err := resolveStaticSchedule(ctx, timezones, request)
			if err != nil {
				return nil, err
			}
			if unknown {
				facts[index], err = unknownEffectiveTimeFact(request)
				if err != nil {
					return nil, err
				}
				continue
			}
			if request.Requirement.kind == EffectiveTimeStaticSchedule || status == EffectiveTimeInactive {
				fact, err := newEffectiveTimeFact(
					status, request.Requirement.digest, request.Requirement.digest,
					request.EvaluationTime, validUntil,
				)
				if err != nil {
					return nil, err
				}
				facts[index] = fact
				continue
			}
			scheduleStatus[index], scheduleValidUntil[index] = status, validUntil
			for _, calendarID := range request.Requirement.activeCalendarIDs {
				calendarRequests[CalendarFactRequest{request.TenantID, calendarID, request.EvaluationTime}] = struct{}{}
			}
			for _, calendarID := range request.Requirement.inactiveCalendarIDs {
				calendarRequests[CalendarFactRequest{request.TenantID, calendarID, request.EvaluationTime}] = struct{}{}
			}
		default:
			return nil, errors.New("effective time: invalid requirement kind")
		}
	}

	if len(calendarRequests) == 0 {
		return facts, nil
	}
	if p == nil || p.calendars == nil {
		return nil, errors.New("effective time: calendar fact source is unavailable")
	}
	batch := make([]CalendarFactRequest, 0, len(calendarRequests))
	for request := range calendarRequests {
		batch = append(batch, request)
	}
	sort.Slice(batch, func(left, right int) bool {
		if batch[left].TenantID != batch[right].TenantID {
			return batch[left].TenantID < batch[right].TenantID
		}
		if batch[left].CalendarID != batch[right].CalendarID {
			return batch[left].CalendarID < batch[right].CalendarID
		}
		return batch[left].EvaluationTime < batch[right].EvaluationTime
	})
	resolved, err := p.calendars.ResolveCalendarFacts(ctx, append([]CalendarFactRequest(nil), batch...))
	if err != nil {
		return nil, newEffectiveTimeDependencyError("resolve calendar facts", err)
	}
	byRequest := indexCalendarFacts(resolved)
	for index, request := range requests {
		if request.Requirement.kind != EffectiveTimeCalendar || facts[index].status != "" {
			continue
		}
		fact, err := resolveCalendarRequirement(request, scheduleStatus[index], scheduleValidUntil[index], byRequest)
		if err != nil {
			return nil, err
		}
		facts[index] = fact
	}
	return facts, nil
}

func resolveStaticSchedule(
	ctx context.Context, timezones TimezoneResolver, request EffectiveTimeRequest,
) (string, int64, bool, error) {
	if timezones == nil {
		return "", 0, false, errors.New("effective time: timezone resolver is unavailable")
	}
	location, err := timezones.ResolveTimezone(
		ctx, request.Requirement.timezoneRef, request.TenantID, request.BusinessID,
	)
	if errors.Is(err, ErrEffectiveTimeUnknown) || (err == nil && location == nil) {
		return "", 0, true, nil
	}
	if err != nil {
		return "", 0, false, err
	}
	now := time.Unix(request.EvaluationTime, 0).In(location)
	status := EffectiveTimeActive
	if !matchesTimeRanges(now.Hour()*60+now.Minute(), request.Requirement.timeRanges) {
		status = EffectiveTimeInactive
	}
	return status, nextScheduleBoundary(now, request.Requirement.timeRanges).Unix(), false, nil
}

func indexCalendarFacts(facts []CalendarFact) map[CalendarFactRequest]CalendarFact {
	indexed := make(map[CalendarFactRequest]CalendarFact, len(facts))
	duplicates := make(map[CalendarFactRequest]struct{})
	for _, fact := range facts {
		if _, duplicate := indexed[fact.Request]; duplicate {
			duplicates[fact.Request] = struct{}{}
			continue
		}
		indexed[fact.Request] = fact
	}
	for request := range duplicates {
		delete(indexed, request)
	}
	return indexed
}

func resolveCalendarRequirement(
	request EffectiveTimeRequest, scheduleStatus string, scheduleValidUntil int64,
	facts map[CalendarFactRequest]CalendarFact,
) (EffectiveTimeFact, error) {
	used := make([]calendarFactWire, 0,
		len(request.Requirement.activeCalendarIDs)+len(request.Requirement.inactiveCalendarIDs))
	validUntil := scheduleValidUntil
	activeMatched, inactiveMatched := false, false
	for _, calendarID := range append(request.Requirement.ActiveCalendarIDs(), request.Requirement.InactiveCalendarIDs()...) {
		key := CalendarFactRequest{request.TenantID, calendarID, request.EvaluationTime}
		fact, ok := facts[key]
		if !ok || !calendarFactCovers(fact, request.EvaluationTime) {
			return unknownEffectiveTimeFact(request)
		}
		used = append(used, calendarFactWire{
			TenantID: fact.Request.TenantID, CalendarID: fact.Request.CalendarID,
			EvaluationTime: fact.Request.EvaluationTime, Matched: fact.Matched,
			Revision: fact.Revision, ValidFrom: fact.ValidFrom, ValidUntil: fact.ValidUntil,
		})
		if fact.ValidUntil < validUntil {
			validUntil = fact.ValidUntil
		}
		if fact.Matched {
			if containsCalendarID(request.Requirement.activeCalendarIDs, calendarID) {
				activeMatched = true
			} else {
				inactiveMatched = true
			}
		}
	}
	status := scheduleStatus
	if activeMatched {
		status = EffectiveTimeActive
	} else if len(request.Requirement.activeCalendarIDs) > 0 || inactiveMatched {
		status = EffectiveTimeInactive
	}
	revision, err := contract.DeriveCanonicalDigestV2("effective-time-calendar-fact-revision-v1", used)
	if err != nil {
		return EffectiveTimeFact{}, err
	}
	return newEffectiveTimeFact(status, request.Requirement.digest, revision, request.EvaluationTime, validUntil)
}

type calendarFactWire struct {
	TenantID       string `json:"tenant_id"`
	CalendarID     int64  `json:"calendar_id"`
	EvaluationTime int64  `json:"evaluation_time"`
	Matched        bool   `json:"matched"`
	Revision       string `json:"revision"`
	ValidFrom      int64  `json:"valid_from"`
	ValidUntil     int64  `json:"valid_until"`
}

func calendarFactCovers(fact CalendarFact, evaluationTime int64) bool {
	return fact.Known && isCanonicalDigest(fact.Revision) &&
		fact.ValidFrom <= evaluationTime && evaluationTime < fact.ValidUntil
}

func isCanonicalDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}

func containsCalendarID(ids []int64, target int64) bool {
	index := sort.Search(len(ids), func(index int) bool { return ids[index] >= target })
	return index < len(ids) && ids[index] == target
}

func unknownEffectiveTimeFact(request EffectiveTimeRequest) (EffectiveTimeFact, error) {
	return newEffectiveTimeFact(
		EffectiveTimeUnknown, request.Requirement.digest, request.Requirement.digest,
		request.EvaluationTime, request.EvaluationTime+60,
	)
}
