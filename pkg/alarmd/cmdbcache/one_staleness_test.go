// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

// One judgement on the host index (Store.judge, read through Usable): never
// loaded, past its staleness bound, or empty, and every caller - admission's
// fullers, the target selectors and exclusions, no-data's host resolution
// and attribution - reads the same answer for the same snapshot. The facts
// are aged from the writer's publish time when it writes one, held to three
// of its rounds, and from alarmd's own read otherwise. The other writer's
// full-pass attempt time is shown and never judged on.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/admission"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/targetplan"
)

// markerClient is a hash client that also answers GET for the marker keys,
// and records every read in order.
type markerClient struct {
	*hashClient
	strings map[string]string
	failing map[string]error
}

func (client *markerClient) Get(_ context.Context, key string) *redis.StringCmd {
	client.scans = append(client.scans, "GET "+key)
	if err, failing := client.failing[key]; failing {
		return redis.NewStringResult("", err)
	}
	if value, found := client.strings[key]; found {
		return redis.NewStringResult(value, nil)
	}
	return redis.NewStringResult("", redis.Nil)
}

// A publish time that cannot be read fails the load, which keeps the index
// before it and its publish time ageing; a key that is not there is a writer
// that publishes none.
func TestAPublishTimeThatCannotBeReadFailsTheLoad(t *testing.T) {
	const published = "bk_monitorv3.ce.cache.cmdb_published_at.host_topo"
	client := &markerClient{hashClient: &hashClient{hashes: map[string][]string{"bk_monitorv3.ce.cache.cmdb.host": {"501", hostUnderSet}}},
		failing: map[string]error{published: errors.New("i/o timeout")}}
	reader, err := NewReader(client, "bk_monitorv3.ce")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Load(context.Background(), time.Unix(1700000300, 0)); err == nil {
		t.Fatal("a load whose publish time could not be read succeeded")
	}
	client.failing = nil
	if index, err := reader.Load(context.Background(), time.Unix(1700000300, 0)); err != nil || !index.PublishedAt().IsZero() {
		t.Fatalf("a load with no publish time = %v, %v; want loaded, aged by its read", err, index.PublishedAt())
	}
}

// A writer clock ahead of this one is a publish just made: Health says the
// writer publishes, at age zero, never a negative age.
func TestHealthReadsAPublishTimeAheadAsJustPublished(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	builder := newIndexBuilder(now)
	builder.addFields([]string{"501", hostUnderSet})
	builder.index.publishedAt = now.Add(time.Minute)
	store := &Store{index: builder.index, now: func() time.Time { return now }, maxAge: 10 * time.Minute, publishedMaxAge: 15 * time.Minute}
	if health := store.Health(); !health.Published || health.PublishedAge != 0 || health.Degraded {
		t.Fatalf("health = %+v, want published at age zero", health)
	}
}

// The writer's publish time is read from its own key, before the hashes, as
// epoch seconds; the other writer's attempt time is read apart from it.
func TestTheIndexReadsTheWritersPublishTimeBeforeTheHashes(t *testing.T) {
	const published = "bk_monitorv3.ce.cache.cmdb_published_at.host_topo"
	const attempted = "bk_monitorv3.ce.cache.cmdb_last_refresh_all_time.host_topo"
	client := &markerClient{hashClient: &hashClient{hashes: map[string][]string{
		"bk_monitorv3.ce.cache.cmdb.host": {"501", hostUnderSet}}},
		strings: map[string]string{published: "1700000000", attempted: "1690000000"}}
	reader, err := NewReader(client, "bk_monitorv3.ce")
	if err != nil {
		t.Fatal(err)
	}
	index, err := reader.Load(context.Background(), time.Unix(1700000300, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !index.PublishedAt().Equal(time.Unix(1700000000, 0)) || !index.SourceRefreshedAt().Equal(time.Unix(1690000000, 0)) {
		t.Fatalf("published %v, attempted %v", index.PublishedAt(), index.SourceRefreshedAt())
	}
	position := func(read string) int {
		for at, done := range client.scans {
			if done == read {
				return at
			}
		}
		return -1
	}
	if get, scan := position("GET "+published), position("bk_monitorv3.ce.cache.cmdb.host"); get < 0 || scan < 0 || get > scan {
		t.Fatalf("reads in order %v: the publish time must come before the host hash", client.scans)
	}
	for _, value := range []string{"", "not-a-time", "-5", "0"} {
		if got := parseEpochSeconds(value); !got.IsZero() {
			t.Errorf("publish time %q read as %v, want none", value, got)
		}
	}
}

// staleStoreCase is one held index, aged by its read and by its writer.
type staleStoreCase struct {
	name      string
	readAgo   time.Duration
	published time.Duration // since the writer published; 0 for no publish time
	attempted time.Duration // since the other writer's attempt; 0 for none
	usable    bool
}

func TestEveryCallerReadsTheOneJudgementOnTheHostIndex(t *testing.T) {
	now := time.Unix(1700003600, 0).UTC()
	host := `{"bk_host_id":730001,"bk_host_innerip":"192.0.2.181","bk_cloud_id":0,"bk_biz_id":11,"model_id":"cw-Host","model_inst_id":"730001",` +
		`"bk_state":"运营中[需告警]","topo_link":{"module|91":[{"bk_obj_id":"module","bk_inst_id":91}]}}`
	for _, c := range []staleStoreCase{
		{name: "read just now, no publish time", usable: true},
		{name: "read past the read bound, no publish time", readAgo: 11 * time.Minute},
		// The writer stopped: alarmd reads it every minute and the facts are
		// still as old as its last publish.
		{name: "read just now, published past the publish bound", published: 16 * time.Minute},
		// One missed round and a slow build: not stale.
		{name: "read just now, published within the publish bound", published: 14 * time.Minute, usable: true},
		// The publish time covers a run of failed reads too.
		{name: "read eleven minutes ago, published twelve", readAgo: 11 * time.Minute, published: 12 * time.Minute, usable: true},
		// A writer clock ahead of this one reads as just published.
		{name: "published in the future", published: -2 * time.Minute, usable: true},
		// The other writer's attempt time is shown, not judged on.
		{name: "read just now, other writer's attempt two hours ago", attempted: 2 * time.Hour, usable: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			builder := newIndexBuilder(now.Add(-c.readAgo))
			builder.addFields([]string{"730001", host, "192.0.2.181|0", host})
			builder.addTopologyNodes([]string{"module|91"})
			if c.published != 0 {
				builder.index.publishedAt = now.Add(-c.published)
			}
			if c.attempted != 0 {
				builder.index.sourceRefreshedAt = now.Add(-c.attempted)
			}
			store := &Store{index: builder.index, now: func() time.Time { return now }, maxAge: 10 * time.Minute,
				publishedMaxAge: 15 * time.Minute}

			_, unusable := store.Usable()
			if (unusable == "") != c.usable {
				t.Fatalf("Usable = %q, want usable %v", unusable, c.usable)
			}
			if health := store.Health(); health.Degraded == c.usable || (!c.usable && health.DegradedReason != IndexStale) {
				t.Errorf("Health degraded %v %q", health.Degraded, health.DegradedReason)
			}
			if c.published > 0 {
				if health := store.Health(); health.PublishedAge != c.published {
					t.Errorf("Health published age %v, want %v", health.PublishedAge, c.published)
				}
			}

			// Admission's fullers.
			chain := admission.NewChain([]admission.Fuller{admission.IdentityFuller{}, NewHostTopologyFuller(store), NewServiceInstanceTopologyFuller(store)}, nil)
			facts := chain.Enrich(map[string]json.RawMessage{"bk_host_id": json.RawMessage(`"730001"`)})
			if facts.HostFactsUnavailable == c.usable || facts.HostResolved != c.usable {
				t.Errorf("fuller: unavailable %v resolved %v", facts.HostFactsUnavailable, facts.HostResolved)
			}
			// The target selectors.
			plan := &contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-Host", Rule: contract.TargetPlanRuleModelInstID,
				Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true}, StaticKeys: []string{},
				StaticMembers:     []contract.TargetPlanMemberV1{{ModelID: "cw-Host", ModelInstID: "730001"}},
				DynamicTopologies: []contract.TargetPlanTopologyV1{{BusinessID: "11", ObjectID: "module", InstanceID: "91"}}}
			resolution := NewTargetResolver(nil, store, store.now).Resolve(context.Background(), plan, time.Minute)
			for _, got := range []targetplan.SelectorResult{
				selector(resolution, targetplan.SelectorKindStatic, "cw-Host"),
				selector(resolution, targetplan.SelectorKindTopology, "11|module|91"),
			} {
				if c.usable && got.State != targetplan.SelectorOK || !c.usable && (got.State != targetplan.SelectorUnavailable || got.Reason != targetplan.ReasonStale) {
					t.Errorf("%s selector %s/%s", got.Kind, got.State, got.Reason)
				}
			}
			// No-data's host resolution and attribution.
			lookup := NewHostBusinessLookup(store)
			if lookup.HostIndexResolved() != c.usable {
				t.Errorf("HostIndexResolved = %v", lookup.HostIndexResolved())
			}
			if _, held := lookup.LookupHostBusiness("730001"); held != c.usable {
				t.Errorf("LookupHostBusiness held = %v", held)
			}
			// Attribution labels, and a held index places even past its
			// bound: every row here holds the host.
			if _, placed := lookup.PlaceHostBusiness(map[string]json.RawMessage{"bk_host_id": json.RawMessage(`"730001"`)}); !placed {
				t.Errorf("PlaceHostBusiness placed = %v, want the held index to place", placed)
			}
		})
	}
}
