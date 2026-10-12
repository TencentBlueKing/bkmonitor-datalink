// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

// The platform keeps its dynamic groups in one hash per tenant beside the
// host hash: "<prefix>.cache.cmdb.dynamic_group", another tenant's with the
// tenant in front, field the group id, value {"bk_obj_id", "bk_inst_ids"}
// (bk-monitor-worker's DynamicGroupCacheManager writes it; Python's
// DynamicGroupManager.mget reads it). These run on a real Redis: what HMGET
// answers for a missing field, a missing hash and a key of another type is
// the server's, not a fake's.

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
)

const platformGroupHash = "bk_monitorv3.ce.cache.cmdb.dynamic_group"

func platformGroupRedis(t *testing.T) *redis.Client {
	t.Helper()
	server := redistest.Start(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr, MaxRetries: -1, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func platformGroupStores(t *testing.T, client redis.Cmdable, now func() time.Time) *PlatformGroupStores {
	t.Helper()
	return platformGroupStoresBound(t, client, now, 1<<20)
}

func platformGroupStoresBound(t *testing.T, client redis.Cmdable, now func() time.Time, bound int) *PlatformGroupStores {
	t.Helper()
	stores, err := NewPlatformGroupStores(client, "bk_monitorv3.ce", GroupStoreOptions{RefreshInterval: time.Minute, MaxAge: 10 * time.Minute, ReadBound: bound, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	return stores
}

func hmgetCalls(t *testing.T, client *redis.Client) int64 {
	t.Helper()
	return commandCalls(t, client, "hmget")
}

func commandCalls(t *testing.T, client *redis.Client, command string) int64 {
	t.Helper()
	info, err := client.Info(context.Background(), "commandstats").Result()
	if err != nil {
		t.Fatal(err)
	}
	_, rest, found := strings.Cut(info, "cmdstat_"+command+":calls=")
	if !found {
		return 0
	}
	calls, err := strconv.ParseInt(rest[:strings.IndexByte(rest, ',')], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return calls
}

func sortedMembers(members map[string]struct{}) string {
	keys := make([]string, 0, len(members))
	for key := range members {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

func TestAPlatformGroupIsReadFromItsTenantsHashAsPythonReadsIt(t *testing.T) {
	ctx := context.Background()
	client := platformGroupRedis(t)
	if err := client.HSet(ctx, platformGroupHash,
		"03c79170-7ee1-11ee-a820-5e22272a2c60", `{"bk_biz_id":7,"bk_inst_ids":[730002,730001],"bk_obj_id":"host","name":"kafka","id":"03c79170-7ee1-11ee-a820-5e22272a2c60"}`,
		"empty", `{"bk_biz_id":7,"bk_inst_ids":[],"bk_obj_id":"host","name":"none","id":"empty"}`,
		"sets", `{"bk_biz_id":7,"bk_inst_ids":[12],"bk_obj_id":"set","name":"sets","id":"sets"}`,
		"broken", `{"bk_obj_id":"host","bk_inst_ids":[1`,
		"unlisted", `{"bk_biz_id":7,"bk_obj_id":"host","name":"unlisted","id":"unlisted"}`,
	).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, "tenant-a."+platformGroupHash, "03c79170-7ee1-11ee-a820-5e22272a2c60",
		`{"bk_biz_id":9,"bk_inst_ids":["740001"],"bk_obj_id":"host","name":"kafka","id":"03c79170-7ee1-11ee-a820-5e22272a2c60"}`).Err(); err != nil {
		t.Fatal(err)
	}
	stores := platformGroupStores(t, client, time.Now)
	for _, c := range []struct {
		tenant, id, state, reason, members string
	}{
		{"", "03c79170-7ee1-11ee-a820-5e22272a2c60", "OK", "none", "730001,730002"},
		{"system", "03c79170-7ee1-11ee-a820-5e22272a2c60", "OK", "none", "730001,730002"},
		{"tenant-a", "03c79170-7ee1-11ee-a820-5e22272a2c60", "OK", "none", "740001"},
		{"", "empty", "OKEmpty", "none", ""},
		// Python matches only a host group's members.
		{"", "sets", "Unavailable", "model_mismatch", ""},
		{"", "broken", "Unavailable", "json_invalid", ""},
		{"", "unlisted", "Unavailable", "structure_invalid", ""},
		{"", "gone", "Unavailable", "key_missing", ""},
		// A tenant whose hash is not there.
		{"tenant-b", "03c79170-7ee1-11ee-a820-5e22272a2c60", "Unavailable", "key_missing", ""},
	} {
		got := stores.ResolveScopeGroup(ctx, c.tenant, c.id, time.Minute)
		if string(got.State) != c.state || got.Reason != c.reason || sortedMembers(got.Members) != c.members || got.Kind != "dynamic_group" || got.ID != c.id {
			t.Errorf("%q/%s = %s %s [%s], want %s %s [%s]", c.tenant, c.id, got.State, got.Reason, sortedMembers(got.Members), c.state, c.reason, c.members)
		}
	}
	if got := (*PlatformGroupStores)(nil).ResolveScopeGroup(ctx, "", "empty", time.Minute); string(got.State) != "Unavailable" || got.Reason != "source_unwired" {
		t.Fatalf("no stores = %s %s", got.State, got.Reason)
	}
}

// A group is read on its first reference and refreshed with the others; a
// refresh that fails keeps what was read, served with its age, until the
// staleness bound, past which it is not served; a refresh that reads again
// ends it.
func TestAPlatformGroupPastAFailedRefreshIsAgedAndPastTheBoundIsStale(t *testing.T) {
	ctx := context.Background()
	client := platformGroupRedis(t)
	if err := client.HSet(ctx, platformGroupHash, "g1", `{"bk_inst_ids":[730001],"bk_obj_id":"host"}`).Err(); err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1700000000, 0).UTC()
	stores := platformGroupStores(t, client, func() time.Time { return at })
	if got := stores.ResolveScopeGroup(ctx, "", "g1", time.Minute); string(got.State) != "OK" || got.StaleAge != 0 {
		t.Fatalf("first reference = %s, stale age %v", got.State, got.StaleAge)
	}
	// The hash becomes a key of another type: the refresh fails.
	if err := client.Del(ctx, platformGroupHash).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, platformGroupHash, "not a hash", 0).Err(); err != nil {
		t.Fatal(err)
	}
	at = at.Add(2 * time.Minute)
	if err := stores.Refresh(ctx); err == nil {
		t.Fatal("a refresh over a key of another type succeeded")
	}
	if got := stores.ResolveScopeGroup(ctx, "", "g1", time.Minute); string(got.State) != "OK" || got.StaleAge != 2*time.Minute || sortedMembers(got.Members) != "730001" {
		t.Fatalf("past a failed refresh = %s [%s], stale age %v, want OK [730001] at 2m", got.State, sortedMembers(got.Members), got.StaleAge)
	}
	at = at.Add(9 * time.Minute)
	if got := stores.ResolveScopeGroup(ctx, "", "g1", time.Minute); string(got.State) != "Unavailable" || got.Reason != "stale" {
		t.Fatalf("past the bound = %s %s, want Unavailable stale", got.State, got.Reason)
	}
	if err := client.Del(ctx, platformGroupHash).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, platformGroupHash, "g1", `{"bk_inst_ids":[730003],"bk_obj_id":"host"}`).Err(); err != nil {
		t.Fatal(err)
	}
	if err := stores.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if got := stores.ResolveScopeGroup(ctx, "", "g1", time.Minute); string(got.State) != "OK" || got.StaleAge != 0 || sortedMembers(got.Members) != "730003" {
		t.Fatalf("after a refresh that read = %s [%s] stale %v", got.State, sortedMembers(got.Members), got.StaleAge)
	}
}

// One read holds at most its bound of group values: the values' lengths are
// read first, and one HMGET names the groups whose values add up to at most
// the bound, or the one larger than the bound alone. The server's own
// command counts.
func TestAPlatformGroupReadHoldsAtMostItsBoundOfValues(t *testing.T) {
	ctx := context.Background()
	client := platformGroupRedis(t)
	group := func(hosts, from int) string {
		ids := make([]string, hosts)
		for index := range ids {
			ids[index] = strconv.Itoa(from + index)
		}
		return `{"bk_inst_ids":[` + strings.Join(ids, ",") + `],"bk_obj_id":"host"}`
	}
	values := map[string]string{"big": group(700, 700000)}
	for index := range 5 {
		values[fmt.Sprintf("g%d", index)] = group(137, 710000+index*1000)
	}
	small, big := len(values["g0"]), len(values["big"])
	const bound = 2500
	if 2*small > bound || 3*small <= bound || big <= bound {
		t.Fatalf("the fixture's sizes do not set the windows: small %d, big %d, bound %d", small, big, bound)
	}
	for id, value := range values {
		if err := client.HSet(ctx, platformGroupHash, id, value).Err(); err != nil {
			t.Fatal(err)
		}
	}
	stores := platformGroupStoresBound(t, client, time.Now, bound)
	for _, id := range []string{"big", "g0", "g1", "g2", "g3", "g4"} {
		if got := stores.ResolveScopeGroup(ctx, "", id, time.Minute); string(got.State) != "OK" {
			t.Fatalf("%s = %s %s", id, got.State, got.Reason)
		}
	}
	hmget, hstrlen := hmgetCalls(t, client), commandCalls(t, client, "hstrlen")
	if err := stores.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	// big alone, then g0+g1, g2+g3, g4.
	if calls := hmgetCalls(t, client) - hmget; calls != 4 {
		t.Fatalf("a refresh within a %d-byte bound sent %d HMGET, want 4", bound, calls)
	}
	if calls := commandCalls(t, client, "hstrlen") - hstrlen; calls != 6 {
		t.Fatalf("a refresh of 6 groups read %d lengths, want 6", calls)
	}
	if got := stores.ResolveScopeGroup(ctx, "", "big", time.Minute); got.Kept != 700 {
		t.Fatalf("the group larger than the bound kept %d hosts, want 700", got.Kept)
	}
}

// A refresh names at most 64 groups per HMGET, and a deployment whose
// strategies name no group sends none: the server's own command counts.
func TestAPlatformGroupRefreshIsBatchedAndCostsNothingWithoutGroups(t *testing.T) {
	ctx := context.Background()
	client := platformGroupRedis(t)
	stores := platformGroupStores(t, client, time.Now)
	if err := stores.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if calls := hmgetCalls(t, client); calls != 0 {
		t.Fatalf("a refresh with no group referenced sent %d HMGET", calls)
	}
	for index := range 130 {
		id := fmt.Sprintf("g%03d", index)
		if err := client.HSet(ctx, platformGroupHash, id, fmt.Sprintf(`{"bk_inst_ids":[%d],"bk_obj_id":"host"}`, 700000+index)).Err(); err != nil {
			t.Fatal(err)
		}
		if got := stores.ResolveScopeGroup(ctx, "", id, time.Minute); string(got.State) != "OK" {
			t.Fatalf("%s = %s %s", id, got.State, got.Reason)
		}
	}
	before := hmgetCalls(t, client)
	if before != 130 {
		t.Fatalf("130 first references sent %d HMGET, want one each", before)
	}
	if err := stores.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if calls := hmgetCalls(t, client) - before; calls != 3 {
		t.Fatalf("a refresh of 130 groups sent %d HMGET, want 3 (64+64+2)", calls)
	}
	if got := stores.ResolveScopeGroup(ctx, "", "g129", time.Minute); sortedMembers(got.Members) != "700129" {
		t.Fatalf("the last group of the last batch = [%s]", sortedMembers(got.Members))
	}
}
