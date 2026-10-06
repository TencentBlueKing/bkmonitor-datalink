// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package openalerts

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// fakeSource answers Read from a script the test sets between refreshes and
// records what it was asked to read.
type fakeSource struct {
	publication Publication
	err         error
	asked       [][]StrategyKey
}

func (source *fakeSource) Read(_ context.Context, keys []StrategyKey) (Publication, error) {
	source.asked = append(source.asked, append([]StrategyKey{}, keys...))
	return source.publication, source.err
}

type clock struct{ at time.Time }

func (c *clock) now() time.Time          { return c.at }
func (c *clock) advance(d time.Duration) { c.at = c.at.Add(d) }

var (
	tenant = "default"
	keyA   = StrategyKey{TenantID: tenant, StrategyID: "1001"}
	keyB   = StrategyKey{TenantID: tenant, StrategyID: "1002"}
)

func fresh(c *clock, cycle time.Duration) *Heartbeat {
	return &Heartbeat{PublishedAt: c.at, Cycle: cycle, FingerprintVersion: FingerprintVersion}
}

func newCache(t *testing.T, source *fakeSource, c *clock, policy UnavailablePolicy) *Cache {
	t.Helper()
	cache, err := New(Options{Source: source, Now: c.now, Policy: policy})
	if err != nil {
		t.Fatal(err)
	}
	return cache
}

func abnormal(key StrategyKey, fingerprint string) contract.TriggerEventV1 {
	return contract.TriggerEventV1{
		EventKind: contract.TriggerEventAbnormal, TenantID: key.TenantID, DedupeMD5: fingerprint,
		StrategyRef: &contract.StrategySnapshotRef{TenantID: key.TenantID, Revision: 1},
		PlanRef:     contract.RuntimePlanRefV1{StrategyID: key.StrategyID},
	}
}

func recovery(key StrategyKey, fingerprint string) contract.TriggerEventV1 {
	event := abnormal(key, fingerprint)
	event.EventKind = contract.TriggerEventRecovery
	return event
}

func wantLookups(t *testing.T, cache *Cache, want map[Answer]uint64) {
	t.Helper()
	got := cache.Stats().Lookups
	for _, answer := range Answers {
		if got[answer] != want[answer] {
			t.Fatalf("lookups = %v, want %v", got, want)
		}
	}
}

// Before any publication has been read the copy is in never_loaded, not in
// self-maintained: a publisher that is not deployed reads as absent, not as
// lost, and fleet health does not degrade on it. It still answers, from what
// this process itself sent.
func TestNeverLoadedAnswersFromOwnSendsAndIsNotStale(t *testing.T) {
	c := &clock{at: time.Unix(1_700_000_000, 0)}
	cache := newCache(t, &fakeSource{}, c, PolicySelfMaintain)
	if got := cache.Stats().Mode; got != ModeNeverLoaded {
		t.Fatalf("mode = %s, want never_loaded", got)
	}
	if cache.Contains(tenant, keyA.StrategyID, "f1") {
		t.Fatal("nothing was sent, nothing should be open")
	}
	cache.Acknowledged([]contract.TriggerEventV1{abnormal(keyA, "f1")})
	if !cache.Contains(tenant, keyA.StrategyID, "f1") {
		t.Fatal("an acknowledged ABNORMAL opens the fingerprint in the copy")
	}
	cache.Acknowledged([]contract.TriggerEventV1{recovery(keyA, "f1")})
	if cache.Contains(tenant, keyA.StrategyID, "f1") {
		t.Fatal("an acknowledged RECOVERY closes it again")
	}
	wantLookups(t, cache, map[Answer]uint64{AnswerSelfMaintained: 3})
	if cache.StaleBeyondBound() {
		t.Fatal("a copy that never loaded is not stale; the publisher may not exist")
	}
}

// An authoritative publication answers membership; a strategy it does not
// carry a key for is "no open alerts" because the publisher rewrites every
// key with open alerts each cycle and TTLs the rest; and a fingerprint this
// process sent within the publisher's lag is open even though the
// publication does not carry it yet.
func TestAuthoritativePublicationAnswersMembership(t *testing.T) {
	c := &clock{at: time.Unix(1_700_000_000, 0)}
	source := &fakeSource{}
	cache := newCache(t, source, c, PolicySelfMaintain)
	cache.Track(keyA, keyB)
	source.publication = Publication{Heartbeat: fresh(c, time.Minute), Sets: map[StrategyKey][]string{keyA: {"f1"}}}
	cache.Refresh(context.Background())
	if len(source.asked) != 1 || len(source.asked[0]) != 2 {
		t.Fatalf("the refresh read %v, want both tracked strategies", source.asked)
	}
	stats := cache.Stats()
	if stats.Mode != ModeAuthoritative || !stats.Available || stats.Loaded != 2 || stats.Members != 1 {
		t.Fatalf("stats = %+v, want authoritative with 2 loaded and 1 member", stats)
	}
	if !cache.Contains(tenant, keyA.StrategyID, "f1") {
		t.Fatal("a published member is open")
	}
	if cache.Contains(tenant, keyA.StrategyID, "f2") {
		t.Fatal("a fingerprint the publication does not carry is closed")
	}
	if cache.Contains(tenant, keyB.StrategyID, "f3") {
		t.Fatal("a strategy with no key under a fresh heartbeat has no open alerts")
	}
	cache.Acknowledged([]contract.TriggerEventV1{abnormal(keyB, "f3")})
	if !cache.Contains(tenant, keyB.StrategyID, "f3") {
		t.Fatal("an ABNORMAL sent since the last read is open before the publisher catches up")
	}
	wantLookups(t, cache, map[Answer]uint64{AnswerMember: 1, AnswerAbsent: 2, AnswerRecentlySent: 1})
	// Past the publisher's lag the publication is the word: the sent
	// fingerprint is dropped on the next authoritative read.
	c.advance(LocalRetentionCycles*time.Minute + time.Second)
	source.publication.Heartbeat = fresh(c, time.Minute)
	cache.Refresh(context.Background())
	if cache.Stats().Added != 0 || cache.Contains(tenant, keyB.StrategyID, "f3") {
		t.Fatal("a send older than the publisher's lag is not kept over the publication")
	}
}

// A strategy first asked about after the last read is unknown until the
// next one, and says so: the unavailable policy answers that one cycle.
func TestAStrategyAskedAfterTheReadIsNotYetLoaded(t *testing.T) {
	c := &clock{at: time.Unix(1_700_000_000, 0)}
	source := &fakeSource{publication: Publication{Heartbeat: &Heartbeat{PublishedAt: time.Unix(1_700_000_000, 0), Cycle: time.Minute, FingerprintVersion: FingerprintVersion}}}
	cache := newCache(t, source, c, PolicySelfMaintain)
	cache.Track(keyA)
	cache.Refresh(context.Background())
	if cache.Contains(tenant, keyB.StrategyID, "f1") {
		t.Fatal("an unloaded strategy answers from own sends, and nothing was sent")
	}
	wantLookups(t, cache, map[Answer]uint64{AnswerNotYetLoaded: 1, AnswerSelfMaintained: 1})
	source.publication.Heartbeat = fresh(c, time.Minute)
	cache.Refresh(context.Background())
	if len(source.asked[1]) != 2 {
		t.Fatalf("the lookup did not track its strategy for the next read: %v", source.asked[1])
	}
	cache.Contains(tenant, keyB.StrategyID, "f1")
	wantLookups(t, cache, map[Answer]uint64{AnswerNotYetLoaded: 1, AnswerSelfMaintained: 1, AnswerAbsent: 1})
}

// Every way a read fails to yield an authoritative publication is named,
// and none of them is read as an empty publication: after each, the copy
// answers from the last publication plus its own sends.
func TestEveryUnavailableReasonIsNamedAndKeepsTheLastPublication(t *testing.T) {
	for _, arm := range []struct {
		name   string
		script func(c *clock, source *fakeSource)
		want   UnavailableReason
	}{
		{name: "read error", script: func(_ *clock, s *fakeSource) { s.err = errors.New("boom") }, want: UnavailableReadError},
		{name: "heartbeat missing", script: func(_ *clock, s *fakeSource) { s.publication.Heartbeat = nil }, want: UnavailableHeartbeatMissing},
		{name: "heartbeat unreadable", script: func(_ *clock, s *fakeSource) {
			s.publication.Heartbeat = nil
			s.publication.HeartbeatErr = &HeartbeatError{Field: HeartbeatCycleSeconds}
		}, want: UnavailableHeartbeatUnreadable},
		{name: "heartbeat stale", script: func(c *clock, s *fakeSource) {
			c.advance(StalenessCycles*time.Minute + time.Second)
		}, want: UnavailableHeartbeatStale},
		{name: "fingerprint version", script: func(_ *clock, s *fakeSource) {
			s.publication.Heartbeat.FingerprintVersion = "some-other-algorithm"
		}, want: UnavailableFingerprintVersion},
	} {
		t.Run(arm.name, func(t *testing.T) {
			c := &clock{at: time.Unix(1_700_000_000, 0)}
			source := &fakeSource{}
			cache := newCache(t, source, c, PolicySelfMaintain)
			cache.Track(keyA)
			source.publication = Publication{Heartbeat: fresh(c, time.Minute), Sets: map[StrategyKey][]string{keyA: {"f1"}}}
			cache.Refresh(context.Background())
			if cache.Stats().Mode != ModeAuthoritative {
				t.Fatal("the fixture did not load an authoritative publication first")
			}
			// Sets absent in the failing read must not be read as emptied.
			source.publication.Sets = nil
			arm.script(c, source)
			cache.Refresh(context.Background())
			stats := cache.Stats()
			if stats.Mode != ModeSelfMaintained || stats.Available || stats.UnavailableReason != arm.want || stats.Unavailable[arm.want] != 1 {
				t.Fatalf("stats = %+v, want self_maintained for %s", stats, arm.want)
			}
			if !cache.Contains(tenant, keyA.StrategyID, "f1") {
				t.Fatal("the last publication's member is still open")
			}
			cache.Acknowledged([]contract.TriggerEventV1{recovery(keyA, "f1"), abnormal(keyA, "f2")})
			if cache.Contains(tenant, keyA.StrategyID, "f1") || !cache.Contains(tenant, keyA.StrategyID, "f2") {
				t.Fatal("own sends after the last read move the copy: f1 closed, f2 opened")
			}
			wantLookups(t, cache, map[Answer]uint64{AnswerSelfMaintained: 3})
		})
	}
}

// The staleness bound is expressed in the publisher's cycles, not as a
// constant of this process: past it, a copy that had a publication and lost
// it is stale, which is what fleet health degrades on.
func TestStaleBeyondBoundFollowsThePublisherCycle(t *testing.T) {
	c := &clock{at: time.Unix(1_700_000_000, 0)}
	source := &fakeSource{}
	cache := newCache(t, source, c, PolicySelfMaintain)
	cache.Track(keyA)
	source.publication = Publication{Heartbeat: fresh(c, 2*time.Minute)}
	cache.Refresh(context.Background())
	source.publication.Heartbeat = nil
	c.advance(StalenessCycles*2*time.Minute - time.Second)
	cache.Refresh(context.Background())
	if cache.StaleBeyondBound() {
		t.Fatal("inside the bound is not stale")
	}
	c.advance(2 * time.Second)
	if !cache.StaleBeyondBound() {
		t.Fatal("past StalenessCycles publisher cycles without a publication is stale")
	}
	// A publication coming back clears it.
	source.publication.Heartbeat = fresh(c, 2*time.Minute)
	cache.Refresh(context.Background())
	if cache.StaleBeyondBound() || cache.Stats().Mode != ModeAuthoritative {
		t.Fatal("an authoritative read ends the stale state")
	}
}

// The other policy, written down and tested so that switching is one word:
// while unavailable every lookup passes, and says it did.
func TestPassThroughPolicyLetsEveryRecoveryGoWhileUnavailable(t *testing.T) {
	c := &clock{at: time.Unix(1_700_000_000, 0)}
	cache := newCache(t, &fakeSource{}, c, PolicyPassThrough)
	cache.Refresh(context.Background())
	if !cache.Contains(tenant, keyA.StrategyID, "never-sent") {
		t.Fatal("pass-through answers open for everything while unavailable")
	}
	wantLookups(t, cache, map[Answer]uint64{AnswerPassedThrough: 1})
}

// Only envelopes the consumer will see move the copy.
func TestAcknowledgedIgnoresWhatTheConsumerNeverSees(t *testing.T) {
	c := &clock{at: time.Unix(1_700_000_000, 0)}
	cache := newCache(t, &fakeSource{}, c, PolicySelfMaintain)
	legacy := abnormal(keyA, "legacy")
	legacy.LegacyOutput = &contract.LegacyEventContext{}
	noFingerprint := abnormal(keyA, "")
	noRevision := abnormal(keyA, "unfrozen")
	noRevision.StrategyRef = nil
	cache.Acknowledged([]contract.TriggerEventV1{legacy, noFingerprint, noRevision})
	if stats := cache.Stats(); stats.Added != 0 {
		t.Fatalf("added = %d, want 0", stats.Added)
	}
	cache.Acknowledged(nil)
}

// Own sends are bounded; past the bound the oldest go first and are counted,
// because in self-maintained mode each eviction is an alert whose recovery
// now waits for the publication.
func TestOwnSendsAreBoundedOldestFirst(t *testing.T) {
	c := &clock{at: time.Unix(1_700_000_000, 0)}
	cache, err := New(Options{Source: &fakeSource{}, Now: c.now, MaxLocalEntries: 3})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		cache.Acknowledged([]contract.TriggerEventV1{abnormal(keyA, "f"+strconv.Itoa(i))})
		c.advance(time.Second)
	}
	stats := cache.Stats()
	if stats.Added != 3 || stats.Evictions != 2 {
		t.Fatalf("added = %d evictions = %d, want 3 and 2", stats.Added, stats.Evictions)
	}
	if cache.Contains(tenant, keyA.StrategyID, "f0") || !cache.Contains(tenant, keyA.StrategyID, "f4") {
		t.Fatal("the oldest sends were evicted, the newest kept")
	}
}

// A strategy nobody has asked about within the tracking window leaves the
// read set, so a worker that lost a Query Group stops reading its strategy.
func TestTrackingForgetsStrategiesNobodyAsksAbout(t *testing.T) {
	c := &clock{at: time.Unix(1_700_000_000, 0)}
	source := &fakeSource{publication: Publication{Heartbeat: &Heartbeat{PublishedAt: time.Unix(1_700_000_000, 0), Cycle: time.Minute, FingerprintVersion: FingerprintVersion}}}
	cache, err := New(Options{Source: source, Now: c.now, TrackingWindow: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	cache.Track(keyA, keyB)
	cache.Refresh(context.Background())
	c.advance(10*time.Minute + time.Second)
	cache.Contains(tenant, keyA.StrategyID, "f1")
	source.publication.Heartbeat = fresh(c, time.Minute)
	cache.Refresh(context.Background())
	if got := source.asked[1]; len(got) != 1 || got[0] != keyA {
		t.Fatalf("read set = %v, want only the strategy asked about recently", got)
	}
}

func TestOptionsAreValidated(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("a source is required")
	}
	if _, err := New(Options{Source: &fakeSource{}, Policy: "whatever"}); err == nil {
		t.Fatal("an unknown policy is refused, not defaulted")
	}
}

func TestHeartbeatParsingRefusesDefaults(t *testing.T) {
	good := map[string]string{HeartbeatPublishedAt: "1700000000", HeartbeatCycleSeconds: "60", HeartbeatFingerprintVersion: FingerprintVersion}
	heartbeat, err := ParseHeartbeat(good)
	if err != nil || heartbeat.Cycle != time.Minute || heartbeat.PublishedAt.Unix() != 1_700_000_000 {
		t.Fatalf("ParseHeartbeat(good) = %+v, %v", heartbeat, err)
	}
	for _, field := range []string{HeartbeatPublishedAt, HeartbeatCycleSeconds, HeartbeatFingerprintVersion} {
		bad := map[string]string{}
		for k, v := range good {
			bad[k] = v
		}
		delete(bad, field)
		if _, err := ParseHeartbeat(bad); err == nil {
			t.Fatalf("a heartbeat without %s parsed", field)
		}
	}
	if _, err := ParseHeartbeat(map[string]string{HeartbeatPublishedAt: "1700000000", HeartbeatCycleSeconds: "0", HeartbeatFingerprintVersion: FingerprintVersion}); err == nil {
		t.Fatal("a zero cycle parsed")
	}
}

func TestSetKeyShape(t *testing.T) {
	if got := SetKey(keyA); got != "alarmd:open_alerts:default:1001" {
		t.Fatalf("SetKey = %q", got)
	}
}
