// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/targetplan"
)

// A dynamic group's snapshot, kept past refreshes that fail at the transport,
// is served up to the staleness bound and labelled with its age, and is not
// served one second past it (decision-017 section 3.2: the store keeps the
// last snapshot and answers from it while its age is at most MaxAge, every
// round that eats the old snapshot says so as resolved_from_stale_snapshot
// with the age, and past MaxAge the selector is Unavailable as stale). The
// bound is 10 minutes, the host index's own (section 3.1).
//
// Read at three ages around the bound - one second short, exactly on it, one
// second past - because the comparison is the whole rule and a > that became
// a >= moves the one row in the middle.
func TestAGroupServedPastAFailedRefreshIsLabelledUpToTheBoundAndUnavailablePastIt(t *testing.T) {
	const bound = 10 * time.Minute
	readAt := time.Unix(1_700_000_000, 0)
	now := readAt
	clock := func() time.Time { return now }
	client := &groupClient{values: map[string]string{"cw:dynamic_group:g": `{"model_id":"cw-Host","model_inst_ids":["101"],` +
		`"member_list":[{"model_id":"cw-Host","model_inst_id":"101","bk_host_id":101}]}`}}
	reader, err := NewGroupReader(client, "cw:")
	if err != nil {
		t.Fatal(err)
	}
	groups, err := NewGroupStore(reader, GroupStoreOptions{RefreshInterval: time.Minute, MaxAge: bound,
		ReadBound: testGroupReadBound, Now: clock})
	if err != nil {
		t.Fatal(err)
	}
	resolver := NewTargetResolver(groups, nil, clock)
	plan := &contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-Host", Rule: contract.TargetPlanRuleHostID,
		Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true}, DynamicGroups: []string{"g"}}

	fresh := resolver.Resolve(context.Background(), plan, time.Minute)
	if group := selector(fresh, targetplan.SelectorKindGroup, "g"); group.State != targetplan.SelectorOK || group.StaleAge != 0 ||
		fresh.State != targetplan.ResolutionComplete || !fresh.Contains("101") {
		t.Fatalf("a group just read answered %+v (plan %s); want OK, current, holding 101", group, fresh.State)
	}

	// Every refresh from here on fails at the transport.
	client.err = errors.New("connection refused")
	now = readAt.Add(time.Minute)
	if err := groups.Refresh(context.Background()); err == nil {
		t.Fatal("a refresh over a failing transport succeeded")
	}
	for _, test := range []struct {
		age    time.Duration
		served bool
	}{
		{age: bound - time.Second, served: true},
		{age: bound, served: true},
		{age: bound + time.Second, served: false},
	} {
		now = readAt.Add(test.age)
		resolution := resolver.Resolve(context.Background(), plan, time.Minute)
		group := selector(resolution, targetplan.SelectorKindGroup, "g")
		if test.served {
			if group.State != targetplan.SelectorOK || group.StaleAge != test.age || resolution.StaleAge != test.age ||
				resolution.State != targetplan.ResolutionComplete || !resolution.Contains("101") {
				t.Fatalf("at age %s the group answered %+v, plan %s stale %s; want it served, holding 101, labelled %s old",
					test.age, group, resolution.State, resolution.StaleAge, test.age)
			}
			continue
		}
		if group.State != targetplan.SelectorUnavailable || group.Reason != targetplan.ReasonStale ||
			resolution.State != targetplan.ResolutionUnavailable || resolution.Contains("101") {
			t.Fatalf("at age %s the group answered %+v, plan %s; want it unavailable as stale, holding nothing",
				test.age, group, resolution.State)
		}
	}
}
