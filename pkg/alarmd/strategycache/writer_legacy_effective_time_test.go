package strategycache

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
	"github.com/go-redis/redis/v8"
)

// writerLegacyEffectiveTime are business records and calendar values in the
// shapes their writers produce, constructed from the writers' source - the
// CMDB cache worker's business refresh and the Python calendar cache
// refresh - rather than read from a deployment. They are rebuilt when either
// writer changes.
type writerLegacyEffectiveTime struct {
	Confidence string `json:"confidence"`
	Businesses []struct {
		Case  string `json:"case"`
		Field string `json:"field"`
		Value string `json:"value"`
	} `json:"businesses"`
	Calendars []struct {
		Case  string `json:"case"`
		ID    int64  `json:"id"`
		Value string `json:"value"`
	} `json:"calendars"`
}

// writerCacheRedis serves the two caches by field and by key.
type writerCacheRedis struct {
	redis.Cmdable
	businesses map[string]string
	calendars  map[string]string
}

func (r *writerCacheRedis) HGet(_ context.Context, key, field string) *redis.StringCmd {
	value, found := r.businesses[field]
	if key != "monitor.cache.cmdb.business" || !found {
		return redis.NewStringResult("", redis.Nil)
	}
	return redis.NewStringResult(value, nil)
}

func (r *writerCacheRedis) GetRange(_ context.Context, key string, _, _ int64) *redis.StringCmd {
	value, found := r.calendars[key]
	if !found {
		return redis.NewStringResult("", redis.Nil)
	}
	return redis.NewStringResult(value, nil)
}

// Every business record and calendar value in the writers' shapes is read:
// none is refused, each business answers in the zone its record names, and
// each calendar is hit exactly when the tenant's days list an occurrence.
// The expectations are read from the values with a generic decoder, not by
// the reader under test.
func TestEveryLegacyEffectiveTimeValueTheWritersProduceIsRead(t *testing.T) {
	payload, err := os.ReadFile("testdata/writer-legacy-effective-time.json")
	if err != nil {
		t.Fatal(err)
	}
	var samples writerLegacyEffectiveTime
	if err := json.Unmarshal(payload, &samples); err != nil {
		t.Fatal(err)
	}
	source := &writerCacheRedis{businesses: map[string]string{}, calendars: map[string]string{}}
	for _, business := range samples.Businesses {
		source.businesses[business.Field] = business.Value
	}
	for _, calendar := range samples.Calendars {
		source.calendars[fmt.Sprintf("monitor.cache.calendar.%d", calendar.ID)] = calendar.Value
	}
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	refused := 0

	for _, business := range samples.Businesses {
		var record struct {
			Tenant string `json:"bk_tenant_id"`
			Zone   string `json:"time_zone"`
		}
		if err := json.Unmarshal([]byte(business.Value), &record); err != nil {
			t.Fatalf("%s: not JSON: %v", business.Case, err)
		}
		if record.Tenant == "" {
			record.Tenant = "system"
		}
		cache := NewLegacyEffectiveTime(source, source, "monitor", func() time.Time { return now }, 64, 64<<10)
		if err := cache.Refresh(context.Background(), record.Tenant, business.Field, nil); err != nil {
			refused++
			t.Errorf("%s: the reader refuses the business record: %v", business.Case, err)
			continue
		}
		location, err := cache.ResolveTimezone(context.Background(), "BUSINESS_LOCAL", record.Tenant, business.Field)
		if err != nil || location.String() != record.Zone {
			t.Errorf("%s: zone read as (%v, %v), the record names %s", business.Case, location, err, record.Zone)
		}
	}

	for _, calendar := range samples.Calendars {
		var days []struct {
			Tenant string            `json:"bk_tenant_id"`
			List   []json.RawMessage `json:"list"`
		}
		if err := json.Unmarshal([]byte(calendar.Value), &days); err != nil {
			t.Fatalf("%s: not JSON: %v", calendar.Case, err)
		}
		occurrences := 0
		for _, day := range days {
			if day.Tenant == "" || day.Tenant == "system" {
				occurrences += len(day.List)
			}
		}
		cache := NewLegacyEffectiveTime(source, source, "monitor", func() time.Time { return now }, 64, 64<<10)
		if err := cache.Refresh(context.Background(), "system", "2", []int64{calendar.ID}); err != nil {
			refused++
			t.Errorf("%s: the reader refuses the calendar value: %v", calendar.Case, err)
			continue
		}
		facts, err := cache.ResolveCalendarFacts(context.Background(), []strategy.CalendarFactRequest{
			{TenantID: "system", CalendarID: calendar.ID, EvaluationTime: now.Unix()}})
		if err != nil || len(facts) != 1 || !facts[0].Known || facts[0].Matched != (occurrences > 0) {
			t.Errorf("%s: read as %+v (%v), the value lists %d occurrences for the tenant", calendar.Case, facts, err, occurrences)
		}
	}
	t.Logf("%s: %d business records, %d calendar values, %d refused",
		samples.Confidence, len(samples.Businesses), len(samples.Calendars), refused)
}
