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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

const (
	EffectiveTimeAlways         = "ALWAYS"
	EffectiveTimeStaticSchedule = "STATIC_SCHEDULE"
	EffectiveTimeCalendar       = "CALENDAR"

	EffectiveTimeActive   = "ACTIVE"
	EffectiveTimeInactive = "INACTIVE"
	EffectiveTimeUnknown  = "UNKNOWN"
)

var ErrEffectiveTimeUnknown = errors.New("effective time fact unknown")

type TimeRange struct {
	startMinute uint16
	endMinute   uint16
}

func (r TimeRange) StartMinute() uint16 { return r.startMinute }
func (r TimeRange) EndMinute() uint16   { return r.endMinute }

type EffectiveTimeRequirement struct {
	kind                string
	version             uint32
	digest              string
	timeRanges          []TimeRange
	timezoneRef         string
	activeCalendarIDs   []int64
	inactiveCalendarIDs []int64
}

type effectiveTimeRequirementWire struct {
	Kind                string          `json:"kind"`
	Version             uint32          `json:"version"`
	TimeRanges          []timeRangeWire `json:"time_ranges"`
	TimezoneRef         string          `json:"timezone_ref"`
	ActiveCalendarIDs   []int64         `json:"active_calendar_ids"`
	InactiveCalendarIDs []int64         `json:"inactive_calendar_ids"`
}

type timeRangeWire struct {
	StartMinute uint16 `json:"start_minute"`
	EndMinute   uint16 `json:"end_minute"`
}

func (r EffectiveTimeRequirement) Kind() string        { return r.kind }
func (r EffectiveTimeRequirement) Version() uint32     { return r.version }
func (r EffectiveTimeRequirement) Digest() string      { return r.digest }
func (r EffectiveTimeRequirement) TimezoneRef() string { return r.timezoneRef }
func (r EffectiveTimeRequirement) TimeRanges() []TimeRange {
	return append([]TimeRange(nil), r.timeRanges...)
}
func (r EffectiveTimeRequirement) ActiveCalendarIDs() []int64 {
	return append([]int64(nil), r.activeCalendarIDs...)
}
func (r EffectiveTimeRequirement) InactiveCalendarIDs() []int64 {
	return append([]int64(nil), r.inactiveCalendarIDs...)
}
func (r EffectiveTimeRequirement) clone() EffectiveTimeRequirement {
	r.timeRanges = r.TimeRanges()
	r.activeCalendarIDs = r.ActiveCalendarIDs()
	r.inactiveCalendarIDs = r.InactiveCalendarIDs()
	return r
}

func (r EffectiveTimeRequirement) wire() effectiveTimeRequirementWire {
	timeRanges := make([]timeRangeWire, len(r.timeRanges))
	for index, timeRange := range r.timeRanges {
		timeRanges[index] = timeRangeWire{StartMinute: timeRange.startMinute, EndMinute: timeRange.endMinute}
	}
	return effectiveTimeRequirementWire{
		Kind: r.kind, Version: r.version, TimeRanges: timeRanges, TimezoneRef: r.timezoneRef,
		ActiveCalendarIDs: r.ActiveCalendarIDs(), InactiveCalendarIDs: r.InactiveCalendarIDs(),
	}
}

type EffectiveTimeRequest struct {
	TenantID       string
	BusinessID     string
	EvaluationTime int64
	Requirement    EffectiveTimeRequirement
}

type EffectiveTimeFact struct {
	status            string
	requirementDigest string
	factRevision      string
	factDigest        string
	validFrom         int64
	validUntil        int64
}

func (f EffectiveTimeFact) Status() string            { return f.status }
func (f EffectiveTimeFact) RequirementDigest() string { return f.requirementDigest }
func (f EffectiveTimeFact) FactRevision() string      { return f.factRevision }
func (f EffectiveTimeFact) FactDigest() string        { return f.factDigest }
func (f EffectiveTimeFact) ValidFrom() int64          { return f.validFrom }
func (f EffectiveTimeFact) ValidUntil() int64         { return f.validUntil }

type EffectiveTimeProvider interface {
	Resolve(context.Context, []EffectiveTimeRequest) ([]EffectiveTimeFact, error)
}

type TimezoneResolver interface {
	ResolveTimezone(context.Context, string, string, string) (*time.Location, error)
}

type TimezoneResolverFunc func(context.Context, string, string, string) (*time.Location, error)

func (f TimezoneResolverFunc) ResolveTimezone(ctx context.Context, ref, tenantID, businessID string) (*time.Location, error) {
	return f(ctx, ref, tenantID, businessID)
}

type StaticScheduleProvider struct {
	timezones TimezoneResolver
}

func NewStaticScheduleProvider(timezones TimezoneResolver) *StaticScheduleProvider {
	return &StaticScheduleProvider{timezones: timezones}
}

func (p *StaticScheduleProvider) Resolve(ctx context.Context, requests []EffectiveTimeRequest) ([]EffectiveTimeFact, error) {
	facts := make([]EffectiveTimeFact, len(requests))
	for index, request := range requests {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		status := EffectiveTimeActive
		validUntil := request.EvaluationTime + 24*60*60
		switch request.Requirement.kind {
		case EffectiveTimeAlways:
		case EffectiveTimeStaticSchedule:
			var unknown bool
			var err error
			var timezones TimezoneResolver
			if p != nil {
				timezones = p.timezones
			}
			status, validUntil, unknown, err = resolveStaticSchedule(ctx, timezones, request)
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
		case EffectiveTimeCalendar:
			facts[index], _ = unknownEffectiveTimeFact(request)
			continue
		default:
			return nil, errors.New("effective time: invalid requirement kind")
		}
		fact, err := newEffectiveTimeFact(
			status, request.Requirement.digest, request.Requirement.digest, request.EvaluationTime, validUntil,
		)
		if err != nil {
			return nil, err
		}
		facts[index] = fact
	}
	return facts, nil
}

func newEffectiveTimeFact(status, requirementDigest, factRevision string, validFrom, validUntil int64) (EffectiveTimeFact, error) {
	digest, err := contract.DeriveCanonicalDigestV2("effective-time-fact-v1", struct {
		Status            string `json:"status"`
		RequirementDigest string `json:"requirement_digest"`
		FactRevision      string `json:"fact_revision"`
		ValidFrom         int64  `json:"valid_from"`
		ValidUntil        int64  `json:"valid_until"`
	}{status, requirementDigest, factRevision, validFrom, validUntil})
	if err != nil {
		return EffectiveTimeFact{}, err
	}
	return EffectiveTimeFact{
		status: status, requirementDigest: requirementDigest, factRevision: factRevision,
		factDigest: digest, validFrom: validFrom, validUntil: validUntil,
	}, nil
}

func compileEffectiveTimeRequirement(uptime *uptimeConfigV1, timezoneRef string) (EffectiveTimeRequirement, error) {
	// Python short-circuits before looking at calendar IDs for an empty range.
	if uptime != nil && uptime.TimeRanges != nil && len(*uptime.TimeRanges) == 0 {
		uptime, timezoneRef = nil, ""
	}
	// An uptime with nothing in it is no uptime: Python's in_alarm_time
	// returns "always in effect" on `not uptime` before it reads a field
	// (alarm_backends/core/control/strategy.py), and so it does for an
	// explicit null time_ranges. A writer that defaults every detect's uptime
	// to {} states no schedule; refusing it withheld every such strategy.
	// An uptime that names calendars but no time_ranges is still refused:
	// Python fails on it (KeyError on time_ranges), so there is no semantics
	// to follow.
	if uptime != nil && uptime.TimeRanges == nil && uptime.ActiveCalendars == nil && uptime.Calendars == nil {
		uptime, timezoneRef = nil, ""
	}
	requirement := EffectiveTimeRequirement{kind: EffectiveTimeAlways, version: 1}
	if uptime == nil && timezoneRef != "" {
		return EffectiveTimeRequirement{}, errors.New("effective time: timezone_ref requires uptime")
	}
	if uptime != nil {
		if uptime.TimeRanges == nil {
			return EffectiveTimeRequirement{}, errors.New("effective time: time_ranges is required")
		}
		ranges := make([]TimeRange, 0, len(*uptime.TimeRanges))
		seenRanges := make(map[TimeRange]struct{}, len(*uptime.TimeRanges))
		for _, raw := range *uptime.TimeRanges {
			timeRange := timeRangeOf(raw)
			if _, duplicate := seenRanges[timeRange]; duplicate {
				continue
			}
			seenRanges[timeRange] = struct{}{}
			ranges = append(ranges, timeRange)
		}
		sort.Slice(ranges, func(left, right int) bool {
			if ranges[left].startMinute == ranges[right].startMinute {
				return ranges[left].endMinute < ranges[right].endMinute
			}
			return ranges[left].startMinute < ranges[right].startMinute
		})
		active, err := normalizeCalendarIDs(uptime.ActiveCalendars)
		if err != nil {
			return EffectiveTimeRequirement{}, err
		}
		inactive, err := normalizeCalendarIDs(uptime.Calendars)
		if err != nil {
			return EffectiveTimeRequirement{}, err
		}
		if len(active) == 0 && len(inactive) == 0 && len(ranges) == 1 &&
			ranges[0].startMinute == 0 && ranges[0].endMinute == 24*60-1 {
			ranges = nil
		}
		if len(ranges) > 0 || len(active) > 0 || len(inactive) > 0 {
			if timezoneRef != "BUSINESS_LOCAL" {
				return EffectiveTimeRequirement{}, errors.New("effective time: timezone_ref must be BUSINESS_LOCAL")
			}
			requirement.kind = EffectiveTimeStaticSchedule
			requirement.timezoneRef = timezoneRef
			requirement.timeRanges = ranges
			requirement.activeCalendarIDs = active
			requirement.inactiveCalendarIDs = inactive
			if len(active) > 0 || len(inactive) > 0 {
				requirement.kind = EffectiveTimeCalendar
			}
		}
	}
	digest, err := contract.DeriveCanonicalDigestV2("effective-time-requirement-v1", requirement.wire())
	if err != nil {
		return EffectiveTimeRequirement{}, err
	}
	requirement.digest = digest
	return requirement, nil
}

// timeRangeOf reads one configured range the way Python's in_alarm_time
// reads it: a start that does not parse is 00:00 and an end that does not
// parse is 23:59, each on its own, and the range is used as it comes out
// (alarm_backends/core/control/strategy.py, the two try/except around
// arrow.get). Refusing the Plan instead, as this did, turned a malformed
// range into a strategy that never detected, when Python had it detect all
// day; the Control Leader names each range it read this way
// (EFFECTIVE_TIME_RANGE_INVALID), so the widening is not silent.
func timeRangeOf(raw uptimeTimeRangeV1) TimeRange {
	start, ok := parseClockMinute(raw.Start)
	if !ok {
		start = 0
	}
	end, ok := parseClockMinute(raw.End)
	if !ok {
		end = 24*60 - 1
	}
	return TimeRange{startMinute: start, endMinute: end}
}

// UptimeTimeRangesNormalized reports whether any configured range of the
// uptime has a start or end that does not parse and so is read as 00:00 or
// 23:59: what timeRangeOf did to it, for the Leader to name. False for an
// absent uptime and for one every range of which parses.
func UptimeTimeRangesNormalized(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	var value uptimeConfigV1
	if json.Unmarshal(raw, &value) != nil || value.TimeRanges == nil {
		return false
	}
	for _, timeRange := range *value.TimeRanges {
		if _, ok := parseClockMinute(timeRange.Start); !ok {
			return true
		}
		if _, ok := parseClockMinute(timeRange.End); !ok {
			return true
		}
	}
	return false
}

func parseClockMinute(value string) (uint16, bool) {
	for _, character := range value {
		if character != ':' && (character < '0' || character > '9') {
			return 0, false
		}
	}
	parts := strings.Split(value, ":")
	if len(parts) == 3 {
		second, err := strconv.Atoi(parts[2])
		if len(parts[2]) != 2 || err != nil || second < 0 || second > 59 {
			return 0, false
		}
		parts = parts[:2]
	}
	if len(parts) != 2 || len(parts[0]) != 2 || len(parts[1]) != 2 {
		return 0, false
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil || hour < 0 || hour > 23 {
		return 0, false
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil || minute < 0 || minute > 59 {
		return 0, false
	}
	return uint16(hour*60 + minute), true
}

// CompileUptime normalizes the Python uptime shape without resolving dependencies.
func CompileUptime(raw json.RawMessage) (EffectiveTimeRequirement, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return compileEffectiveTimeRequirement(nil, "")
	}
	var value uptimeConfigV1
	if err := json.Unmarshal(raw, &value); err != nil {
		return EffectiveTimeRequirement{}, err
	}
	return compileEffectiveTimeRequirement(&value, businessLocalTimezoneRef)
}

func normalizeCalendarIDs(ids []int64) ([]int64, error) {
	result := append([]int64(nil), ids...)
	sort.Slice(result, func(left, right int) bool { return result[left] < result[right] })
	unique := result[:0]
	for _, id := range result {
		if id <= 0 {
			return nil, errors.New("effective time: invalid calendar ID")
		}
		if len(unique) == 0 || unique[len(unique)-1] != id {
			unique = append(unique, id)
		}
	}
	return unique, nil
}

func matchesTimeRanges(minute int, ranges []TimeRange) bool {
	if len(ranges) == 0 {
		return true
	}
	for _, timeRange := range ranges {
		start, end := int(timeRange.startMinute), int(timeRange.endMinute)
		if (start <= end && minute >= start && minute <= end) || (start > end && (minute >= start || minute <= end)) {
			return true
		}
	}
	return false
}

func nextScheduleBoundary(now time.Time, ranges []TimeRange) time.Time {
	next := now.Add(24 * time.Hour)
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	for dayOffset := 0; dayOffset <= 1; dayOffset++ {
		base := day.AddDate(0, 0, dayOffset)
		for _, timeRange := range ranges {
			boundaries := []int{int(timeRange.startMinute), (int(timeRange.endMinute) + 1) % (24 * 60)}
			for _, minute := range boundaries {
				candidate := base.Add(time.Duration(minute) * time.Minute)
				if candidate.After(now) && candidate.Before(next) {
					next = candidate
				}
			}
		}
	}
	return next
}
