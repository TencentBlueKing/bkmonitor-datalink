// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package openalerts

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
)

func indexRedis(t *testing.T) *redis.Client {
	t.Helper()
	executable := redistest.Server(t)
	address := reserveTCPAddress(t)
	startRedisServer(t, executable, address)
	client := redis.NewClient(&redis.Options{Addr: address, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second})
	t.Cleanup(func() { _ = client.Close() })
	waitRedisReady(t, client)
	return client
}

func reserveTCPAddress(t *testing.T) string {
	t.Helper()
	return redistest.FreeAddress(t)
}

func startRedisServer(t *testing.T, _ string, address string) {
	t.Helper()
	redistest.StartAt(t, address)
}

func waitRedisReady(t *testing.T, client *redis.Client) {
	t.Helper()
	// The server answered before StartAt returned; what is left is this
	// client's own connection, under the same bound.
	deadline := time.Now().Add(redistest.ReadyWithin)
	for time.Now().Before(deadline) {
		if err := client.Ping(context.Background()).Err(); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("redis-server did not become ready")
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
	var refusedMu sync.Mutex
	refused := map[NoticeRefusal]int{}
	go func() {
		done <- subscriber.Watch(ctx, func(value bool) { ready <- value }, func(key StrategyKey) { changes <- key },
			func(reason NoticeRefusal) { refusedMu.Lock(); refused[reason]++; refusedMu.Unlock() })
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
	// Every notice dropped is named by why, never silently: a field this
	// build does not know, a wrong type, content after the notice, a key
	// that is not a valid tenant and strategy, and a payload past the bound.
	oversized := `{"bk_tenant_id":"x","strategy_id":"` + strings.Repeat("y", 64<<10) + `"}`
	for _, payload := range []string{`{"bk_tenant_id":"x","strategy_id":"y","key":"foreign"}`, `{"bk_tenant_id":"x","strategy_id":1}`,
		`{"bk_tenant_id":"x","strategy_id":"y"} {}`, `{"bk_tenant_id":"a:b","strategy_id":"c"}`, oversized} {
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
	refusedMu.Lock()
	if want := map[NoticeRefusal]int{NoticeUndecodable: 3, NoticeInvalidKey: 1, NoticeOversized: 1}; !reflect.DeepEqual(refused, want) {
		t.Fatalf("refused = %v, want %v", refused, want)
	}
	refusedMu.Unlock()
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

// The read's size bounds refuse a set as too large - more members than the
// read may hold before or while it reads, more bytes, more pages - and a
// member no alert id can be refuses it as incomplete only: the copy counts
// the first apart (UnavailableTooLarge) and the second as a failed read.
func TestASetBeyondTheReadsBoundsIsRefusedAsTooLarge(t *testing.T) {
	client := indexRedis(t)
	ctx := context.Background()
	key := "test:index:" + keyA.TenantID + ":" + keyA.StrategyID
	read := func(limits ReadLimits, members ...string) error {
		t.Helper()
		if err := client.Del(ctx, key).Err(); err != nil {
			t.Fatal(err)
		}
		values := make([]any, 0, len(members))
		for _, member := range members {
			values = append(values, member)
		}
		if err := client.SAdd(ctx, key, values...).Err(); err != nil {
			t.Fatal(err)
		}
		source, err := NewSetSource(client, "test:index", limits)
		if err != nil {
			t.Fatal(err)
		}
		_, err = source.ReadSet(ctx, keyA)
		return err
	}
	wide := ReadLimits{MaxMembers: 10, MaxBytes: 1 << 20, MaxPages: 10, PageSize: 10}
	paged := make([]string, 0, 200)
	for index := 0; index < 200; index++ {
		paged = append(paged, "member-"+strconv.Itoa(index))
	}
	for name, c := range map[string]struct {
		limits   ReadLimits
		members  []string
		tooLarge bool
	}{
		"more members than the read holds": {ReadLimits{MaxMembers: 2, MaxBytes: 1 << 20, MaxPages: 10, PageSize: 10}, []string{"a", "b", "c"}, true},
		"more bytes than the read holds":   {ReadLimits{MaxMembers: 10, MaxBytes: 4, MaxPages: 10, PageSize: 10}, []string{"abc", "def"}, true},
		// Past the size a set is kept compact in, so a scan pages it.
		"more pages than the read takes": {ReadLimits{MaxMembers: 1000, MaxBytes: 1 << 20, MaxPages: 1, PageSize: 1}, paged, true},
		"a member no alert id can be":    {wide, []string{strings.Repeat("x", 4097)}, false},
	} {
		err := read(c.limits, c.members...)
		if !errors.Is(err, ErrIncomplete) || errors.Is(err, ErrSetTooLarge) != c.tooLarge {
			t.Errorf("%s: %v, want incomplete and too large %t", name, err, c.tooLarge)
		}
	}
}
