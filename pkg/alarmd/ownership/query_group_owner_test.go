// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package ownership

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func TestReadQueryGroupOwnerUsesActiveLeaseAndRedisTime(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	identity := execution.QueryGroupIdentity("owner-read-fixture")
	key := store.ownershipKey(identity)
	now, err := store.client.Time(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	deadline := now.Add(time.Minute).UnixMilli()
	facts := map[string]any{"owner_id": "lease-worker", "owner_epoch": "19", "deadline_ms": deadline,
		"execution_disposition": "ACTIVE", "lease_token": "fixture-only-secret"}
	if err := store.client.HSet(ctx, key, facts).Err(); err != nil {
		t.Fatal(err)
	}
	// Desired placement can already point elsewhere while the old lease is live.
	if err := store.client.HSet(ctx, store.assignmentKey(identity), "desired_worker_id", "desired-worker").Err(); err != nil {
		t.Fatal(err)
	}
	before, err := store.client.HGetAll(ctx, key).Result()
	if err != nil {
		t.Fatal(err)
	}
	owner, found, err := store.ReadQueryGroupOwner(ctx, identity)
	if err != nil || !found || owner.OwnerID != "lease-worker" || owner.OwnerEpoch != 19 || owner.Deadline.UnixMilli() != deadline {
		t.Fatalf("unexpected owner: %+v found=%v err=%v", owner, found, err)
	}
	afterTime, err := store.client.Time(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	if owner.ObservedAt.Before(now.Truncate(time.Millisecond)) || owner.ObservedAt.After(afterTime) {
		t.Fatal("observation did not use Redis TIME")
	}
	after, err := store.client.HGetAll(ctx, key).Result()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("owner query mutated the lease")
	}
	encoded, _ := json.Marshal(owner)
	if strings.Contains(string(encoded), "fixture-only-secret") || strings.Contains(string(encoded), "lease_token") {
		t.Fatal("owner result exposed a lease credential")
	}

	for _, tc := range []struct {
		name   string
		mutate func() error
	}{
		{"expired", func() error {
			return store.client.HSet(ctx, key, "deadline_ms", now.Add(-time.Millisecond).UnixMilli()).Err()
		}},
		{"paused", func() error {
			return store.client.HSet(ctx, key, "deadline_ms", deadline, "execution_disposition", "PAUSED").Err()
		}},
		{"missing disposition", func() error { return store.client.HDel(ctx, key, "execution_disposition").Err() }},
		{"released", func() error {
			return store.client.HSet(ctx, key, "execution_disposition", "ACTIVE", "owner_id", "").Err()
		}},
		{"missing lease despite desired worker", func() error { return store.client.Del(ctx, key).Err() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.mutate(); err != nil {
				t.Fatal(err)
			}
			owner, found, err := store.ReadQueryGroupOwner(ctx, identity)
			if err != nil || found || owner != (QueryGroupOwner{}) {
				t.Fatalf("unavailable owner returned: %+v %v %v", owner, found, err)
			}
		})
	}
}

func TestReadQueryGroupOwnerRejectsInvalidFacts(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	identity := execution.QueryGroupIdentity("invalid-owner")
	key := store.ownershipKey(identity)
	now, err := store.client.Time(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	for _, epoch := range []string{"0", "-1", "invalid", "18446744073709551616"} {
		if err := store.client.HSet(ctx, key, "owner_id", "worker", "owner_epoch", epoch, "deadline_ms", now.Add(time.Minute).UnixMilli(), "execution_disposition", "ACTIVE").Err(); err != nil {
			t.Fatal(err)
		}
		if _, found, err := store.ReadQueryGroupOwner(ctx, identity); err == nil || found {
			t.Fatalf("invalid epoch %q accepted", epoch)
		}
	}
	if _, _, err := store.ReadQueryGroupOwner(ctx, ""); err == nil {
		t.Fatal("empty Query Group accepted")
	}
	if _, _, err := (*RedisStore)(nil).ReadQueryGroupOwner(ctx, identity); err == nil {
		t.Fatal("nil store accepted")
	}
	if err := store.client.Del(ctx, key).Err(); err != nil {
		t.Fatal(err)
	}
	if err := store.client.Set(ctx, key, "wrong-type", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.ReadQueryGroupOwner(ctx, identity); err == nil || found {
		t.Fatal("store error became an absent owner")
	}
}

func TestReadActiveControlLeaderUsesLiveSpecialLeaseWithoutExposingToken(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	if _, found, err := store.ReadActiveControlLeader(ctx); err != nil || found {
		t.Fatal("missing control leader was not absent")
	}
	authority, err := store.AcquireControlLeader(ctx, "control-worker", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	owner, found, err := store.ReadActiveControlLeader(ctx)
	if err != nil || !found || owner.OwnerID != "control-worker" || owner.OwnerEpoch != authority.Fence.OwnerEpoch || !owner.Deadline.After(owner.ObservedAt) {
		t.Fatalf("active control leader=%+v found=%v err=%v", owner, found, err)
	}
	raw, _ := json.Marshal(owner)
	if strings.Contains(string(raw), "lease_token") || strings.Contains(string(raw), authority.Fence.LeaseToken) {
		t.Fatal("control leader read exposed the lease token")
	}
	elapseOnRedis(t, store, ControlLeaderIdentity, time.Minute+time.Second)
	if _, found, err := store.ReadActiveControlLeader(ctx); err != nil || found {
		t.Fatal("expired control lease was accepted")
	}
	legacy, found, err := store.ReadControlLeader(ctx)
	if err != nil || !found || legacy.OwnerID != owner.OwnerID || legacy.OwnerEpoch != owner.OwnerEpoch {
		t.Fatal("the legacy control leader read changed semantics")
	}
}
