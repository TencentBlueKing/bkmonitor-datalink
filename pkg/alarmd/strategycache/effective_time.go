package strategycache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
	"github.com/go-redis/redis/v8"
)

const (
	// legacyReadInterval is how often one business or calendar entry is
	// re-read. Python rewrites the calendar cache once a minute (the
	// alarm_backends.core.cache.calendar cron), so reading it more often
	// buys nothing.
	legacyReadInterval = time.Minute
	// legacyTrustWindow is how old an entry may be and still answer. Python
	// reads the cache live on every trigger batch; an entry two refresh
	// cycles old is the copy this process would have if its last read had
	// simply been slow, and older than that it is nobody's current answer.
	legacyTrustWindow = 2 * time.Minute
	// legacyIdleExpiry drops entries nobody has asked for.
	legacyIdleExpiry = 5 * time.Minute
	// legacyRefreshDeadline bounds the read a resolution performs itself
	// when the entry it needs is missing or due. It is the same bound the
	// maintenance loop gives its own refresh of one Plan.
	legacyRefreshDeadline = 250 * time.Millisecond
	// legacyCalendarEntryBytes is what one calendar entry costs in memory:
	// the entry keeps a count and a digest, not the occurrences.
	legacyCalendarEntryBytes = 160
)

// LegacyEffectiveTime answers effective-time facts from the caches the Python
// backend writes, by the rules the Python backend reads them with
// (alarm_backends.core.control.strategy.Strategy.in_alarm_time):
//
//   - a calendar counts as hit when the cache holds any occurrence of the
//     tenant, whatever that occurrence's own start and end say. Python does
//     not compare them; the writer already selected the occurrences that
//     cover the minute it wrote at (ItemDetailResource with time=now);
//   - a calendar whose key is missing, or whose list is empty, is not hit.
//     Python decodes a missing key as an empty list and an empty list hits
//     nothing. Neither is a reason to say "unknown": that answer freezes the
//     Level, which is a state Python has no equivalent of;
//   - the decision is made against the cache as it is now. Python judges
//     every point of a trigger batch at the wall clock of that batch, so a
//     Slot for an earlier second is judged by the current cache too, exactly
//     as its points would have been.
//
// The provider's memory is filled by Refresh: the maintenance loop keeps the
// entries of owned Plans warm, and a resolution that finds its entry missing
// or due for a read performs one bounded read itself, so the first Slot after
// a takeover does not freeze for want of a background tick. What no read
// could fetch within the trust window is the one case that is UNKNOWN - the
// Python analogue is a trigger batch that failed on its cache read and is
// retried.
//
// Time ranges are judged at the evaluation second in the business timezone,
// as the frozen-snapshot path judges them; Python judges them at the wall
// clock, so the two differ only at a range boundary by the data delay.
type LegacyEffectiveTime struct {
	calendar, cmdb       redis.Cmdable
	prefix               string
	now                  func() time.Time
	maxEntries, maxBytes int
	pruned               time.Time
	mu                   sync.RWMutex
	business             map[string]legacyBusiness
	calendars            map[string]legacyCalendar
}
type legacyBusiness struct {
	zone     string
	location *time.Location
	read     time.Time
}

// legacyCalendarDay is one element of the calendar cache value as Python
// writes it: the occurrences of one day, under the calendar's tenant.
// CalendarCacheManager.refresh stores ItemDetailResource's list of
// {"today": <day>, "list": [<occurrence>...]} and stamps bk_tenant_id on each
// day; CalendarCacheManager.mget filters the days by that tenant and
// in_alarm_time counts the occurrences under "list". Nothing inside an
// occurrence is read.
type legacyCalendarDay struct {
	Tenant string            `json:"bk_tenant_id"`
	List   []json.RawMessage `json:"list"`
}
type legacyCalendar struct {
	// occurrences is how many the tenant's days hold; hit is occurrences > 0.
	occurrences int
	revision    string
	read        time.Time
}

func NewLegacyEffectiveTime(calendar, cmdb redis.Cmdable, prefix string, now func() time.Time, maxEntries, maxBytes int) *LegacyEffectiveTime {
	return &LegacyEffectiveTime{calendar: calendar, cmdb: cmdb, prefix: prefix, now: now, maxEntries: maxEntries, maxBytes: maxBytes,
		business: make(map[string]legacyBusiness), calendars: make(map[string]legacyCalendar)}
}

func (c *LegacyEffectiveTime) Provider() strategy.EffectiveTimeProvider {
	return c
}

func (c *LegacyEffectiveTime) Resolve(ctx context.Context, requests []strategy.EffectiveTimeRequest) ([]strategy.EffectiveTimeFact, error) {
	for _, request := range requests {
		c.ensureFresh(ctx, request)
	}
	return strategy.NewCalendarScheduleProvider(c, c).Resolve(ctx, requests)
}

// ensureFresh reads the entries a request needs when any of them is missing
// or due for its minute read. The read is bounded and its failure is not
// returned: what the memory holds decides, and the trust window is what
// turns a read that keeps failing into UNKNOWN.
func (c *LegacyEffectiveTime) ensureFresh(ctx context.Context, request strategy.EffectiveTimeRequest) {
	if request.Requirement.Kind() == strategy.EffectiveTimeAlways {
		return
	}
	ids := append(request.Requirement.ActiveCalendarIDs(), request.Requirement.InactiveCalendarIDs()...)
	if !c.due(request.TenantID, request.BusinessID, ids) {
		return
	}
	bounded, cancel := context.WithTimeout(ctx, legacyRefreshDeadline)
	defer cancel()
	_ = c.Refresh(bounded, request.TenantID, request.BusinessID, ids)
}

func (c *LegacyEffectiveTime) due(tenant, business string, ids []int64) bool {
	now := c.now()
	c.mu.RLock()
	defer c.mu.RUnlock()
	if now.Sub(c.business[tenant+"\x00"+business].read) >= legacyReadInterval {
		return true
	}
	for _, id := range ids {
		if now.Sub(c.calendars[tenant+"\x00"+strconv.FormatInt(id, 10)].read) >= legacyReadInterval {
			return true
		}
	}
	return false
}

// Refresh reads the business timezone and the calendars named. Identical
// dependencies are read at most once per legacyReadInterval and unused
// entries expire, with a hard memory bound.
func (c *LegacyEffectiveTime) Refresh(ctx context.Context, tenant, business string, ids []int64) error {
	if c == nil || c.cmdb == nil {
		return errors.New("legacy effective time cache is unavailable")
	}
	now := c.now()
	// Nothing due and nothing to prune is answered under the read lock. The
	// maintenance loop asks once a second for every legacy Plan it holds,
	// and the resolution on the Slot path reads under the same lock; a write
	// lock taken only to find out there is nothing to do was that many
	// writers a second against the detection path.
	c.mu.RLock()
	idle := now.Sub(c.pruned) < legacyReadInterval
	c.mu.RUnlock()
	if idle && !c.due(tenant, business, ids) {
		return nil
	}
	key := tenant + "\x00" + business
	c.mu.Lock()
	if now.Sub(c.pruned) >= legacyReadInterval {
		for k, v := range c.business {
			if now.Sub(v.read) > legacyIdleExpiry {
				delete(c.business, k)
			}
		}
		for k, v := range c.calendars {
			if now.Sub(v.read) > legacyIdleExpiry {
				delete(c.calendars, k)
			}
		}
		c.pruned = now
	}
	b := c.business[key]
	c.mu.Unlock()
	if now.Sub(b.read) >= legacyReadInterval {
		raw, err := c.cmdb.HGet(ctx, c.prefix+".cache.cmdb.business", business).Bytes()
		if err != nil {
			return err
		}
		if len(raw) > c.maxBytes {
			return errors.New("legacy business cache exceeds byte budget")
		}
		var value struct {
			Tenant string `json:"bk_tenant_id"`
			Zone   string `json:"time_zone"`
			Extend struct {
				Zone string `json:"time_zone"`
			} `json:"extend"`
		}
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		if value.Tenant == "" {
			// constants/common.py DEFAULT_TENANT_ID, as the Python business
			// cache reader fills it in.
			value.Tenant = "system"
		}
		if value.Tenant != tenant {
			return errors.New("legacy business cache tenant mismatch")
		}
		if value.Extend.Zone != "" {
			value.Zone = value.Extend.Zone
		}
		location, err := time.LoadLocation(value.Zone)
		if err != nil || value.Zone == "" || value.Zone == "Local" {
			return errors.New("legacy business timezone missing or invalid")
		}
		c.mu.Lock()
		if len(c.business) < c.maxEntries || !b.read.IsZero() {
			c.business[key] = legacyBusiness{zone: value.Zone, location: location, read: now}
		}
		c.mu.Unlock()
	}
	for _, id := range ids {
		key := tenant + "\x00" + strconv.FormatInt(id, 10)
		c.mu.RLock()
		prior := c.calendars[key]
		c.mu.RUnlock()
		if now.Sub(prior.read) < legacyReadInterval {
			continue
		}
		if c.calendar == nil {
			return errors.New("legacy calendar connection is unavailable")
		}
		raw, err := c.calendar.GetRange(ctx, c.prefix+".cache.calendar."+strconv.FormatInt(id, 10), 0, int64(c.maxBytes)).Bytes()
		if err != nil && !errors.Is(err, redis.Nil) {
			return err
		}
		if len(raw) > c.maxBytes {
			return errors.New("legacy calendar exceeds byte budget")
		}
		occurrences, err := countLegacyOccurrences(raw, tenant)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(raw)
		c.mu.Lock()
		if (len(c.calendars) < c.maxEntries || !prior.read.IsZero()) && (len(c.calendars)+1)*legacyCalendarEntryBytes <= c.maxBytes {
			c.calendars[key] = legacyCalendar{occurrences: occurrences, revision: hex.EncodeToString(digest[:]), read: now}
		} else {
			c.mu.Unlock()
			return errors.New("legacy calendar memory budget exhausted")
		}
		c.mu.Unlock()
	}
	return nil
}

// countLegacyOccurrences counts what Python's in_alarm_time would count for
// the tenant: every occurrence under every day stamped with the tenant. A
// missing key and an empty list are both zero, as CalendarCacheManager.get
// decodes them; a day without a tenant stamp is the default tenant's, as
// CalendarCacheManager.mget fills it in.
func countLegacyOccurrences(raw []byte, tenant string) (int, error) {
	if len(raw) == 0 {
		return 0, nil
	}
	var days []legacyCalendarDay
	if err := json.Unmarshal(raw, &days); err != nil {
		return 0, errors.New("legacy calendar cache is not a list of days: " + err.Error())
	}
	occurrences := 0
	for _, day := range days {
		if day.Tenant == "" {
			// constants/common.py DEFAULT_TENANT_ID, the value the Python
			// reader fills in for a day without a tenant.
			day.Tenant = "system"
		}
		if day.Tenant != tenant {
			continue
		}
		occurrences += len(day.List)
	}
	return occurrences, nil
}

func (c *LegacyEffectiveTime) ResolveBusinessTimezone(_ context.Context, tenant, business string) (string, bool, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.business[tenant+"\x00"+business]
	return v.zone, ok && c.now().Sub(v.read) <= legacyTrustWindow, nil
}

func (c *LegacyEffectiveTime) ResolveTimezone(_ context.Context, ref, tenant, business string) (*time.Location, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.business[tenant+"\x00"+business]
	if ref != "BUSINESS_LOCAL" || !ok || c.now().Sub(v.read) > legacyTrustWindow {
		return nil, strategy.ErrEffectiveTimeUnknown
	}
	return v.location, nil
}

// ResolveCalendarFacts answers from memory. A calendar read within the trust
// window is known, hit when it holds any occurrence of the tenant; the fact
// is bound to the evaluation second and to the bytes that were read, which
// is what it proves: this second was judged by that copy of the cache.
func (c *LegacyEffectiveTime) ResolveCalendarFacts(_ context.Context, requests []strategy.CalendarFactRequest) ([]strategy.CalendarFact, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	now := c.now()
	facts := make([]strategy.CalendarFact, len(requests))
	for i, r := range requests {
		facts[i].Request = r
		v, ok := c.calendars[r.TenantID+"\x00"+strconv.FormatInt(r.CalendarID, 10)]
		if !ok || now.Sub(v.read) > legacyTrustWindow {
			continue
		}
		facts[i] = strategy.CalendarFact{Request: r, Known: true, Matched: v.occurrences > 0, Revision: v.revision, ValidFrom: r.EvaluationTime, ValidUntil: r.EvaluationTime + 1}
	}
	return facts, nil
}
