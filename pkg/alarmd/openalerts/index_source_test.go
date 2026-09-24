// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package openalerts

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
)

func indexRedis(t *testing.T) *redis.Client {
	t.Helper()
	executable, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server is not installed")
	}
	address := reserveTCPAddress(t)
	startRedisServer(t, executable, address)
	client := redis.NewClient(&redis.Options{Addr: address, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second})
	t.Cleanup(func() { _ = client.Close() })
	waitRedisReady(t, client)
	return client
}

func TestSetSourceReadsWithoutHeartbeatAndBoundsPayload(t *testing.T) {
	client := indexRedis(t)
	ctx := context.Background()
	source, err := NewSetSource(client, "test:index", ReadLimits{MaxMembers: 2, MaxBytes: 20, MaxPages: 10, PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	key := "test:index:" + keyA.TenantID + ":" + keyA.StrategyID
	if err := client.SAdd(ctx, key, "one").Err(); err != nil {
		t.Fatal(err)
	}
	members, err := source.ReadSet(ctx, keyA)
	if err != nil || !reflect.DeepEqual(members, []string{"one"}) {
		t.Fatalf("members %v error %v", members, err)
	}
	if err := client.SAdd(ctx, key, "two", "three").Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := source.ReadSet(ctx, keyA); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("member limit: %v", err)
	}
	if err := client.Del(ctx, key).Err(); err != nil {
		t.Fatal(err)
	}
	if members, err := source.ReadSet(ctx, keyA); err != nil || len(members) != 0 {
		t.Fatalf("deleted key = %v, %v", members, err)
	}
	if err := client.SAdd(ctx, key, "this-member-exceeds-twenty-bytes").Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := source.ReadSet(ctx, keyA); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("byte limit: %v", err)
	}
	if _, err := source.ReadSet(ctx, StrategyKey{TenantID: "a:b", StrategyID: "c"}); err == nil {
		t.Fatal("tenant alias accepted")
	}
}

func TestRedisSubscriberAcknowledgesReconnectAndFiltersInvalidNotices(t *testing.T) {
	client := indexRedis(t)
	subscriber, err := NewRedisSubscriber(client, "test:index", 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan bool, 20)
	changes := make(chan StrategyKey, 20)
	done := make(chan error, 1)
	go func() {
		done <- subscriber.Watch(ctx, func(value bool) { ready <- value }, func(key StrategyKey) { changes <- key })
	}()
	waitReady := func(wanted bool) {
		t.Helper()
		select {
		case actual := <-ready:
			if actual != wanted {
				t.Fatalf("ready %v want %v", actual, wanted)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("no subscription acknowledgement")
		}
	}
	waitReady(true)
	for _, payload := range []string{`{"bk_tenant_id":"x","strategy_id":"y","key":"foreign"}`, `{"bk_tenant_id":"x","strategy_id":1}`, `{"bk_tenant_id":"a:b","strategy_id":"c"}`} {
		if err := client.Publish(ctx, "test:index:changes", payload).Err(); err != nil {
			t.Fatal(err)
		}
	}
	valid := `{"bk_tenant_id":"default","strategy_id":"1001"}`
	if err := client.Publish(ctx, "test:index:changes", valid).Err(); err != nil {
		t.Fatal(err)
	}
	select {
	case key := <-changes:
		if key != keyA {
			t.Fatalf("unexpected notice %+v", key)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("valid notice lost")
	}
	if err := client.Do(ctx, "CLIENT", "KILL", "TYPE", "pubsub").Err(); err != nil {
		t.Fatal(err)
	}
	waitReady(false)
	// Pub/Sub has no replay. A reconnect acknowledgement is the signal the
	// cache uses to reread this mutation, which intentionally has no notice.
	if err := client.SAdd(ctx, "test:index:default:1001", "missed").Err(); err != nil {
		t.Fatal(err)
	}
	waitReady(true)
	source, _ := NewSetSource(client, "test:index", ReadLimits{MaxMembers: 10, MaxBytes: 100, MaxPages: 10, PageSize: 10})
	if members, err := source.ReadSet(ctx, keyA); err != nil || !reflect.DeepEqual(members, []string{"missed"}) {
		t.Fatalf("reconnect read %v, %v", members, err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("subscriber did not close")
	}
}
