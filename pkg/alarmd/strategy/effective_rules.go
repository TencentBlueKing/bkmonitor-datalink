package strategy

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// The bound is per evaluation, not per item. Old start dates cost no more than
// recent ones; only the interval that can overlap the requested second is read.
const effectiveRuleCandidateBudget = 4096

type effectiveSnapshot struct {
	SchemaVersion    int                 `json:"schema_version"`
	Status           string              `json:"status"`
	Reason           string              `json:"reason"`
	BusinessTimezone string              `json:"business_timezone"`
	Calendars        []effectiveCalendar `json:"calendars"`
}
type effectiveCalendar struct {
	ID       int64           `json:"id"`
	TenantID string          `json:"bk_tenant_id"`
	Status   string          `json:"status"`
	Items    []effectiveItem `json:"items"`
}
type effectiveItem struct {
	ID       int64           `json:"id"`
	TimeKind string          `json:"time_kind"`
	Start    *int64          `json:"start_time"`
	End      *int64          `json:"end_time"`
	Timezone string          `json:"time_zone"`
	ParentID *int64          `json:"parent_id"`
	Repeat   json.RawMessage `json:"repeat"`
}
type effectiveRepeat struct {
	Freq             string  `json:"freq"`
	Interval         int64   `json:"interval"`
	Until            *int64  `json:"until"`
	Every            []int   `json:"every"`
	Exclude          []int64 `json:"exclude_date"`
	EncodingTimezone string  `json:"exclude_date_encoding_timezone"`
}
type compiledCalendarItem struct {
	start, end                 int64
	daily                      bool
	location, encodingLocation *time.Location
	repeat                     effectiveRepeat
	excluded                   map[int64]struct{}
}
type compiledEffectiveRules struct {
	location  *time.Location
	calendars map[int64][]compiledCalendarItem
	digest    string
	bytes     int
}

// The reasons compileEffectiveRules refuses a snapshot with. The codes live in
// the contract's reason catalogue with every other code a reader can meet; a
// code that exists only here is one the catalog cannot classify and the page
// cannot name.
const (
	ReasonEffectiveTimeSnapshotInvalid       = contract.ReasonEffectiveTimeSnapshotInvalid
	ReasonEffectiveTimeSchemaUnsupported     = contract.ReasonEffectiveTimeSchemaUnsupported
	ReasonEffectiveTimeSnapshotUnavailable   = contract.ReasonEffectiveTimeSnapshotUnavailable
	ReasonEffectiveTimeSnapshotStatusInvalid = contract.ReasonEffectiveTimeSnapshotStatusInvalid
	ReasonEffectiveTimeCalendarsMissing      = contract.ReasonEffectiveTimeCalendarsMissing
	ReasonEffectiveTimeCalendarIdentity      = contract.ReasonEffectiveTimeCalendarIdentity
	ReasonEffectiveTimeCalendarDuplicate     = contract.ReasonEffectiveTimeCalendarDuplicate
	ReasonEffectiveTimeCalendarNotPresent    = contract.ReasonEffectiveTimeCalendarNotPresent
	ReasonEffectiveTimeCalendarItemsMissing  = contract.ReasonEffectiveTimeCalendarItemsMissing
	ReasonEffectiveTimeInvalid               = contract.ReasonEffectiveTimeInvalid
	ReasonEffectiveTimeCalendarMissing       = contract.ReasonEffectiveTimeCalendarMissing
)

// EffectiveTimeTerminalReasons is every reason this compiler refuses a Plan
// for over its effective time.
func EffectiveTimeTerminalReasons() []string {
	return []string{
		ReasonEffectiveTimeSnapshotInvalid, ReasonEffectiveTimeSchemaUnsupported,
		ReasonEffectiveTimeSnapshotUnavailable, ReasonEffectiveTimeSnapshotStatusInvalid,
		ReasonEffectiveTimeCalendarsMissing, ReasonEffectiveTimeCalendarIdentity,
		ReasonEffectiveTimeCalendarDuplicate, ReasonEffectiveTimeCalendarNotPresent,
		ReasonEffectiveTimeCalendarItemsMissing, ReasonEffectiveTimeInvalid,
		ReasonEffectiveTimeCalendarMissing,
	}
}

// CompilerTerminalReasons is every reason this compiler can end a compile on,
// in any scope.
//
// It exists to be walked. The table that classifies these for the catalog is
// checked by a test over the reason catalogue, and that test skips a code it
// finds unclassified - it holds the classified codes consistent with each
// other and cannot go red because one is missing, which is how a deployment
// came to meet the missing one first. A list the compiler owns, asserted to be
// classified in full, is the guard that reaches the case this one did not.
func CompilerTerminalReasons() []string {
	return append([]string{
		contract.ReasonAlgorithmUnsupported, contract.ReasonLevelBudgetExceeded,
		contract.ReasonLevelInvalid,
		contract.ReasonPlanBudgetExceeded, contract.ReasonPlanDuplicateLevelID,
		contract.ReasonPlanInvalid, contract.ReasonProjectionInvalid,
		contract.ReasonNoDataPlanUncompilable,
	}, EffectiveTimeTerminalReasons()...)
}

func compileEffectiveRules(raw json.RawMessage, tenant string) (*compiledEffectiveRules, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var snapshot effectiveSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return nil, errors.New(ReasonEffectiveTimeSnapshotInvalid)
	}
	if snapshot.SchemaVersion != 1 {
		return nil, errors.New(ReasonEffectiveTimeSchemaUnsupported)
	}
	switch snapshot.Status {
	case "READY":
	case "INVALID", "UNAVAILABLE":
		// Both of the snapshot's own failure states map to one reason: the
		// difference between a snapshot the source could not build and one it
		// built wrong is not something this Plan can act on, and a code built
		// by interpolation is a code nothing downstream can be written
		// against.
		return nil, errors.New(ReasonEffectiveTimeSnapshotUnavailable)
	default:
		return nil, errors.New(ReasonEffectiveTimeSnapshotStatusInvalid)
	}
	if snapshot.Calendars == nil {
		return nil, errors.New(ReasonEffectiveTimeCalendarsMissing)
	}
	location, err := ruleLocation(snapshot.BusinessTimezone)
	if err != nil {
		return nil, err
	}
	digest, err := contract.DeriveCanonicalDigestV2("effective-time-snapshot-v1", snapshot)
	if err != nil {
		return nil, err
	}
	rules := &compiledEffectiveRules{location: location, calendars: make(map[int64][]compiledCalendarItem), digest: digest, bytes: len(raw) * 2}
	for _, calendar := range snapshot.Calendars {
		if calendar.ID <= 0 || calendar.TenantID != tenant {
			return nil, errors.New(ReasonEffectiveTimeCalendarIdentity)
		}
		if _, exists := rules.calendars[calendar.ID]; exists {
			return nil, errors.New(ReasonEffectiveTimeCalendarDuplicate)
		}
		if calendar.Status != "PRESENT" {
			return nil, errors.New(ReasonEffectiveTimeCalendarNotPresent)
		}
		if calendar.Items == nil {
			return nil, errors.New(ReasonEffectiveTimeCalendarItemsMissing)
		}
		items := make([]compiledCalendarItem, 0, len(calendar.Items))
		ids := make(map[int64]struct{}, len(calendar.Items))
		for _, item := range calendar.Items {
			if _, duplicate := ids[item.ID]; duplicate {
				return nil, errors.New("EFFECTIVE_TIME_ITEM_DUPLICATE")
			}
			ids[item.ID] = struct{}{}
			compiled, err := compileCalendarItem(item)
			if err != nil {
				return nil, err
			}
			items = append(items, compiled)
		}
		rules.calendars[calendar.ID] = items
	}
	return rules, nil
}

func ruleLocation(name string) (*time.Location, error) {
	if name == "" || name == "Local" {
		return nil, errors.New("EFFECTIVE_TIME_TIMEZONE_INVALID")
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return nil, errors.New("EFFECTIVE_TIME_TIMEZONE_INVALID")
	}
	return location, nil
}

func compileCalendarItem(item effectiveItem) (compiledCalendarItem, error) {
	var result compiledCalendarItem
	if item.ID <= 0 || item.Start == nil || item.End == nil {
		return result, errors.New("EFFECTIVE_TIME_ITEM_INVALID")
	}
	result.start, result.end = *item.Start, *item.End
	result.daily = item.TimeKind == "DAILY_SECONDS"
	if !result.daily && item.TimeKind != "UNIX_SECONDS" {
		return result, errors.New("EFFECTIVE_TIME_TIME_KIND_INVALID")
	}
	if result.daily {
		if result.start < 0 || result.start >= 86400 || result.end < 0 || result.end >= 86400 {
			return result, errors.New("EFFECTIVE_TIME_ITEM_TIME_INVALID")
		}
	} else if result.end < result.start || result.start < -62135596800 || result.end > 253402300799 {
		return result, errors.New("EFFECTIVE_TIME_ITEM_TIME_INVALID")
	}
	var err error
	result.location, err = ruleLocation(item.Timezone)
	if err != nil {
		return result, err
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(item.Repeat, &fields) != nil || fields == nil {
		return result, errors.New("EFFECTIVE_TIME_REPEAT_INVALID")
	}
	if len(fields) == 0 {
		return result, nil
	}
	if json.Unmarshal(item.Repeat, &result.repeat) != nil || result.repeat.Interval <= 0 {
		return result, errors.New("EFFECTIVE_TIME_REPEAT_INVALID")
	}
	for _, key := range []string{"every", "exclude_date"} {
		if raw, exists := fields[key]; exists {
			var numbers []json.RawMessage
			if json.Unmarshal(raw, &numbers) != nil || string(raw) == "null" {
				return result, errors.New("EFFECTIVE_TIME_REPEAT_LIST_INVALID")
			}
			for _, number := range numbers {
				var value int64
				if len(number) == 0 || string(number) == "null" || json.Unmarshal(number, &value) != nil {
					return result, errors.New("EFFECTIVE_TIME_REPEAT_LIST_INVALID")
				}
			}
		}
	}
	low, high := 0, 0
	switch result.repeat.Freq {
	case "day":
	case "week":
		high = 6
	case "month":
		low, high = 1, 31
	case "year":
		low, high = 1, 12
	default:
		return result, errors.New("EFFECTIVE_TIME_REPEAT_INVALID")
	}
	if result.repeat.Freq == "day" && len(result.repeat.Every) > 0 {
		return result, errors.New("EFFECTIVE_TIME_REPEAT_EVERY_INVALID")
	}
	for _, n := range result.repeat.Every {
		if n < low || n > high {
			return result, errors.New("EFFECTIVE_TIME_REPEAT_EVERY_INVALID")
		}
	}
	if result.repeat.Until != nil && *result.repeat.Until < 0 {
		return result, errors.New("EFFECTIVE_TIME_REPEAT_UNTIL_INVALID")
	}
	if len(result.repeat.Exclude) > 0 {
		result.encodingLocation, err = ruleLocation(result.repeat.EncodingTimezone)
		if err != nil {
			return result, err
		}
		result.excluded = make(map[int64]struct{}, len(result.repeat.Exclude))
		for _, n := range result.repeat.Exclude {
			result.excluded[n] = struct{}{}
		}
	}
	return result, nil
}

// ResolveEffectiveTime evaluates the frozen Plan at an explicit second, also
// for maintenance with no series or Access data. UNKNOWN must never close an alert.
func (p *CompiledPlan) HasEffectiveTimeSnapshot() bool { return p != nil && p.effectiveRules != nil }

func (p *CompiledPlan) ResolveEffectiveTime(ctx context.Context, evaluationTime int64) (EffectiveTimeFact, error) {
	return p.ResolveEffectiveTimeWithProvider(ctx, evaluationTime, "", nil)
}

// The legacy provider is used only when the snapshot field was entirely absent.
// It must prove coverage of evaluationTime; a current empty Python cache does not.
func (p *CompiledPlan) ResolveEffectiveTimeWithProvider(ctx context.Context, evaluationTime int64, businessID string, legacy EffectiveTimeProvider) (EffectiveTimeFact, error) {
	if p == nil {
		return EffectiveTimeFact{}, errors.New("effective time: Plan is missing")
	}
	request := EffectiveTimeRequest{TenantID: p.strategyRef.TenantID, BusinessID: businessID, EvaluationTime: evaluationTime}
	levels := append([]CompiledLevel(nil), p.levels...)
	if p.noDataLevel != nil {
		levels = append(levels, *p.noDataLevel)
	}
	if len(levels) == 0 {
		request.Requirement, _ = compileEffectiveTimeRequirement(nil, "")
		return unknownEffectiveTimeFact(request)
	}
	seen := make(map[string]bool, len(levels))
	status := EffectiveTimeInactive
	var facts []EffectiveTimeFact
	var bindings []string
	for _, level := range levels {
		request.Requirement = level.effectiveTime
		if seen[request.Requirement.digest] {
			continue
		}
		seen[request.Requirement.digest] = true
		fact, err := p.resolveEffectiveRequirement(ctx, request, legacy)
		if err != nil {
			return EffectiveTimeFact{}, err
		}
		facts = append(facts, fact)
		bindings = append(bindings, fact.FactDigest())
		if fact.Status() == EffectiveTimeActive {
			status = EffectiveTimeActive
		} else if fact.Status() == EffectiveTimeUnknown && status != EffectiveTimeActive {
			status = EffectiveTimeUnknown
		}
	}
	if len(facts) == 1 {
		return facts[0], nil
	}
	digest, err := contract.DeriveCanonicalDigestV2("effective-time-maintenance-levels-v1", bindings)
	if err != nil {
		return EffectiveTimeFact{}, err
	}
	return newEffectiveTimeFact(status, digest, digest, evaluationTime, evaluationTime+1)
}

// ResolveEffectiveTimeRequirement includes the no-data level while using the
// same immutable source and provider semantics as strategy-wide maintenance.
func (p *CompiledPlan) ResolveEffectiveTimeRequirement(ctx context.Context, request EffectiveTimeRequest, legacy EffectiveTimeProvider) (EffectiveTimeFact, error) {
	if p == nil || request.TenantID != p.strategyRef.TenantID {
		return EffectiveTimeFact{}, errors.New("effective time: Plan identity mismatch")
	}
	return p.resolveEffectiveRequirement(ctx, request, legacy)
}

func (p *CompiledPlan) resolveEffectiveRequirement(ctx context.Context, request EffectiveTimeRequest, legacy EffectiveTimeProvider) (EffectiveTimeFact, error) {
	if err := ctx.Err(); err != nil {
		return EffectiveTimeFact{}, err
	}
	if request.EvaluationTime >= math.MaxInt64-86400 {
		return EffectiveTimeFact{}, errors.New("effective time: evaluation second overflows")
	}
	if p.effectiveRules != nil {
		provider := NewCalendarScheduleProvider(TimezoneResolverFunc(func(context.Context, string, string, string) (*time.Location, error) {
			return p.effectiveRules.location, nil
		}), p.effectiveRules)
		facts, err := provider.Resolve(ctx, []EffectiveTimeRequest{request})
		if err != nil {
			return EffectiveTimeFact{}, err
		}
		// Keep the proof bound to both this rule version and this second.
		// A local clock boundary can jump or repeat at a DST transition.
		return newEffectiveTimeFact(facts[0].Status(), request.Requirement.digest, p.effectiveRules.digest, request.EvaluationTime, request.EvaluationTime+1)
	}
	if request.Requirement.kind == EffectiveTimeAlways {
		facts, err := NewStaticScheduleProvider(nil).Resolve(ctx, []EffectiveTimeRequest{request})
		if err != nil {
			return EffectiveTimeFact{}, err
		}
		return facts[0], nil
	}
	if legacy == nil {
		return unknownEffectiveTimeFact(request)
	}
	facts, err := legacy.Resolve(ctx, []EffectiveTimeRequest{request})
	if err != nil {
		if ctx.Err() != nil {
			return EffectiveTimeFact{}, ctx.Err()
		}
		return unknownEffectiveTimeFact(request)
	}
	if len(facts) != 1 || facts[0].RequirementDigest() != request.Requirement.digest || facts[0].ValidFrom() > request.EvaluationTime || facts[0].ValidUntil() <= request.EvaluationTime {
		return unknownEffectiveTimeFact(request)
	}
	return facts[0], nil
}

func (r *compiledEffectiveRules) ResolveCalendarFacts(ctx context.Context, requests []CalendarFactRequest) ([]CalendarFact, error) {
	facts := make([]CalendarFact, len(requests))
	budget := effectiveRuleCandidateBudget
	for index, request := range requests {
		fact := CalendarFact{Request: request, Revision: r.digest, ValidFrom: request.EvaluationTime, ValidUntil: request.EvaluationTime + 1}
		items, exists := r.calendars[request.CalendarID]
		fact.Known = exists
		for _, item := range items {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			matched, known := item.matches(request.EvaluationTime, &budget)
			if !known {
				fact.Known = false
				break
			}
			if matched {
				fact.Matched = true
				break
			}
		}
		facts[index] = fact
	}
	return facts, nil
}

func (i compiledCalendarItem) matches(second int64, budget *int) (bool, bool) {
	if *budget <= 0 {
		return false, false
	}
	*budget--
	if i.daily {
		return i.matchesDaily(second)
	}
	if i.repeat.Freq == "" {
		return second >= i.start && second <= i.end, true
	}
	duration := i.end - i.start
	// Civil dates, rather than duration/86400 alone, cover offset transitions.
	first := time.Unix(second-duration, 0).In(i.location)
	last := time.Unix(second, 0).In(i.location)
	date := time.Date(first.Year(), first.Month(), first.Day(), 12, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
	stop := time.Date(last.Year(), last.Month(), last.Day(), 12, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
	anchor := time.Unix(i.start, 0).In(i.location)
	for !date.After(stop) {
		if *budget <= 0 {
			return false, false
		}
		*budget--
		if i.matchesDate(date, anchor) {
			clock := anchor.Hour()*3600 + anchor.Minute()*60 + anchor.Second()
			start, exists := civilSecond(date.Year(), date.Month(), date.Day(), clock, i.location)
			if exists && start >= i.start && (i.repeat.Until == nil || *i.repeat.Until == 0 || start <= *i.repeat.Until) {
				if _, excluded := i.excluded[encodedDate(date, i.encodingLocation)]; !excluded {
					if second >= start && second <= start+duration {
						return true, true
					}
				}
			}
		}
		date = date.AddDate(0, 0, 1)
	}
	return false, true
}

func (i compiledCalendarItem) matchesDaily(second int64) (bool, bool) {
	now := time.Unix(second, 0).In(i.location)
	clock := int64(now.Hour()*3600 + now.Minute()*60 + now.Second())
	if i.start <= i.end {
		if clock < i.start || clock > i.end {
			return false, true
		}
	} else if clock < i.start && clock > i.end {
		return false, true
	}
	date := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.UTC)
	if !i.matchesDate(date, now) {
		return false, true
	}
	if _, excluded := i.excluded[encodedDate(date, i.encodingLocation)]; excluded {
		return false, true
	}
	startDate := date
	if i.end < i.start && clock <= i.end {
		startDate = startDate.AddDate(0, 0, -1)
	}
	start, exists := civilSecond(startDate.Year(), startDate.Month(), startDate.Day(), int(i.start), i.location)
	if !exists || second < start {
		return false, true
	}
	// DAILY_SECONDS follows the Python local-seconds branch: until bounds
	// the evaluation second, unlike anchored UNIX instances.
	if i.repeat.Until != nil && *i.repeat.Until != 0 && second > *i.repeat.Until {
		return false, true
	}
	return true, true
}

func encodedDate(date time.Time, location *time.Location) int64 {
	if location == nil {
		return 0
	}
	return time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, location).Unix()
}

func (i compiledCalendarItem) matchesDate(date, anchor time.Time) bool {
	r := i.repeat
	if r.Freq == "" {
		return true
	}
	member := func(n, defaultValue int) bool {
		if len(r.Every) == 0 {
			return !i.daily && n == defaultValue
		}
		for _, v := range r.Every {
			if v == n {
				return true
			}
		}
		return false
	}
	interval := r.Interval
	if i.daily {
		interval = 1
	}
	anchorDate := time.Date(anchor.Year(), anchor.Month(), anchor.Day(), 12, 0, 0, 0, time.UTC)
	// Unix subtraction avoids time.Duration saturation for very old anchors.
	days := (date.Unix() - anchorDate.Unix()) / 86400
	var cycles int64
	switch r.Freq {
	case "day":
		cycles = days
	case "week":
		cycles = (days + int64(anchor.Weekday()) - int64(date.Weekday())) / 7
		if !member(int(date.Weekday()), int(anchor.Weekday())) {
			return false
		}
	case "month":
		cycles = int64(date.Year()-anchor.Year())*12 + int64(date.Month()-anchor.Month())
		if !member(date.Day(), anchor.Day()) {
			return false
		}
	case "year":
		cycles = int64(date.Year() - anchor.Year())
		if !member(int(date.Month()), int(anchor.Month())) || (!i.daily && date.Day() != anchor.Day()) {
			return false
		}
	}
	return i.daily || (cycles >= 0 && cycles%interval == 0)
}

// Resolve a local wall second. Gaps have no instance; a fold chooses the later
// instant. Candidate offsets on both sides also handle half-hour DST changes.
func civilSecond(year int, month time.Month, day, clock int, location *time.Location) (int64, bool) {
	wall := time.Date(year, month, day, clock/3600, clock/60%60, clock%60, 0, time.UTC)
	guess := time.Date(year, month, day, clock/3600, clock/60%60, clock%60, 0, location)
	best := int64(math.MinInt64)
	for _, probe := range []time.Time{guess.Add(-48 * time.Hour), guess, guess.Add(48 * time.Hour)} {
		_, offset := probe.Zone()
		candidate := wall.Unix() - int64(offset)
		local := time.Unix(candidate, 0).In(location)
		if local.Year() == year && local.Month() == month && local.Day() == day && local.Hour() == clock/3600 && local.Minute() == clock/60%60 && local.Second() == clock%60 && candidate > best {
			best = candidate
		}
	}
	return best, best != math.MinInt64
}
