// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/cmdbcache"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
)

// A replica's target group endpoint says, in one read, that its refreshes
// fail, since when and why, and its metrics count every group it holds as
// served past them: here a group whose key comes to hold another type,
// which Redis answers WRONGTYPE. A refresh reads every group or none, so the
// group beside it is served past the failure too. Read again, the failure
// clears.
func TestTheTargetGroupEndpointSaysSinceWhenAndWhyItsRefreshesFail(t *testing.T) {
	_, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	cfg := config.Default()
	prefix := "test_prefix:"
	cfg.PlatformCache.DynamicGroupKeyPrefix = &prefix
	document := func(host string) string {
		return `{"model_id":"cw-Host","model_inst_ids":["` + host + `"],"member_list":[{"model_id":"cw-Host","model_inst_id":"` +
			host + `","bk_host_id":` + host + `}]}`
	}
	for _, id := range []string{"1", "2"} {
		if err := client.Set(ctx, prefix+"dynamic_group:"+id, document(id), 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	_, groups, err := buildTargetResolver(cfg, client, nil, 1<<20)
	if err != nil || groups == nil {
		t.Fatalf("group store = %v, %v", groups, err)
	}
	for _, id := range []string{"1", "2"} {
		if lookup := groups.Group(ctx, id, time.Minute); lookup.Snapshot == nil || len(lookup.Snapshot.Members) != 1 {
			t.Fatalf("setup: group %s = %+v", id, lookup)
		}
	}
	endpoint := func(at time.Time) *fleet.WriterEvidence {
		t.Helper()
		list := withTargetGroups(func() []fleet.Endpoint {
			return []fleet.Endpoint{{Role: fleet.EndpointStateRedis}, {Role: fleet.EndpointTargetGroup}}
		}, groups, func() time.Time { return at })()
		if list[0].Writer != nil {
			t.Fatalf("an endpoint other than the target groups' got their evidence: %+v", list[0].Writer)
		}
		return list[1].Writer
	}
	if writer := endpoint(time.Now()); writer == nil || !writer.Present || writer.Count != 2 || writer.State != targetGroupLoaded ||
		writer.FailingSinceAgeSeconds != nil || writer.FailingReason != "" {
		t.Fatalf("evidence with every group read = %+v", writer)
	}

	if err := client.Del(ctx, prefix+"dynamic_group:2").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, prefix+"dynamic_group:2", "field", "value").Err(); err != nil {
		t.Fatal(err)
	}
	if err := groups.Refresh(ctx); err == nil {
		t.Fatal("a refresh Redis answered a group of with an error did not fail")
	}
	at := time.Now().Add(90 * time.Second)
	writer := endpoint(at)
	if writer == nil || writer.State != targetGroupRefreshFailed || writer.Count != 2 || writer.FailingReason != "WRONGTYPE" ||
		writer.FailingSinceAgeSeconds == nil || *writer.FailingSinceAgeSeconds < 89 || *writer.FailingSinceAgeSeconds > 120 {
		t.Fatalf("evidence with group 2 answered WRONGTYPE = %+v", writer)
	}
	reading := targetGroupReading(groups.Health())
	if reading.Groups["failing"] != 2 || reading.Groups["loaded"] != 2 || reading.Groups["referenced"] != 2 || !reading.RefreshFailed {
		t.Fatalf("metric reading with group 2 answered WRONGTYPE = %+v", reading)
	}

	if err := client.Del(ctx, prefix+"dynamic_group:2").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, prefix+"dynamic_group:2", document("2"), 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := groups.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if writer := endpoint(time.Now()); writer.State != targetGroupLoaded || writer.FailingSinceAgeSeconds != nil || writer.FailingReason != "" {
		t.Fatalf("evidence with group 2 read again = %+v", writer)
	}
}

// Each count of the store's health lands under its own name, and the state
// says a failed refresh before a store with nothing referenced: every count
// differs here, so none can stand in for another. While the refreshes fail
// every group held is failing, and the evidence says since when and why.
func TestTargetGroupHealthIsReadUnderItsOwnNames(t *testing.T) {
	at := time.Unix(10_000, 0)
	health := cmdbcache.GroupHealth{Referenced: 9, Loaded: 5, Unavailable: 2,
		RefreshFailed: true, FailingSince: at.Add(-300 * time.Second), FailureReason: "transport"}
	reading := targetGroupReading(health)
	want := map[string]int{"referenced": 9, "loaded": 5, "unavailable": 2, "failing": 7}
	for state, count := range want {
		if reading.Groups[state] != count {
			t.Fatalf("%s = %d, want %d (%+v)", state, reading.Groups[state], count, reading)
		}
	}
	if !reading.RefreshFailed {
		t.Fatalf("reading = %+v", reading)
	}
	evidence := targetGroupEvidence(health, at)
	if evidence.State != targetGroupRefreshFailed || evidence.Count != 5 || !evidence.Present || evidence.FailingReason != "transport" ||
		evidence.FailingSinceAgeSeconds == nil || *evidence.FailingSinceAgeSeconds != 300 {
		t.Fatalf("evidence = %+v", evidence)
	}
	for _, test := range []struct {
		health cmdbcache.GroupHealth
		state  string
		loaded bool
	}{
		{health: cmdbcache.GroupHealth{Referenced: 2, Loaded: 2, RefreshFailed: true}, state: targetGroupRefreshFailed, loaded: true},
		{health: cmdbcache.GroupHealth{Referenced: 2, Loaded: 2}, state: targetGroupLoaded, loaded: true},
		{health: cmdbcache.GroupHealth{Referenced: 1, Unavailable: 1}, state: targetGroupLoaded, loaded: true},
		{health: cmdbcache.GroupHealth{}, state: targetGroupNoneReferenced},
	} {
		if evidence := targetGroupEvidence(test.health, at); evidence.State != test.state || evidence.Present != test.loaded {
			t.Fatalf("%+v read %s present %v, want %s present %v", test.health, evidence.State, evidence.Present, test.state, test.loaded)
		}
	}
}
