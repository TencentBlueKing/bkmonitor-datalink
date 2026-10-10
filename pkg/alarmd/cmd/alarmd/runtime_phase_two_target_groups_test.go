// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/cmdbcache"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
)

// A replica's target group endpoint says, in one read, which groups it
// serves past refreshes that could not read them, since when and why, and
// its metrics count them: here a group whose key comes to hold another
// type, which Redis answers WRONGTYPE. The group beside it is read as
// usual and is not named. Read again, the group leaves the list.
func TestTheTargetGroupEndpointNamesEachGroupServedPastAFailedRefresh(t *testing.T) {
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
	_, groups, err := buildTargetResolver(cfg, client, nil, 1<<20, nil)
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
		len(writer.Failing) != 0 {
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
	if writer == nil || writer.State != targetGroupRefreshFailed || writer.Count != 2 || len(writer.Failing) != 1 ||
		writer.Failing[0].ID != "2" || writer.Failing[0].Reason != "WRONGTYPE" || writer.FailingTotal != 1 ||
		writer.Failing[0].SinceAgeSeconds < 89 || writer.Failing[0].SinceAgeSeconds > 120 {
		t.Fatalf("evidence with group 2 unanswered = %+v", writer)
	}
	reading := targetGroupReading(groups.Health(), at)
	if reading.Groups["failing"] != 1 || reading.Groups["loaded"] != 2 || reading.Groups["referenced"] != 2 || !reading.RefreshFailed ||
		reading.UnansweredReads != 1 || reading.OldestFailingSeconds != writer.Failing[0].SinceAgeSeconds {
		t.Fatalf("metric reading with group 2 unanswered = %+v", reading)
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
	if writer := endpoint(time.Now()); writer.State != targetGroupLoaded || len(writer.Failing) != 0 {
		t.Fatalf("evidence with group 2 read again = %+v", writer)
	}
}

// Each count of the store's health lands under its own name, and the state
// says a failed refresh before a held emptying, a held emptying before a
// store with nothing referenced: every count differs here, so none can
// stand in for another. The oldest failing group is the one failing
// longest, listed first here, not the last listed.
func TestTargetGroupHealthIsReadUnderItsOwnNames(t *testing.T) {
	at := time.Unix(10_000, 0)
	health := cmdbcache.GroupHealth{Referenced: 9, Loaded: 5, Unavailable: 2, EmptiedPending: 3, EmptiedHeld: 1,
		RefreshFailed: true, Unanswered: 7, UnansweredReads: 11,
		Failing: []cmdbcache.GroupFailure{
			{ID: "a", Since: at.Add(-300 * time.Second), Reason: "transport"},
			{ID: "b", Since: at.Add(-30 * time.Second), Reason: "LOADING"},
		}}
	reading := targetGroupReading(health, at)
	want := map[string]int{"referenced": 9, "loaded": 5, "unavailable": 2, "failing": 2, "emptied_pending": 3, "emptied_held": 1}
	for state, count := range want {
		if reading.Groups[state] != count {
			t.Fatalf("%s = %d, want %d (%+v)", state, reading.Groups[state], count, reading)
		}
	}
	if !reading.RefreshFailed || reading.UnansweredReads != 11 || reading.OldestFailingSeconds != 300 {
		t.Fatalf("reading = %+v", reading)
	}
	evidence := targetGroupEvidence(health, at)
	if evidence.State != targetGroupRefreshFailed || evidence.Count != 5 || !evidence.Present || len(evidence.Failing) != 2 ||
		evidence.Failing[0].ID != "a" || evidence.Failing[0].SinceAgeSeconds != 300 || evidence.Failing[0].Reason != "transport" ||
		evidence.Failing[1].ID != "b" || evidence.Failing[1].SinceAgeSeconds != 30 || evidence.FailingTotal != 2 ||
		evidence.FailingReasons["transport"] != 1 || evidence.FailingReasons["LOADING"] != 1 {
		t.Fatalf("evidence = %+v", evidence)
	}
	for _, test := range []struct {
		health cmdbcache.GroupHealth
		state  string
		loaded bool
	}{
		{health: cmdbcache.GroupHealth{Referenced: 2, Loaded: 2, EmptiedHeld: 2, RefreshFailed: true}, state: targetGroupRefreshFailed, loaded: true},
		{health: cmdbcache.GroupHealth{Referenced: 2, Loaded: 2, EmptiedHeld: 2}, state: targetGroupEmptiedHeld, loaded: true},
		{health: cmdbcache.GroupHealth{Referenced: 1, Unavailable: 1}, state: targetGroupLoaded, loaded: true},
		{health: cmdbcache.GroupHealth{}, state: targetGroupNoneReferenced},
	} {
		if evidence := targetGroupEvidence(test.health, at); evidence.State != test.state || evidence.Present != test.loaded {
			t.Fatalf("%+v read %s present %v, want %s present %v", test.health, evidence.State, evidence.Present, test.state, test.loaded)
		}
	}
}

// Every group fails at once when Redis loads, and the evidence rides in
// every replica's head: it names the groups failing longest, at most
// fleet.MaxFailingCopiesListed of them oldest first, and counts all of them
// by reason. Three hundred failing groups publish in a few hundred bytes.
func TestTheTargetGroupEvidenceNamesTheLongestFailingAndCountsTheRest(t *testing.T) {
	at := time.Unix(100_000, 0)
	health := cmdbcache.GroupHealth{Referenced: 300, Loaded: 300, RefreshFailed: true}
	for index := 0; index < 300; index++ {
		reason := "LOADING"
		if index%100 == 0 {
			reason = "transport"
		}
		health.Failing = append(health.Failing, cmdbcache.GroupFailure{
			ID: fmt.Sprintf("%06d", 100000+index), Since: at.Add(-time.Duration(index%50) * time.Minute), Reason: reason})
	}
	evidence := targetGroupEvidence(health, at)
	if len(evidence.Failing) != fleet.MaxFailingCopiesListed || evidence.FailingTotal != 300 ||
		evidence.FailingReasons["LOADING"] != 297 || evidence.FailingReasons["transport"] != 3 {
		t.Fatalf("evidence names %d, counts %d by %v", len(evidence.Failing), evidence.FailingTotal, evidence.FailingReasons)
	}
	// Six groups have failed 49 minutes and six 48: the six, then two of
	// the next, each age's groups by id.
	for index, failure := range evidence.Failing {
		want := 49 * 60.0
		if index >= 6 {
			want = 48 * 60
		}
		if failure.SinceAgeSeconds != want {
			t.Fatalf("named %d is %+v, want a group failing %v seconds", index, failure, want)
		}
		if index > 0 && failure.SinceAgeSeconds == evidence.Failing[index-1].SinceAgeSeconds && failure.ID <= evidence.Failing[index-1].ID {
			t.Fatalf("groups failing as long are named out of order: %+v", evidence.Failing)
		}
	}
	encoded, err := json.Marshal(evidence)
	if err != nil || len(encoded) > 1024 {
		t.Fatalf("the evidence publishes as %d bytes, %v; want under 1 KiB", len(encoded), err)
	}
}
