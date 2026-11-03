// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/admission"
)

const identityLive = `{"bk_host_id":720002,"bk_host_innerip":"192.0.2.172","bk_cloud_id":0,"bk_biz_id":999,"bk_agent_id":"agent-live",` +
	`"bk_state":"运营中[需告警]","display_name":"l","topo_link":{"module|91":[{"bk_obj_id":"module","bk_inst_id":91}]}}`

const identityOther = `{"bk_host_id":720003,"bk_host_innerip":"192.0.2.173","bk_cloud_id":0,"bk_biz_id":999,` +
	`"bk_state":"运营中[需告警]","display_name":"o","topo_link":{"module|91":[{"bk_obj_id":"module","bk_inst_id":91}]}}`

// identityLoad is one load of the writer's cache: the hosts it holds, the
// agent hash (nil for one that could not be read), when the writer
// published it, and records refused beside them.
func identityLoad(hosts []string, agents map[string]string, published time.Time, refused int) func() (*Index, error) {
	return func() (*Index, error) {
		builder := newIndexBuilder(published)
		builder.addFields(hosts)
		index := builder.index
		if agents == nil {
			index.byAgent, index.agentsUnreadable = nil, true
		} else {
			index.byAgent = agents
		}
		index.publishedAt = published
		index.refused.Hosts += refused
		return index, nil
	}
}

type identityCase struct {
	t      *testing.T
	now    time.Time
	store  *Store
	loads  *scriptedLoads
	chain  *admission.Chain
	record map[string]json.RawMessage
}

func newIdentityCase(t *testing.T, record string) *identityCase {
	t.Helper()
	c := &identityCase{t: t, now: time.Unix(1_800_000_000, 0), loads: &scriptedLoads{}}
	store, err := NewStore(c.loads, StoreOptions{RefreshInterval: time.Minute, MaxAge: 10 * time.Minute,
		PublishedMaxAge: 15 * time.Minute, Now: func() time.Time { return c.now }})
	if err != nil {
		t.Fatal(err)
	}
	c.store, c.chain = store, instanceChain(t, store)
	c.record = jsonDims(t, record)
	return c
}

// refresh hands the store its next load.
func (c *identityCase) refresh(load func() (*Index, error)) {
	c.t.Helper()
	c.loads.loads = append(c.loads.loads, load)
	if err := c.store.Refresh(context.Background()); err != nil {
		c.t.Fatal(err)
	}
}

// overlay is what the record's alert identity reads now, and whether the
// facts the filters decide on were there.
func (c *identityCase) overlay() (string, bool) {
	c.t.Helper()
	facts := c.chain.Enrich(c.record)
	if facts.HostIdentityUnavailable {
		c.t.Fatalf("the record's identity was said unreadable with an identity index held")
	}
	encoded, _ := json.Marshal(facts.HostOverlay)
	return string(encoded), !facts.HostFactsUnavailable
}

// wantLiveOverlay is what an agent-named record's identity reads at host
// 720002; an id-named record carries its own bk_host_id, which is not
// written over (wantLiveOverlayByID).
const (
	wantLiveOverlay     = `{"bk_host_id":"720002","bk_target_cloud_id":"0","bk_target_ip":"192.0.2.172"}`
	wantLiveOverlayByID = `{"bk_target_cloud_id":"0","bk_target_ip":"192.0.2.172"}`
)

// A writer measured at one deployment published 16.5 minutes apart against
// a 15-minute bound. For the minute and a half between the bound and the
// next publish the held index was stale;
// the record's alert identity still reads the host CMDB placed it at, while
// the filters see the facts unavailable, as they did.
func TestAStaleIndexKeepsAHostPlacedRecordsIdentity(t *testing.T) {
	for _, by := range []struct{ name, record, want string }{
		{"by agent", `{"bk_agent_id":"agent-live","bk_target_ip":"198.51.100.9"}`, wantLiveOverlay},
		{"by id", `{"bk_host_id":"720002","bk_target_ip":"198.51.100.9"}`, wantLiveOverlayByID},
	} {
		t.Run(by.name, func(t *testing.T) {
			c := newIdentityCase(t, by.record)
			published := c.now
			c.refresh(identityLoad([]string{"720002", identityLive, "192.0.2.172|0", identityLive}, map[string]string{"agent-live": "720002"}, published, 0))
			fresh, decided := c.overlay()
			if fresh != by.want || !decided {
				t.Fatalf("fresh index: overlay %s (facts there %t), want %s", fresh, decided, by.want)
			}
			c.now = published.Add(15*time.Minute + 17*time.Second)
			if _, reason := c.store.Usable(); reason != IndexStale {
				t.Fatalf("the index at 15m17s is %q, want stale", reason)
			}
			stale, decided := c.overlay()
			if stale != fresh {
				t.Fatalf("stale index: overlay %s, want the same as fresh, %s", stale, fresh)
			}
			if decided {
				t.Fatal("stale index: the filters were handed facts as if the index were usable")
			}
		})
	}
}

// An empty load replacing a populated one leaves the identity where the
// populated one placed it.
func TestAnEmptyLoadKeepsAHostPlacedRecordsIdentity(t *testing.T) {
	c := newIdentityCase(t, `{"bk_agent_id":"agent-live","bk_target_ip":"198.51.100.9"}`)
	c.refresh(identityLoad([]string{"720002", identityLive}, map[string]string{"agent-live": "720002"}, c.now, 0))
	before, _ := c.overlay()
	c.refresh(identityLoad(nil, map[string]string{}, c.now, 0))
	if _, reason := c.store.Usable(); reason != IndexEmpty {
		t.Fatalf("the empty load is %q, want empty", reason)
	}
	if after, _ := c.overlay(); after != before || after != wantLiveOverlay {
		t.Fatalf("after an empty load: overlay %s, want %s", after, before)
	}
}

// A load whose agent hash could not be read leaves an agent-named record's
// identity where the last readable hash placed it, and is read again on the
// next refresh rather than kept on an unmoved publish time.
func TestAnUnreadableAgentHashKeepsTheRecordsIdentity(t *testing.T) {
	c := newIdentityCase(t, `{"bk_agent_id":"agent-live","bk_target_ip":"198.51.100.9"}`)
	c.refresh(identityLoad([]string{"720002", identityLive}, map[string]string{"agent-live": "720002"}, c.now, 0))
	before, _ := c.overlay()
	c.refresh(identityLoad([]string{"720002", identityLive}, nil, c.now, 0))
	after, decided := c.overlay()
	if after != before || after != wantLiveOverlay || decided {
		t.Fatalf("unreadable agent hash: overlay %s (facts there %t), want %s with the facts unavailable", after, decided, before)
	}
}

// A host refused in load after load keeps where the last load that placed it
// put it, by id and by agent; a clean load without it means the host is
// gone, and its record keeps its own address, as Python's would.
func TestARefusedHostKeepsItsIdentityUntilACleanLoadDropsIt(t *testing.T) {
	for _, by := range []struct{ name, record, want string }{
		{"by id", `{"bk_host_id":"720002","bk_target_ip":"198.51.100.9"}`, wantLiveOverlayByID},
		{"by agent", `{"bk_agent_id":"agent-live","bk_target_ip":"198.51.100.9"}`, wantLiveOverlay},
	} {
		t.Run(by.name, func(t *testing.T) {
			c := newIdentityCase(t, by.record)
			agents := map[string]string{"agent-live": "720002"}
			c.refresh(identityLoad([]string{"720002", identityLive, "720003", identityOther}, agents, c.now, 0))
			for load := 1; load <= 10; load++ {
				c.refresh(identityLoad([]string{"720003", identityOther}, agents, c.now, 1))
				if got, _ := c.overlay(); got != by.want {
					t.Fatalf("load %d refused the host: overlay %s, want %s", load, got, by.want)
				}
			}
			c.refresh(identityLoad([]string{"720003", identityOther}, agents, c.now, 0))
			if gone, _ := c.overlay(); gone != "null" {
				t.Fatalf("a clean load without the host: overlay %s, want none", gone)
			}
		})
	}
}

// Before the store's first load has returned, a record naming a true id or
// agent is held; once loads have returned with no host in them - fifty empty
// ones - it goes out under its own address, counted raw_no_index; once a
// load holds its host, it carries the host's identity. A record naming
// neither is never held.
func TestAHostPlacedRecordIsHeldOnlyUntilTheFirstLoadReturns(t *testing.T) {
	for _, record := range []string{`{"bk_host_id":"720002","bk_target_ip":"198.51.100.9"}`,
		`{"bk_agent_id":"agent-live","bk_target_ip":"198.51.100.9"}`} {
		c := newIdentityCase(t, record)
		if facts := c.chain.Enrich(c.record); !facts.HostIdentityUnavailable || facts.HostOverlay != nil {
			t.Fatalf("%s before the first load: held %t overlay %v, want held", record, facts.HostIdentityUnavailable, facts.HostOverlay)
		}
		for load := 0; load < 50; load++ {
			c.refresh(identityLoad(nil, map[string]string{}, c.now, 0))
		}
		facts := c.chain.Enrich(c.record)
		if facts.HostIdentityUnavailable || facts.HostOverlay != nil || facts.HostOverlaySource != admission.HostIdentityRawNoIndex {
			t.Fatalf("%s after fifty empty loads: held %t overlay %v source %q, want raw, named %s", record,
				facts.HostIdentityUnavailable, facts.HostOverlay, facts.HostOverlaySource, admission.HostIdentityRawNoIndex)
		}
		c.refresh(identityLoad([]string{"720002", identityLive}, map[string]string{"agent-live": "720002"}, c.now, 0))
		if facts := c.chain.Enrich(c.record); facts.HostOverlay == nil || facts.HostIdentityUnavailable {
			t.Fatalf("%s once a load holds the host: overlay %v, want it", record, facts.HostOverlay)
		}
	}
	plain := newIdentityCase(t, `{"bk_target_ip":"198.51.100.9"}`)
	if facts := plain.chain.Enrich(plain.record); facts.HostIdentityUnavailable || facts.HostOverlaySource != "" {
		t.Fatalf("a record naming no id or agent: held %t source %q, want neither", facts.HostIdentityUnavailable, facts.HostOverlaySource)
	}
}

// Where the identity came from is named: the index in force when it may be
// decided on (nothing), the stale index, or the last populated one.
func TestTheIdentityOverlaySourceIsNamed(t *testing.T) {
	c := newIdentityCase(t, `{"bk_agent_id":"agent-live","bk_target_ip":"198.51.100.9"}`)
	published := c.now
	c.refresh(identityLoad([]string{"720002", identityLive}, map[string]string{"agent-live": "720002"}, published, 0))
	if source := c.chain.Enrich(c.record).HostOverlaySource; source != "" {
		t.Fatalf("usable index: source %q, want none", source)
	}
	c.now = published.Add(16 * time.Minute)
	if source := c.chain.Enrich(c.record).HostOverlaySource; source != admission.HostOverlayFromStaleIndex {
		t.Fatalf("stale index: source %q, want %s", source, admission.HostOverlayFromStaleIndex)
	}
	c.now = published
	c.refresh(identityLoad(nil, map[string]string{}, published, 0))
	if source := c.chain.Enrich(c.record).HostOverlaySource; source != admission.HostOverlayFromLastPopulatedIndex {
		t.Fatalf("empty index: source %q, want %s", source, admission.HostOverlayFromLastPopulatedIndex)
	}
}

// refreshFailing hands the store a load that fails, as a Redis timeout does.
func (c *identityCase) refreshFailing() {
	c.t.Helper()
	c.loads.loads = append(c.loads.loads, func() (*Index, error) { return nil, errors.New("i/o timeout") })
	_ = c.store.Refresh(context.Background())
}

// A first load that fails does not end the hold: a tenant whose cache holds
// hosts but could not be read is held, not emitted raw; a read that later
// succeeds gives the record its identity. Reads that keep failing end the
// hold after five refresh intervals since the first try, and the record goes
// out under its own address, named raw_no_index.
func TestAFailedFirstLoadHoldsUntilAReadSucceedsOrTheBoundPasses(t *testing.T) {
	record := `{"bk_agent_id":"agent-live","bk_target_ip":"198.51.100.9"}`
	c := newIdentityCase(t, record)
	c.refreshFailing()
	for minute := 1; minute <= 4; minute++ {
		c.now = c.now.Add(time.Minute)
		c.refreshFailing()
		if facts := c.chain.Enrich(c.record); !facts.HostIdentityUnavailable {
			t.Fatalf("minute %d of failed reads: not held (source %q), want held", minute, facts.HostOverlaySource)
		}
	}
	c.refresh(identityLoad([]string{"720002", identityLive}, map[string]string{"agent-live": "720002"}, c.now, 0))
	if got, _ := c.overlay(); got != wantLiveOverlay {
		t.Fatalf("after a read succeeded: overlay %s, want %s", got, wantLiveOverlay)
	}

	failing := newIdentityCase(t, record)
	failing.refreshFailing()
	failing.now = failing.now.Add(5 * time.Minute)
	failing.refreshFailing()
	facts := failing.chain.Enrich(failing.record)
	if facts.HostIdentityUnavailable || facts.HostOverlay != nil || facts.HostOverlaySource != admission.HostIdentityRawNoIndex {
		t.Fatalf("five minutes of failed reads: held %t overlay %v source %q, want raw, named %s",
			facts.HostIdentityUnavailable, facts.HostOverlay, facts.HostOverlaySource, admission.HostIdentityRawNoIndex)
	}
}

// A load that refused most of CMDB's hosts keeps every one of them: the
// carry is bounded by the identity's own size, not by the few hosts the
// refusing load still held.
func TestAMassRefusalKeepsEveryRefusedHost(t *testing.T) {
	host := func(id int) string {
		return `{"bk_host_id":` + strconv.Itoa(id) + `,"bk_host_innerip":"192.0.2.` + strconv.Itoa(id%250) + `","bk_cloud_id":0,"bk_biz_id":999}`
	}
	var ten, two []string
	for id := 730001; id <= 730010; id++ {
		ten = append(ten, strconv.Itoa(id), host(id))
		if id > 730008 {
			two = append(two, strconv.Itoa(id), host(id))
		}
	}
	c := newIdentityCase(t, `{"bk_host_id":"730001"}`)
	c.refresh(identityLoad(ten, map[string]string{}, c.now, 0))
	c.refresh(identityLoad(two, map[string]string{}, c.now, 8))
	for id := 730001; id <= 730008; id++ {
		if _, found := c.store.IdentityHost(strconv.Itoa(id)); !found {
			t.Errorf("host %d, refused with seven others by a load holding two, lost its identity", id)
		}
	}
}
