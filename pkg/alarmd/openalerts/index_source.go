// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package openalerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-redis/redis/v8"
)

var ErrIncomplete = errors.New("alarmd openalerts: incomplete index observation")

// ErrSetTooLarge is a set larger than this process's read budget - more
// members or bytes than its memory allows one read to hold. It is an
// incomplete read to every caller that asks (errors.Is), and named apart so
// the copy counts it apart from a read that failed: a set this size is not
// an outage. A read refused this way returns a *SetTooLargeError, which says
// how large and against what budget.
var ErrSetTooLarge = fmt.Errorf("alarmd openalerts: set too large for one read: %w", ErrIncomplete)

// SetTooLargeError is a set refused for its size: its member count, the
// bytes it was estimated or found to hold (zero when refused on members),
// and the budget it exceeded.
type SetTooLargeError struct {
	Members              int64
	Bytes                int64
	MaxMembers, MaxBytes int
}

func (e *SetTooLargeError) Error() string {
	return fmt.Sprintf("%v: %d members, %d bytes against %d members and %d bytes", ErrSetTooLarge, e.Members, e.Bytes, e.MaxMembers, e.MaxBytes)
}

func (e *SetTooLargeError) Is(target error) bool {
	return target == ErrSetTooLarge || target == ErrIncomplete
}

// LargeSetError is a set that fits the budget but whose read would not end
// inside the caller's deadline: it is to be read on its own, with Budget.
// Nothing was read.
type LargeSetError struct {
	Members int64
	Budget  time.Duration
}

func (e *LargeSetError) Error() string {
	return fmt.Sprintf("alarmd openalerts: a set of %d members needs a read of its own, %s", e.Members, e.Budget)
}

type largeReadKey struct{}

// WithLargeRead marks ctx as a read of its own: ReadSet reads the set however
// many pages it takes, inside ctx's deadline.
func WithLargeRead(ctx context.Context) context.Context {
	return context.WithValue(ctx, largeReadKey{}, true)
}

var ErrCapacity = errors.New("alarmd openalerts: cache capacity reached")

// IndexSource returns only a complete bounded read. Empty is an index
// observation, never proof that the alert store has no active alerts.
type IndexSource interface {
	ReadSet(context.Context, StrategyKey) ([]string, error)
}

// Subscriber delivers the link's change notices: ready says whether the
// subscription is acknowledged, changed names a strategy whose set changed,
// and refused names a notice that was dropped and why, so a notice shape this
// build does not read is a count and not a silence.
type Subscriber interface {
	Watch(ctx context.Context, ready func(bool), changed func(StrategyKey), refused func(NoticeRefusal)) error
}

// NoticeRefusal is why a change notice was dropped. Each drop costs only
// delay: the strategy it named is read on the next periodic read. The set is
// closed: it is a metric label.
type NoticeRefusal string

const (
	// NoticeOversized: the payload is past the 64 KiB a notice may be.
	NoticeOversized NoticeRefusal = "oversized"
	// NoticeUndecodable: the payload does not decode as a notice under the
	// strict shape - a field this build does not know, a wrong type, or
	// content after the notice.
	NoticeUndecodable NoticeRefusal = "undecodable"
	// NoticeInvalidKey: the tenant or strategy it names is not a valid one.
	NoticeInvalidKey NoticeRefusal = "invalid_key"
)

// NoticeRefusals is every refusal, for the metric that pre-creates them.
var NoticeRefusals = []NoticeRefusal{NoticeOversized, NoticeUndecodable, NoticeInvalidKey}

// ReadLimits bound one set read by what this process may hold - members and
// bytes, both derived from its memory - not by a page count: a read goes on
// until the scan's cursor wraps. PageSize is the scan's COUNT hint, and
// MaxLargeRead the longest a set read on its own may take.
type ReadLimits struct {
	MaxMembers   int
	MaxBytes     int
	PageSize     int64
	MaxLargeRead time.Duration
}

// minLargeRead is the least a read of its own is given: a round's half of
// the refresh cycle, which an inline read already had.
const minLargeRead = 2500 * time.Millisecond

// defaultPageTime is a scan page's time before any has been measured.
const defaultPageTime = 20 * time.Millisecond

type SetSource struct {
	client redis.Cmdable
	prefix string
	limits ReadLimits
	// pageNanos is a moving average of a scan page's time, pages the
	// deadline cut included.
	pageNanos atomic.Int64
	// refused is each set a read found too large, by its own count then:
	// refused before reading while it holds at least that many, read again
	// once it holds fewer. A set is judged by what it was measured to weigh,
	// never by an average of the sets that passed.
	refusedMu sync.Mutex
	refused   map[string]SetTooLargeError
}

// maxRememberedRefusals bounds the sets remembered as too large; one past it
// is read again on each attempt, which the byte budget still bounds.
const maxRememberedRefusals = 4096

func validPrefix(prefix string) bool {
	return prefix != "" && len(prefix) <= 256 && strings.TrimSpace(prefix) == prefix
}

func validStrategyKey(key StrategyKey) bool {
	// A delimiter in either identity aliases another tenant/strategy pair.
	return key.TenantID != "" && len(key.TenantID) <= 256 && key.StrategyID != "" && len(key.StrategyID) <= 1024 &&
		!strings.ContainsAny(key.TenantID+key.StrategyID, ":\x00")
}

func NewSetSource(client redis.Cmdable, prefix string, limits ReadLimits) (*SetSource, error) {
	if client == nil || !validPrefix(prefix) || limits.MaxMembers <= 0 || limits.MaxBytes <= 0 || limits.PageSize <= 0 {
		return nil, errors.New("alarmd openalerts: Redis client, prefix and positive read limits are required")
	}
	return &SetSource{client: client, prefix: prefix, limits: limits}, nil
}

// ReadSet reads a strategy's set whole, or refuses it by name. A set larger
// than the budget is refused before it is read: by its member count, and,
// once a read of it was refused for its size, while it holds at least as
// many members as it did then. A set that fits but would not be read inside the caller's
// deadline is handed back as a *LargeSetError, to be read on its own under
// WithLargeRead. Members are counted once each: a scan may return one twice
// across a rehash.
func (source *SetSource) ReadSet(ctx context.Context, key StrategyKey) ([]string, error) {
	if !validStrategyKey(key) {
		return nil, errors.New("alarmd openalerts: invalid strategy identity")
	}
	name := source.prefix + ":" + key.TenantID + ":" + key.StrategyID
	before, err := source.client.SCard(ctx, name).Result()
	if err != nil {
		return nil, err
	}
	tooLarge := func(bytes int64) error {
		return &SetTooLargeError{Members: before, Bytes: bytes, MaxMembers: source.limits.MaxMembers, MaxBytes: source.limits.MaxBytes}
	}
	if before > int64(source.limits.MaxMembers) {
		return nil, tooLarge(0)
	}
	source.refusedMu.Lock()
	prior, remembered := source.refused[name]
	source.refusedMu.Unlock()
	if remembered && before >= prior.Members {
		refusal := prior
		refusal.Members = before
		return nil, &refusal
	}
	// tooLargeRead is a refusal the read measured: remembered for this set.
	tooLargeRead := func(bytes int64) error {
		err := tooLarge(bytes)
		source.refusedMu.Lock()
		if source.refused == nil {
			source.refused = make(map[string]SetTooLargeError)
		}
		if _, ok := source.refused[name]; ok || len(source.refused) < maxRememberedRefusals {
			source.refused[name] = *err.(*SetTooLargeError)
		}
		source.refusedMu.Unlock()
		return err
	}
	if pages := (before + source.limits.PageSize - 1) / source.limits.PageSize; pages > 1 && ctx.Value(largeReadKey{}) == nil {
		page := time.Duration(source.pageNanos.Load())
		if page <= 0 {
			page = defaultPageTime
		}
		need := time.Duration(pages) * page * 2
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < need {
			budget := max(minLargeRead, need)
			if source.limits.MaxLargeRead > 0 {
				budget = min(budget, source.limits.MaxLargeRead)
			}
			return nil, &LargeSetError{Members: before, Budget: budget}
		}
	}
	members := make(map[string]struct{})
	var cursor uint64
	var bytes int64
	for {
		started := time.Now()
		values, next, err := source.client.SScan(ctx, name, cursor, "", source.limits.PageSize).Result()
		if err != nil {
			// A page the deadline cut took at least this long: left out, the
			// average would learn only from the pages fast enough to finish.
			// A cancel (shutdown, takeover) says nothing about pace.
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				source.notePage(time.Since(started))
			}
			return nil, err
		}
		source.notePage(time.Since(started))
		for _, value := range values {
			// A member no alert id can be is a read refused, not a set too
			// large.
			if value == "" || len(value) > 4096 {
				return nil, ErrIncomplete
			}
			if _, seen := members[value]; seen {
				continue
			}
			bytes += int64(len(value))
			if bytes > int64(source.limits.MaxBytes) {
				return nil, tooLargeRead(bytes)
			}
			members[value] = struct{}{}
			if len(members) > source.limits.MaxMembers {
				return nil, tooLargeRead(bytes)
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	after, err := source.client.SCard(ctx, name).Result()
	if err != nil {
		return nil, err
	}
	if after != before || int64(len(members)) != after {
		return nil, ErrIncomplete
	}
	source.refusedMu.Lock()
	delete(source.refused, name)
	source.refusedMu.Unlock()
	result := make([]string, 0, len(members))
	for value := range members {
		result = append(result, value)
	}
	return result, nil
}

// notePage folds one scan page's time into the moving average.
func (source *SetSource) notePage(took time.Duration) {
	previous := source.pageNanos.Load()
	if previous == 0 {
		source.pageNanos.Store(int64(took))
		return
	}
	source.pageNanos.Store(previous + (int64(took)-previous)/8)
}

// RedisSubscriber owns only its PubSub connection, never the supplied client.
// Receive exposes subscription acknowledgements (including reconnects), unlike
// Channel which hides them. Acknowledgement always schedules a bounded reread.
type RedisSubscriber struct {
	client  redis.UniversalClient
	channel string
	retry   time.Duration
}

func NewRedisSubscriber(client redis.UniversalClient, prefix string, retry time.Duration) (*RedisSubscriber, error) {
	if client == nil || !validPrefix(prefix) || retry <= 0 {
		return nil, errors.New("alarmd openalerts: subscriber client, prefix and retry interval are required")
	}
	return &RedisSubscriber{client: client, channel: prefix + ":changes", retry: retry}, nil
}

func (subscriber *RedisSubscriber) Watch(ctx context.Context, ready func(bool), changed func(StrategyKey), refused func(NoticeRefusal)) error {
	refuse := func(reason NoticeRefusal) {
		if refused != nil {
			refused(reason)
		}
	}
	for ctx.Err() == nil {
		pubsub := subscriber.client.Subscribe(ctx, subscriber.channel)
		done := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				_ = pubsub.Close()
			case <-done:
			}
		}()
		for ctx.Err() == nil {
			message, err := pubsub.Receive(ctx)
			if err != nil {
				break
			}
			switch message := message.(type) {
			case *redis.Subscription:
				if message.Kind == "subscribe" && message.Channel == subscriber.channel {
					ready(true)
				}
			case *redis.Message:
				if message.Channel != subscriber.channel {
					continue
				}
				if len(message.Payload) > 64<<10 {
					refuse(NoticeOversized)
					continue
				}
				var notice struct {
					Tenant   string `json:"bk_tenant_id"`
					Strategy string `json:"strategy_id"`
				}
				decoder := json.NewDecoder(strings.NewReader(message.Payload))
				decoder.DisallowUnknownFields()
				if decoder.Decode(&notice) != nil {
					refuse(NoticeUndecodable)
					continue
				}
				var extra any
				if decoder.Decode(&extra) != io.EOF {
					refuse(NoticeUndecodable)
					continue
				}
				key := StrategyKey{TenantID: notice.Tenant, StrategyID: notice.Strategy}
				if !validStrategyKey(key) {
					refuse(NoticeInvalidKey)
					continue
				}
				changed(key)
			}
		}
		close(done)
		_ = pubsub.Close()
		ready(false)
		if !waitIndex(ctx, subscriber.retry) {
			break
		}
	}
	return ctx.Err()
}

func waitIndex(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
