package datasources

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	redis "github.com/redis/go-redis/v9"
)

type dynamicGroupCacheStub struct {
	key, field string
	value      string
	err        error
	reads      int
}

func (s *dynamicGroupCacheStub) HStrLen(_ context.Context, key, field string) *redis.IntCmd {
	s.key, s.field = key, field
	return redis.NewIntResult(int64(len(s.value)), s.err)
}

func (s *dynamicGroupCacheStub) HGet(_ context.Context, key, field string) *redis.StringCmd {
	s.reads++
	s.key, s.field = key, field
	return redis.NewStringResult(s.value, s.err)
}

func TestDynamicGroupClientReadsCurrentWriterProjection(t *testing.T) {
	cache := &dynamicGroupCacheStub{value: `{"group_ids":[12,2,12]}`}
	client, err := NewDynamicGroupClient(map[string]DynamicGroupTenantCache{"tenant-a": {Client: cache, KeyPrefix: "bk_monitor:"}})
	if err != nil {
		t.Fatal(err)
	}
	ids, err := client.GetDynamicGroupIDs(t.Context(), "tenant-a", "cw-Host", "101")
	if err != nil || !reflect.DeepEqual(ids, []string{"2", "12"}) {
		t.Fatalf("ids=%v error=%v", ids, err)
	}
	if cache.key != "bk_monitor:dynamic_inst_group:cw-Host" || cache.field != "101" || cache.reads != 1 {
		t.Fatalf("lookup key=%q field=%q reads=%d", cache.key, cache.field, cache.reads)
	}
	if _, err := client.GetDynamicGroupIDs(t.Context(), "tenant-b", "cw-Host", "101"); err == nil || cache.reads != 1 {
		t.Fatalf("unconfigured tenant accessed cache: %v", err)
	}
}

func TestDynamicGroupClientMissingAndInvalidProjection(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		err   error
		valid bool
	}{
		{name: "missing", valid: true},
		{name: "empty list", value: `{"group_ids":[]}`, valid: true},
		{name: "missing group IDs", value: `{}`, valid: false},
		{name: "malformed", value: `{"group_ids":`, valid: false},
		{name: "wrong type", value: `{"group_ids":["12"]}`, valid: false},
		{name: "negative ID", value: `{"group_ids":[-1]}`, valid: false},
		{name: "read failure", value: `{"group_ids":[12]}`, err: errors.New("redis failed"), valid: false},
		{name: "oversized value", value: strings.Repeat("x", maxDynamicGroupValueBytes+1), valid: false},
		{name: "too many IDs", value: `{"group_ids":[` + strings.Repeat("1,", maxDynamicGroupIDs) + `1]}`, valid: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := &dynamicGroupCacheStub{value: tc.value, err: tc.err}
			client, err := NewDynamicGroupClient(map[string]DynamicGroupTenantCache{"tenant-a": {Client: cache, KeyPrefix: "bk_monitor:"}})
			if err != nil {
				t.Fatal(err)
			}
			ids, err := client.GetDynamicGroupIDs(t.Context(), "tenant-a", "cw-Host", "101")
			if (err == nil) != tc.valid || tc.valid && len(ids) != 0 {
				t.Fatalf("ids=%v error=%v", ids, err)
			}
		})
	}
}

func TestDynamicGroupClientCancellationSkipsRedis(t *testing.T) {
	cache := &dynamicGroupCacheStub{value: `{"group_ids":[12]}`}
	client, err := NewDynamicGroupClient(map[string]DynamicGroupTenantCache{"tenant-a": {Client: cache, KeyPrefix: "bk_monitor:"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := client.GetDynamicGroupIDs(ctx, "tenant-a", "cw-Host", "101"); !errors.Is(err, context.Canceled) || cache.reads != 0 || cache.key != "" {
		t.Fatalf("canceled lookup error=%v cache=%#v", err, cache)
	}
}
