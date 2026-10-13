package strategycache

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// A refresh may take half of what its caller has left, at most a second and
// at least a quarter of one, and never more than the caller has left.
func TestARefreshTakesWhatItsCallerCanSpare(t *testing.T) {
	for _, c := range []struct{ remaining, want time.Duration }{
		{3 * time.Second, time.Second}, {2 * time.Second, time.Second}, {1200 * time.Millisecond, 600 * time.Millisecond},
		{400 * time.Millisecond, 250 * time.Millisecond}, {250 * time.Millisecond, 250 * time.Millisecond},
		{200 * time.Millisecond, 200 * time.Millisecond}, {0, 0}, {-time.Second, 0},
	} {
		if got := refreshBudget(c.remaining); got != c.want {
			t.Fatalf("remaining %v: budget %v, want %v", c.remaining, got, c.want)
		}
	}
	if got := RefreshDeadline(context.Background()); got != time.Second {
		t.Fatalf("no deadline: %v, want a second", got)
	}
}

// deadlineRedis records the deadline the business read was given.
type deadlineRedis struct {
	*effectiveCacheRedis
	left time.Duration
}

func (r *deadlineRedis) HGet(ctx context.Context, key, field string) *redis.StringCmd {
	if deadline, ok := ctx.Deadline(); ok {
		r.left = time.Until(deadline)
	}
	return r.effectiveCacheRedis.HGet(ctx, key, field)
}

// The read a resolution makes itself is bounded by the Slot that asked:
// with three seconds left it may use one, with 200 ms left only those.
func TestTheResolutionsOwnReadIsBoundedByItsCaller(t *testing.T) {
	for _, c := range []struct{ left, atMost, atLeast time.Duration }{
		{3 * time.Second, time.Second, 900 * time.Millisecond}, {200 * time.Millisecond, 200 * time.Millisecond, 100 * time.Millisecond},
	} {
		_, source, now := effectiveCacheFixture(t)
		recorder := &deadlineRedis{effectiveCacheRedis: source}
		cache := NewLegacyEffectiveTime(recorder, recorder, "monitor", func() time.Time { return *now }, 64, 64<<10)
		ctx, cancel := context.WithTimeout(context.Background(), c.left)
		if _, err := cache.Provider().Resolve(ctx, []strategy.EffectiveTimeRequest{effectiveRequest(t, "tenant-a", now.Unix(), false)}); err != nil {
			t.Fatal(err)
		}
		cancel()
		if recorder.left > c.atMost || recorder.left < c.atLeast {
			t.Fatalf("caller with %v: the read had %v, want within [%v, %v]", c.left, recorder.left, c.atLeast, c.atMost)
		}
	}
}

// A refresh that read anything is counted by result; one with nothing due
// is not. A failed one writes a line naming the dependency it stopped on and
// its class, at most once a minute for each, with the failures since; it is
// listed as unread only once the memory of it is past the trust window, and
// a good read of it ends that.
func TestAFailedRefreshIsCountedWrittenAndNamedOnceUntrusted(t *testing.T) {
	cache, source, now := effectiveCacheFixture(t)
	type line struct {
		dependency, class string
		failures          uint64
	}
	var lines []line
	cache.SetFailureLine(func(dependency, class string, failures uint64) {
		lines = append(lines, line{dependency, class, failures})
	})
	counts := func(ok, timeout, failed uint64) {
		t.Helper()
		if got, want := cache.RefreshCounts(), map[string]uint64{"ok": ok, "timeout": timeout, "error": failed}; !reflect.DeepEqual(got, want) {
			t.Fatalf("counts %v, want %v", got, want)
		}
	}
	if err := cache.Refresh(context.Background(), "tenant-a", "2", []int64{7}); err != nil {
		t.Fatal(err)
	}
	_ = cache.Refresh(context.Background(), "tenant-a", "2", []int64{7})
	counts(1, 0, 0)
	read := *now

	// The calendar stops decoding; the business before it still reads.
	source.calendars[7] = "not json"
	*now = now.Add(legacyReadInterval)
	if err := cache.Refresh(context.Background(), "tenant-a", "2", []int64{7}); err == nil {
		t.Fatal("a calendar that does not decode was accepted")
	}
	counts(1, 0, 1)
	if want := []line{{"calendar/tenant-a/7", "decode", 1}}; !reflect.DeepEqual(lines, want) {
		t.Fatalf("lines %+v, want %+v", lines, want)
	}
	if unread, total := cache.Unread(); total != 0 || len(unread) != 0 {
		t.Fatalf("unread %+v while the last copy is trusted", unread)
	}
	failingSince := *now

	// Within the minute the failure is counted, not written again.
	*now = now.Add(30 * time.Second)
	_ = cache.Refresh(context.Background(), "tenant-a", "2", []int64{7})
	counts(1, 0, 2)
	if len(lines) != 1 {
		t.Fatalf("lines %+v, want the second failure held back", lines)
	}
	// The read runs out of time: a timeout, and the next line carries both.
	source.err = context.DeadlineExceeded
	*now = now.Add(31 * time.Second)
	_ = cache.Refresh(context.Background(), "tenant-a", "2", []int64{7})
	counts(1, 1, 2)
	if len(lines) != 2 || lines[1] != (line{"business/tenant-a/2", "timeout", 1}) {
		t.Fatalf("lines %+v, want the business timeout written", lines)
	}
	*now = read.Add(legacyTrustWindow + time.Second)
	_ = cache.Refresh(context.Background(), "tenant-a", "2", []int64{7})
	// The business was read again when the calendar first failed, so only
	// the calendar is past the trust window.
	unread, total := cache.Unread()
	at := read
	want := []EffectiveTimeUnread{{Tenant: "tenant-a", Business: "2", Calendar: 7, Class: "decode", FailingSince: failingSince, LastRead: &at}}
	if total != 1 || !reflect.DeepEqual(unread, want) {
		t.Fatalf("unread %+v (%d), want %+v", unread, total, want)
	}
	requireEffectiveStatus(t, cache, effectiveRequest(t, "tenant-a", now.Unix(), true), strategy.EffectiveTimeUnknown)

	source.err, source.calendars[7] = nil, pythonCalendar("tenant-a")
	if err := cache.Refresh(context.Background(), "tenant-a", "2", []int64{7}); err != nil {
		t.Fatal(err)
	}
	if unread, total := cache.Unread(); total != 0 || len(unread) != 0 {
		t.Fatalf("unread %+v after a good read", unread)
	}
}

// A dependency never read is listed without a last read, and the list is
// bounded with the total kept.
func TestUnreadIsBoundedAndSaysWhenNothingWasEverRead(t *testing.T) {
	cache, source, now := effectiveCacheFixture(t)
	source.err = errors.New("connection refused")
	if err := cache.Refresh(context.Background(), "tenant-a", "2", nil); err == nil {
		t.Fatal("the failure was hidden")
	}
	unread, total := cache.Unread()
	if total != 1 || len(unread) != 1 || unread[0].LastRead != nil || unread[0].Class != "read" || unread[0].Calendar != 0 {
		t.Fatalf("unread %+v (%d), want the business never read", unread, total)
	}
	source.err = nil
	ids := make([]int64, 0, MaxEffectiveTimeUnread+3)
	for id := int64(100); id < 100+MaxEffectiveTimeUnread+3; id++ {
		ids = append(ids, id)
		source.calendars[id] = "not json"
	}
	for range ids {
		*now = now.Add(time.Second)
		_ = cache.Refresh(context.Background(), "tenant-a", "2", ids)
		// Each refresh stops on the first calendar that fails; read the
		// next one on its own.
		ids = ids[1:]
	}
	if unread, total := cache.Unread(); total != MaxEffectiveTimeUnread+3 || len(unread) != MaxEffectiveTimeUnread ||
		unread[0].Calendar != 100 || unread[0].Class != "decode" {
		t.Fatalf("unread %d of %d: first %+v", len(unread), total, unread[0])
	}
}

// Each line carries the failures since the line before it.
func TestAFailureLineCountsTheFailuresSinceTheLastLine(t *testing.T) {
	cache, source, now := effectiveCacheFixture(t)
	var failures []uint64
	cache.SetFailureLine(func(_, _ string, count uint64) { failures = append(failures, count) })
	source.calendars[7] = "not json"
	for _, step := range []time.Duration{0, 30 * time.Second, 30 * time.Second, 60 * time.Second} {
		*now = now.Add(step)
		_ = cache.Refresh(context.Background(), "tenant-a", "2", []int64{7})
	}
	if !reflect.DeepEqual(failures, []uint64{1, 2, 1}) {
		t.Fatalf("lines carried %v, want 1, then 2, then 1", failures)
	}
}

// A good read of a dependency ends its failure, also when the refresh it was
// read in failed further on: a later failure of it starts a new one.
func TestAGoodReadEndsAFailureThoughTheRefreshFailedLater(t *testing.T) {
	cache, source, now := effectiveCacheFixture(t)
	source.err = errors.New("connection refused")
	_ = cache.Refresh(context.Background(), "tenant-a", "2", []int64{7})
	source.err, source.calendars[7] = nil, "not json"
	*now = now.Add(time.Minute)
	_ = cache.Refresh(context.Background(), "tenant-a", "2", []int64{7})
	source.err = errors.New("connection refused")
	*now = now.Add(legacyTrustWindow + time.Second)
	_ = cache.Refresh(context.Background(), "tenant-a", "2", []int64{7})
	unread, _ := cache.Unread()
	for _, entry := range unread {
		if entry.Calendar == 0 && !entry.FailingSince.Equal(*now) {
			t.Fatalf("the business fails since %v, want %v: the read in between ended the first failure", entry.FailingSince, *now)
		}
	}
	if len(unread) != 2 {
		t.Fatalf("unread %+v, want the business and the calendar", unread)
	}
}
