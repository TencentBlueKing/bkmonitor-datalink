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
	"time"

	"github.com/go-redis/redis/v8"
)

var ErrIncomplete = errors.New("alarmd openalerts: incomplete index observation")

// ErrSetTooLarge is a set larger than one read may hold - more members or
// bytes than the read's bounds, or more pages than it may take. It is an
// incomplete read to every caller that asks (errors.Is), and named apart so
// the copy counts it apart from a read that failed: a set this size is not
// an outage, and is read in no other way yet.
var ErrSetTooLarge = fmt.Errorf("alarmd openalerts: set too large for one read: %w", ErrIncomplete)
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

type ReadLimits struct {
	MaxMembers int
	MaxBytes   int
	MaxPages   int
	PageSize   int64
}

type SetSource struct {
	client redis.Cmdable
	prefix string
	limits ReadLimits
}

func validPrefix(prefix string) bool {
	return prefix != "" && len(prefix) <= 256 && strings.TrimSpace(prefix) == prefix
}

func validStrategyKey(key StrategyKey) bool {
	// A delimiter in either identity aliases another tenant/strategy pair.
	return key.TenantID != "" && len(key.TenantID) <= 256 && key.StrategyID != "" && len(key.StrategyID) <= 1024 &&
		!strings.ContainsAny(key.TenantID+key.StrategyID, ":\x00")
}

func NewSetSource(client redis.Cmdable, prefix string, limits ReadLimits) (*SetSource, error) {
	if client == nil || !validPrefix(prefix) || limits.MaxMembers <= 0 || limits.MaxBytes <= 0 || limits.MaxPages <= 0 || limits.PageSize <= 0 {
		return nil, errors.New("alarmd openalerts: Redis client, prefix and positive read limits are required")
	}
	return &SetSource{client: client, prefix: prefix, limits: limits}, nil
}

func (source *SetSource) ReadSet(ctx context.Context, key StrategyKey) ([]string, error) {
	if !validStrategyKey(key) {
		return nil, errors.New("alarmd openalerts: invalid strategy identity")
	}
	name := source.prefix + ":" + key.TenantID + ":" + key.StrategyID
	before, err := source.client.SCard(ctx, name).Result()
	if err != nil {
		return nil, err
	}
	if before > int64(source.limits.MaxMembers) {
		return nil, ErrSetTooLarge
	}
	members := make(map[string]struct{})
	var cursor uint64
	bytes := 0
	for page := 0; page < source.limits.MaxPages; page++ {
		values, next, err := source.client.SScan(ctx, name, cursor, "", source.limits.PageSize).Result()
		if err != nil {
			return nil, err
		}
		for _, value := range values {
			bytes += len(value)
			// A member no alert id can be is a read refused, not a set too
			// large.
			if value == "" || len(value) > 4096 {
				return nil, ErrIncomplete
			}
			if bytes > source.limits.MaxBytes {
				return nil, ErrSetTooLarge
			}
			members[value] = struct{}{}
			if len(members) > source.limits.MaxMembers {
				return nil, ErrSetTooLarge
			}
		}
		cursor = next
		if cursor == 0 {
			after, err := source.client.SCard(ctx, name).Result()
			if err != nil {
				return nil, err
			}
			if after != before || int64(len(members)) != after {
				return nil, ErrIncomplete
			}
			result := make([]string, 0, len(members))
			for value := range members {
				result = append(result, value)
			}
			return result, nil
		}
	}
	return nil, ErrSetTooLarge
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
