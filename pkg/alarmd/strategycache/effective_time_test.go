package strategycache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
	"github.com/go-redis/redis/v8"
)

// The two Python caches, served by key. A calendar with no entry answers as
// Redis answers a missing key.
type effectiveCacheRedis struct {
	redis.Cmdable
	business  string
	calendars map[int64]string
	err       error
	calls     int
}

func (r *effectiveCacheRedis) HGet(_ context.Context, key, field string) *redis.StringCmd {
	r.calls++
	if key != "monitor.cache.cmdb.business" || field != "2" {
		return redis.NewStringResult("", fmt.Errorf("unexpected business key %s %s", key, field))
	}
	return redis.NewStringResult(r.business, r.err)
}
func (r *effectiveCacheRedis) GetRange(_ context.Context, key string, start, end int64) *redis.StringCmd {
	r.calls++
	if start != 0 || end <= 0 {
		return redis.NewStringResult("", fmt.Errorf("unexpected calendar range %s %d %d", key, start, end))
	}
	if r.err != nil {
		return redis.NewStringResult("", r.err)
	}
	for id, payload := range r.calendars {
		if key == fmt.Sprintf("monitor.cache.calendar.%d", id) {
			return redis.NewStringResult(payload, nil)
		}
	}
	return redis.NewStringResult("", redis.Nil)
}

// pythonCalendar is the calendar cache value as CalendarCacheManager.refresh
// writes it, copied from alarm_backends' own in_alarm_time test: one day
// group, stamped with the tenant, holding one occurrence. The occurrence's
// times are April 2022; the tests run in September 2026, and Python still
// counts it as a hit, because in_alarm_time never reads them.
func pythonCalendar(tenant string) string {
	return `[{"bk_tenant_id":"` + tenant + `","today":1649833300,"list":[{"id":2,"calendar_name":"test calendar","name":"holiday",` +
		`"start_time":1649833200,"end_time":1649833800,"update_user":"admin","update_time":1649832487,"create_time":1649832487,` +
		`"create_user":"admin","calendar_id":2,"color":"#540ac0","repeat":{},"parent_id":null,"is_first":true,"status":false}]}]`
}

func effectiveCacheFixture(t *testing.T) (*LegacyEffectiveTime, *effectiveCacheRedis, *time.Time) {
	t.Helper()
	now := time.Date(2026, 9, 22, 12, 0, 30, 0, time.UTC)
	source := &effectiveCacheRedis{business: `{"bk_tenant_id":"tenant-a","time_zone":"UTC"}`,
		calendars: map[int64]string{7: pythonCalendar("tenant-a")}}
	cache := NewLegacyEffectiveTime(source, source, "monitor", func() time.Time { return now }, 64, 64<<10)
	return cache, source, &now
}

func effectiveRequestWith(t *testing.T, tenant string, at int64, uptime string) strategy.EffectiveTimeRequest {
	t.Helper()
	requirement, err := strategy.CompileUptime(json.RawMessage(uptime))
	if err != nil {
		t.Fatal(err)
	}
	return strategy.EffectiveTimeRequest{TenantID: tenant, BusinessID: "2", EvaluationTime: at, Requirement: requirement}
}

func effectiveRequest(t *testing.T, tenant string, at int64, calendar bool) strategy.EffectiveTimeRequest {
	t.Helper()
	raw := `{"time_ranges":[{"start":"11:00","end":"13:00"}]}`
	if calendar {
		raw = `{"time_ranges":[{"start":"11:00","end":"13:00"}],"active_calendars":[7]}`
	}
	return effectiveRequestWith(t, tenant, at, raw)
}

func requireEffectiveStatus(t *testing.T, cache *LegacyEffectiveTime, request strategy.EffectiveTimeRequest, want string) {
	t.Helper()
	facts, err := cache.Provider().Resolve(context.Background(), []strategy.EffectiveTimeRequest{request})
	if err != nil {
		t.Fatalf("provider returned dependency error instead of %s: %v", want, err)
	}
	if len(facts) != 1 || facts[0].Status() != want {
		t.Fatalf("facts=%+v want status=%s", facts, want)
	}
}

// The calendar cache is a list of days, each holding the occurrences of that
// day under the calendar's tenant. The flat list the first reader assumed
// was never what Python writes: read that way, a real cache value was
// refused as an occurrence with invalid bounds and every Level on the
// calendar froze.
func TestTheCalendarCacheIsReadInTheShapePythonWrites(t *testing.T) {
	cache, _, now := effectiveCacheFixture(t)
	if err := cache.Refresh(context.Background(), "tenant-a", "2", []int64{7}); err != nil {
		t.Fatalf("a calendar value in Python's shape was refused: %v", err)
	}
	requireEffectiveStatus(t, cache, effectiveRequest(t, "tenant-a", now.Unix(), true), strategy.EffectiveTimeActive)
	// Occurrences are counted across the tenant's days and none of the
	// others; a day without a list counts nothing, as Python's
	// item.get("list", []) counts nothing.
	for name, tt := range map[string]struct {
		payload string
		want    int
	}{
		"two days":             {`[{"bk_tenant_id":"tenant-a","today":1,"list":[{},{}]},{"bk_tenant_id":"tenant-a","today":2,"list":[{}]}]`, 3},
		"another tenant's day": {`[{"bk_tenant_id":"tenant-a","today":1,"list":[{}]},{"bk_tenant_id":"tenant-b","today":1,"list":[{},{}]}]`, 1},
		"day without a list":   {`[{"bk_tenant_id":"tenant-a","today":1}]`, 0},
		"unstamped is system":  {`[{"today":1,"list":[{}]}]`, 0},
	} {
		occurrences, err := countLegacyOccurrences([]byte(tt.payload), "tenant-a")
		if err != nil || occurrences != tt.want {
			t.Fatalf("%s: counted %d (%v), want %d", name, occurrences, err, tt.want)
		}
	}
	if occurrences, err := countLegacyOccurrences([]byte(`[{"today":1,"list":[{}]}]`), "system"); err != nil || occurrences != 1 {
		t.Fatalf("an unstamped day is the default tenant's: %d %v", occurrences, err)
	}
	if _, err := countLegacyOccurrences([]byte(`{"today":1}`), "tenant-a"); err == nil {
		t.Fatal("a value that is not a list of days was counted")
	}
}

// An occurrence in the cache is a hit whatever its own start and end say.
// Python's in_alarm_time counts the occurrences it finds and reads nothing
// inside them; the writer chose them for the minute it wrote at. Comparing
// their times against the evaluation second is a rule Python does not have,
// and it turned every occurrence that did not straddle the second into
// "unknown".
func TestAnOccurrenceHitsWhateverItsOwnTimesSay(t *testing.T) {
	cache, _, now := effectiveCacheFixture(t)
	if err := cache.Refresh(context.Background(), "tenant-a", "2", []int64{7}); err != nil {
		t.Fatal(err)
	}
	facts, err := cache.ResolveCalendarFacts(context.Background(), []strategy.CalendarFactRequest{{TenantID: "tenant-a", CalendarID: 7, EvaluationTime: now.Unix()}})
	if err != nil {
		t.Fatal(err)
	}
	if !facts[0].Known || !facts[0].Matched {
		t.Fatalf("an occurrence from 2022 in a cache written now is not a hit: %+v", facts[0])
	}
	if facts[0].ValidFrom != now.Unix() || facts[0].ValidUntil != now.Unix()+1 {
		t.Fatalf("the fact is bound to %d..%d, want the evaluation second", facts[0].ValidFrom, facts[0].ValidUntil)
	}
}

// A calendar with no key, an empty value, an empty list, a day with no
// occurrences, or only another tenant's occurrences is not hit. Python
// decodes a missing key as [] and hits nothing on it; none of these is
// "unknown", the answer that freezes the Level and that Python has no
// equivalent of. Whether "not hit" makes the Level active or inactive is
// Python's composition: an alerting calendar that is not hit makes it
// inactive, a rest calendar that is not hit leaves it active.
func TestAMissingOrEmptyCalendarIsNotHitRatherThanUnknown(t *testing.T) {
	for name, payload := range map[string]*string{
		"missing key":         nil,
		"empty value":         ptr(""),
		"empty list":          ptr(`[]`),
		"day without items":   ptr(`[{"bk_tenant_id":"tenant-a","today":1758542400,"list":[]}]`),
		"other tenant's only": ptr(pythonCalendar("tenant-b")),
	} {
		t.Run(name, func(t *testing.T) {
			cache, source, now := effectiveCacheFixture(t)
			delete(source.calendars, 7)
			if payload != nil {
				source.calendars[7] = *payload
			}
			if err := cache.Refresh(context.Background(), "tenant-a", "2", []int64{7}); err != nil {
				t.Fatalf("refused as unknown: %v", err)
			}
			facts, err := cache.ResolveCalendarFacts(context.Background(), []strategy.CalendarFactRequest{{TenantID: "tenant-a", CalendarID: 7, EvaluationTime: now.Unix()}})
			if err != nil {
				t.Fatal(err)
			}
			if !facts[0].Known || facts[0].Matched {
				t.Fatalf("want known and not hit, got %+v", facts[0])
			}
			requireEffectiveStatus(t, cache, effectiveRequestWith(t, "tenant-a", now.Unix(), `{"time_ranges":[{"start":"11:00","end":"13:00"}],"active_calendars":[7]}`), strategy.EffectiveTimeInactive)
			requireEffectiveStatus(t, cache, effectiveRequestWith(t, "tenant-a", now.Unix(), `{"time_ranges":[{"start":"11:00","end":"13:00"}],"calendars":[7]}`), strategy.EffectiveTimeActive)
		})
	}
}

func ptr(value string) *string { return &value }

// Python's composition of the two calendar kinds, through this provider:
// an alerting calendar hit wins over a rest calendar hit; a rest calendar
// hit alone makes the Level inactive; an alerting calendar configured and
// not hit makes it inactive; a rest calendar configured and not hit leaves
// it active.
func TestTheTwoCalendarKindsComposeAsPythonComposesThem(t *testing.T) {
	for name, tt := range map[string]struct {
		uptime     string
		alerting   *string
		rest       *string
		wantStatus string
	}{
		"both hit, alerting wins":          {`{"time_ranges":[{"start":"11:00","end":"13:00"}],"active_calendars":[7],"calendars":[8]}`, ptr(pythonCalendar("tenant-a")), ptr(pythonCalendar("tenant-a")), strategy.EffectiveTimeActive},
		"only the rest calendar hit":       {`{"time_ranges":[{"start":"11:00","end":"13:00"}],"active_calendars":[7],"calendars":[8]}`, ptr(`[]`), ptr(pythonCalendar("tenant-a")), strategy.EffectiveTimeInactive},
		"alerting configured, nothing hit": {`{"time_ranges":[{"start":"11:00","end":"13:00"}],"active_calendars":[7]}`, ptr(`[]`), nil, strategy.EffectiveTimeInactive},
		"rest configured, nothing hit":     {`{"time_ranges":[{"start":"11:00","end":"13:00"}],"calendars":[8]}`, nil, nil, strategy.EffectiveTimeActive},
	} {
		t.Run(name, func(t *testing.T) {
			cache, source, now := effectiveCacheFixture(t)
			delete(source.calendars, 7)
			if tt.alerting != nil {
				source.calendars[7] = *tt.alerting
			}
			if tt.rest != nil {
				source.calendars[8] = *tt.rest
			}
			requireEffectiveStatus(t, cache, effectiveRequestWith(t, "tenant-a", now.Unix(), tt.uptime), tt.wantStatus)
		})
	}
}

// A Slot for an earlier second is judged by the cache as it is now, as
// Python judges every point of a trigger batch at the wall clock of that
// batch. The previous minute, an hour ago and the next second all get the
// current answer; none of them is "unknown". The time range is judged at
// the evaluation second, so a second outside the range is inactive rather
// than unknown too.
func TestASlotForAnotherSecondIsJudgedByTheCurrentCache(t *testing.T) {
	for _, calendar := range []bool{false, true} {
		t.Run(fmt.Sprintf("calendar=%v", calendar), func(t *testing.T) {
			cache, source, now := effectiveCacheFixture(t)
			if err := cache.Refresh(context.Background(), "tenant-a", "2", []int64{7}); err != nil {
				t.Fatal(err)
			}
			reads := source.calls
			for _, tt := range []struct {
				name string
				at   int64
				want string
			}{
				{"minute-start", now.Truncate(time.Minute).Unix(), strategy.EffectiveTimeActive},
				{"current-second", now.Unix(), strategy.EffectiveTimeActive},
				{"previous-minute", now.Truncate(time.Minute).Unix() - 1, strategy.EffectiveTimeActive},
				{"an-hour-ago", now.Add(-time.Hour).Unix(), strategy.EffectiveTimeActive},
				{"next-second", now.Unix() + 1, strategy.EffectiveTimeActive},
				{"outside-the-range", now.Add(-3 * time.Hour).Unix(), strategy.EffectiveTimeInactive},
			} {
				t.Run(tt.name, func(t *testing.T) {
					requireEffectiveStatus(t, cache, effectiveRequest(t, "tenant-a", tt.at, calendar), tt.want)
				})
			}
			if source.calls != reads {
				t.Fatal("a resolution with fresh entries read Redis")
			}
		})
	}
}

// A resolution whose entries are missing reads them itself, once, and
// within the minute the next resolution reads nothing. Before this, the
// first Slot after a takeover froze every Level on a legacy schedule until
// the maintenance loop's own tick had reached the Plan.
func TestAResolutionReadsWhatItLacksAndNotAgainWithinTheMinute(t *testing.T) {
	cache, source, now := effectiveCacheFixture(t)
	requireEffectiveStatus(t, cache, effectiveRequest(t, "tenant-a", now.Unix(), true), strategy.EffectiveTimeActive)
	if source.calls != 2 {
		t.Fatalf("the first resolution read %d times, want the business and the calendar once each", source.calls)
	}
	requireEffectiveStatus(t, cache, effectiveRequest(t, "tenant-a", now.Unix(), true), strategy.EffectiveTimeActive)
	if source.calls != 2 {
		t.Fatalf("a resolution within the minute read again: %d", source.calls)
	}
	*now = now.Add(legacyReadInterval)
	requireEffectiveStatus(t, cache, effectiveRequest(t, "tenant-a", now.Unix(), true), strategy.EffectiveTimeActive)
	if source.calls != 4 {
		t.Fatalf("a resolution a minute later did not re-read: %d", source.calls)
	}
	// An ALWAYS requirement asks the cache for nothing.
	cache, source, now = effectiveCacheFixture(t)
	requireEffectiveStatus(t, cache, effectiveRequestWith(t, "tenant-a", now.Unix(), `null`), strategy.EffectiveTimeActive)
	if source.calls != 0 {
		t.Fatalf("an ALWAYS requirement read Redis %d times", source.calls)
	}
}

// A read that fails leaves the last copy in place; that copy answers until
// the trust window ends, and only then is the answer unknown. That is the
// one case Python has an analogue for: a trigger batch whose cache read
// failed is retried, not judged.
func TestAFailedReadLeavesTheLastCopyUntilTheTrustWindowEnds(t *testing.T) {
	cache, source, now := effectiveCacheFixture(t)
	if err := cache.Refresh(context.Background(), "tenant-a", "2", []int64{7}); err != nil {
		t.Fatal(err)
	}
	reads := source.calls
	if err := cache.Refresh(context.Background(), "tenant-a", "2", []int64{7}); err != nil {
		t.Fatal(err)
	}
	if reads != source.calls {
		t.Fatal("same-minute refresh reread shared dependencies")
	}
	source.err = errors.New("cache disconnected")
	*now = now.Add(90 * time.Second)
	if err := cache.Refresh(context.Background(), "tenant-a", "2", []int64{7}); err == nil {
		t.Fatal("dependency failure hidden")
	}
	requireEffectiveStatus(t, cache, effectiveRequest(t, "tenant-a", now.Unix(), true), strategy.EffectiveTimeActive)
	*now = now.Add(legacyTrustWindow - 90*time.Second + time.Second)
	requireEffectiveStatus(t, cache, effectiveRequest(t, "tenant-a", now.Unix(), true), strategy.EffectiveTimeUnknown)
	requireEffectiveStatus(t, cache, effectiveRequest(t, "tenant-a", now.Unix(), false), strategy.EffectiveTimeUnknown)
}

func TestLegacyEffectiveTimeTenantAndTimezone(t *testing.T) {
	t.Run("business-tenant-mismatch", func(t *testing.T) {
		cache, _, now := effectiveCacheFixture(t)
		if err := cache.Refresh(context.Background(), "tenant-b", "2", nil); err == nil {
			t.Fatal("foreign tenant business was loaded")
		}
		requireEffectiveStatus(t, cache, effectiveRequest(t, "tenant-b", now.Unix(), false), strategy.EffectiveTimeUnknown)
	})
	t.Run("extend-timezone-wins", func(t *testing.T) {
		cache, source, now := effectiveCacheFixture(t)
		source.business = `{"bk_tenant_id":"tenant-a","time_zone":"UTC","extend":{"time_zone":"Asia/Kolkata"}}`
		if err := cache.Refresh(context.Background(), "tenant-a", "2", nil); err != nil {
			t.Fatal(err)
		}
		requireEffectiveStatus(t, cache, effectiveRequest(t, "tenant-a", now.Unix(), false), strategy.EffectiveTimeInactive)
	})
	t.Run("missing-tenant-is-system", func(t *testing.T) {
		cache, source, now := effectiveCacheFixture(t)
		source.business = `{"time_zone":"UTC"}`
		source.calendars[7] = `[{"today":1758542400,"list":[{"id":1}]}]`
		if err := cache.Refresh(context.Background(), "system", "2", []int64{7}); err != nil {
			t.Fatal(err)
		}
		requireEffectiveStatus(t, cache, effectiveRequest(t, "system", now.Unix(), true), strategy.EffectiveTimeActive)
	})
	for _, zone := range []string{"", "Local", "invalid/zone"} {
		t.Run("invalid-zone-"+zone, func(t *testing.T) {
			cache, source, _ := effectiveCacheFixture(t)
			source.business = fmt.Sprintf(`{"bk_tenant_id":"tenant-a","time_zone":%q}`, zone)
			if err := cache.Refresh(context.Background(), "tenant-a", "2", nil); err == nil {
				t.Fatal("invalid timezone loaded")
			}
		})
	}
}

// A refresh with nothing due takes no write lock: it is asked once a second
// for every legacy Plan a replica holds, and the resolution on the Slot path
// reads under the same lock.
func TestARefreshWithNothingDueTakesNoWriteLock(t *testing.T) {
	cache, source, _ := effectiveCacheFixture(t)
	if err := cache.Refresh(context.Background(), "tenant-a", "2", []int64{7}); err != nil {
		t.Fatal(err)
	}
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	done := make(chan error, 1)
	go func() { done <- cache.Refresh(context.Background(), "tenant-a", "2", []int64{7}) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a refresh with nothing due waited for the write lock behind a reader")
	}
	if source.calls != 2 {
		t.Fatalf("a refresh with nothing due read Redis: %d calls", source.calls)
	}
}
