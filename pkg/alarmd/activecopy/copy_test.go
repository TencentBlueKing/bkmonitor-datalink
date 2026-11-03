// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package activecopy

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdmanage"
)

const source = "built_in_bk"

func fp(n int) string {
	sum := md5.Sum([]byte(fmt.Sprintf("series-%d", n)))
	return hex.EncodeToString(sum[:])
}

// fakeSearch is the link's search over a set of active alerts, by the
// query's own clauses: it refuses a query without the read scope, and
// answers only the alerts of the query's source and fingerprints.
type fakeSearch struct {
	t       *testing.T
	active  map[string]linkdmanage.Alert
	queries []map[string]any
	tenants []string
	errs    []error
}

func newFakeSearch(t *testing.T) *fakeSearch {
	return &fakeSearch{t: t, active: map[string]linkdmanage.Alert{}}
}

func (f *fakeSearch) open(tenant, strategy, fingerprint string) {
	labels := map[string]json.RawMessage{"strategy_id": json.RawMessage(`"` + strategy + `"`)}
	f.active[fingerprint] = linkdmanage.Alert{AlertID: "a-" + fingerprint, TenantID: tenant, EventSourceID: source, Fingerprint: fingerprint,
		Status: "active", Labels: labels}
}

func (f *fakeSearch) Search(_ context.Context, tenant string, query json.RawMessage, after string, limit int) (linkdmanage.SearchPage, error) {
	var q map[string]any
	if err := json.Unmarshal(query, &q); err != nil {
		f.t.Fatalf("query not an object: %v", err)
	}
	f.queries = append(f.queries, q)
	f.tenants = append(f.tenants, tenant)
	if len(f.errs) > 0 {
		err := f.errs[0]
		f.errs = f.errs[1:]
		if err != nil {
			return linkdmanage.SearchPage{}, err
		}
	}
	filter := q["bool"].(map[string]any)["filter"].([]any)
	var sourceSeen, activeSeen bool
	wanted := map[string]bool{}
	for _, clause := range filter {
		c := clause.(map[string]any)
		if term, ok := c["term"].(map[string]any); ok {
			for field, value := range term {
				switch {
				case field == "event_source_id" && value == source:
					sourceSeen = true
				case field == "status" && value == "active":
					activeSeen = true
				default:
					f.t.Fatalf("unexpected term %s=%v", field, value)
				}
			}
		}
		if terms, ok := c["terms"].(map[string]any); ok {
			for field, values := range terms {
				if field != "fingerprint" {
					f.t.Fatalf("unexpected terms on %s", field)
				}
				if len(values.([]any)) > BatchSize {
					f.t.Fatalf("%d fingerprints in one request", len(values.([]any)))
				}
				for _, v := range values.([]any) {
					wanted[v.(string)] = true
				}
			}
		}
	}
	if !sourceSeen || !activeSeen || len(wanted) == 0 {
		f.t.Fatalf("read without its scope: %s", query)
	}
	page := linkdmanage.SearchPage{}
	for value := range wanted {
		if alert, ok := f.active[value]; ok && alert.TenantID == tenant {
			page.Items = append(page.Items, alert)
		}
	}
	return page, nil
}

type clock struct{ at time.Time }

func (c *clock) now() time.Time { return c.at }

func newCopy(t *testing.T, link Searcher, clk *clock) *Copy {
	t.Helper()
	c, err := New(Options{Link: link, EventSourceID: source, Now: clk.now})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestGateHoldsOneRoundThenAnswersFromTheSearch(t *testing.T) {
	link := newFakeSearch(t)
	link.open("t", "1", fp(1))
	clk := &clock{at: time.Unix(1_800_000_000, 0)}
	c := newCopy(t, link, clk)
	if c.Contains("t", "1", fp(1)) || c.Contains("t", "1", fp(2)) {
		t.Fatal("a fingerprint the copy has no word on passed")
	}
	if len(link.queries) != 0 {
		t.Fatal("the gate asked the link inside the lookup")
	}
	c.Pass(context.Background())
	if len(link.queries) != 1 {
		t.Fatalf("requests = %d, want one batch", len(link.queries))
	}
	if !c.Contains("t", "1", fp(1)) {
		t.Fatal("an active alert found by the search did not pass")
	}
	if c.Contains("t", "1", fp(2)) {
		t.Fatal("a miss passed")
	}
	stats := c.Stats()
	if stats.Answers[AnswerUnknown] != 2 || stats.Answers[AnswerSearchHit] != 1 || stats.Outcomes[OutcomeHit] != 1 || stats.Outcomes[OutcomeMiss] != 1 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestReadsBatchAcrossStrategiesWithoutAStrategyTerm(t *testing.T) {
	link := newFakeSearch(t)
	clk := &clock{at: time.Unix(1_800_000_000, 0)}
	c := newCopy(t, link, clk)
	for n := 0; n < 1100; n++ {
		c.Contains("t", fmt.Sprint(n%7), fp(n))
	}
	c.Contains("u", "1", fp(5000))
	c.Pass(context.Background())
	if len(link.queries) != 4 {
		t.Fatalf("requests = %d, want ceil(1100/500) for one tenant and one for the other", len(link.queries))
	}
	for i, q := range link.queries {
		body, _ := json.Marshal(q)
		if json.Valid(body) && contains(string(body), "strategy_id") {
			t.Fatalf("request %d names a strategy: %s", i, body)
		}
	}
	if link.tenants[3] != "u" {
		t.Fatalf("tenants = %v", link.tenants)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestBusyOrFailedReadHoldsAndCounts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		outcome string
	}{
		{"429", &linkdmanage.StatusError{Route: "search", Status: 429}, OutcomeLinkBusy},
		{"500", &linkdmanage.StatusError{Route: "search", Status: 500}, OutcomeReadFailed},
		{"transport", errors.New("connection reset"), OutcomeReadFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			link := newFakeSearch(t)
			link.open("t", "1", fp(1))
			clk := &clock{at: time.Unix(1_800_000_000, 0)}
			c := newCopy(t, link, clk)
			for n := 0; n < 600; n++ {
				c.Contains("t", "1", fp(n+1))
			}
			link.errs = []error{tc.err}
			c.Pass(context.Background())
			if len(link.queries) != 1 {
				t.Fatalf("requests = %d: the pass went on after a failed read", len(link.queries))
			}
			if c.Contains("t", "1", fp(1)) {
				t.Fatal("held through a failed read, the gate passed")
			}
			if held, judged := c.Holds("t", "1", fp(2)); held || judged {
				t.Fatal("a failed read judged")
			}
			stats := c.Stats()
			if stats.Outcomes[tc.outcome] != 1 || stats.Queued != 600 {
				t.Fatalf("stats = %+v", stats)
			}
			c.Pass(context.Background())
			if !c.Contains("t", "1", fp(1)) {
				t.Fatal("the next pass did not answer what the failed one kept")
			}
		})
	}
}

// A link that answers it cannot search is asked again after a bounded,
// doubling wait, never given up on; the gate holds meanwhile under its own
// answer, and the copy is warned once an episode.
func TestUnsupportedLinkIsRetriedOnABoundedBackoff(t *testing.T) {
	link := newFakeSearch(t)
	clk := &clock{at: time.Unix(1_800_000_000, 0)}
	warned := 0
	c, err := New(Options{Link: link, EventSourceID: source, Now: clk.now, Warn: func(error) { warned++ }})
	if err != nil {
		t.Fatal(err)
	}
	link.open("t", "1", fp(1))
	unsupported := &linkdmanage.StatusError{Route: "search", Status: 404}
	link.errs = []error{unsupported, unsupported, unsupported, unsupported, unsupported, unsupported}
	c.Contains("t", "1", fp(1))
	first := clk.at
	c.Pass(context.Background())
	if len(link.queries) != 1 || warned != 1 || !c.Stats().Unsupported {
		t.Fatalf("queries %d, warned %d, stats %+v", len(link.queries), warned, c.Stats())
	}
	// Held, under its own answer, and not asked before the wait ends.
	if c.Contains("t", "1", fp(1)) || c.Stats().Answers[AnswerLinkUnsupported] != 1 {
		t.Fatalf("gate while unsupported: %+v", c.Stats().Answers)
	}
	clk.at = clk.at.Add(UnsupportedRetryMin - time.Second)
	c.Pass(context.Background())
	if len(link.queries) != 1 {
		t.Fatalf("asked again before the wait: %d", len(link.queries))
	}
	// The wait doubles while the link keeps answering so, up to its bound.
	waits := []time.Duration{}
	last := first
	for asked, tick := 1, 0; asked < 6; tick++ {
		if tick > 3600 {
			t.Fatalf("not asked again within an hour: waits %v", waits)
		}
		clk.at = clk.at.Add(time.Second)
		c.Pass(context.Background())
		if len(link.queries) > asked {
			asked = len(link.queries)
			waits = append(waits, clk.at.Sub(last))
			last = clk.at
		}
	}
	want := []time.Duration{UnsupportedRetryMin, 2 * UnsupportedRetryMin, 4 * UnsupportedRetryMin, 8 * UnsupportedRetryMin, UnsupportedRetryMax}
	for i, wait := range waits {
		if wait != want[i] {
			t.Fatalf("waits %v, want %v", waits, want)
		}
	}
	if warned != 1 || c.Stats().Outcomes[OutcomeLinkUnsupported] != 6 {
		t.Fatalf("warned %d, stats %+v", warned, c.Stats())
	}
	// The link serves its search again: the next retry answers, the episode
	// ends, and a later 501 is a new episode, warned again.
	clk.at = clk.at.Add(UnsupportedRetryMax)
	c.Pass(context.Background())
	if !c.Contains("t", "1", fp(1)) || c.Stats().Unsupported {
		t.Fatalf("after the link came back: %+v", c.Stats())
	}
	link.errs = []error{&linkdmanage.StatusError{Route: "search", Status: 501}}
	c.Holds("t", "1", fp(2))
	c.Pass(context.Background())
	if warned != 2 || !c.Stats().Unsupported {
		t.Fatalf("second episode: warned %d, stats %+v", warned, c.Stats())
	}
	clk.at = clk.at.Add(UnsupportedRetryMin)
	c.Pass(context.Background())
	if c.Stats().Unsupported {
		t.Fatal("the second episode started from the long wait")
	}
}

// A fingerprint the read cannot name is taken out of its batch and
// counted; the rest of the batch is asked in the same pass.
func TestANonNativeFingerprintDoesNotHoldUpItsBatch(t *testing.T) {
	link := newFakeSearch(t)
	clk := &clock{at: time.Unix(1_800_000_000, 0)}
	c := newCopy(t, link, clk)
	link.open("t", "1", fp(1))
	c.Contains("t", "1", fp(1))
	c.Contains("t", "1", "not-a-native-fingerprint")
	c.Contains("t", "1", "0123456789ABCDEF0123456789ABCDEF")
	c.Pass(context.Background())
	stats := c.Stats()
	if len(link.queries) != 1 || !c.Contains("t", "1", fp(1)) || stats.Outcomes[OutcomeFingerprintRefused] != 2 ||
		stats.Outcomes[OutcomeReadFailed] != 0 || stats.Queued != 0 {
		t.Fatalf("queries %d, stats %+v", len(link.queries), stats)
	}
	wanted := link.queries[0]["bool"].(map[string]any)["filter"].([]any)[2].(map[string]any)["terms"].(map[string]any)["fingerprint"].([]any)
	if len(wanted) != 1 || wanted[0] != fp(1) {
		t.Fatalf("asked %v", wanted)
	}
	// A batch of nothing but refused fingerprints sends nothing.
	c.Contains("t", "1", "still-not-native")
	c.Pass(context.Background())
	if len(link.queries) != 1 || c.Stats().Outcomes[OutcomeFingerprintRefused] != 3 {
		t.Fatalf("queries %d, stats %+v", len(link.queries), c.Stats())
	}
}

func TestAMissYoungerThanTheMinimumAgeIsNotJudged(t *testing.T) {
	link := newFakeSearch(t)
	clk := &clock{at: time.Unix(1_800_000_000, 0)}
	c := newCopy(t, link, clk)
	c.Holds("t", "1", fp(1))
	c.Pass(context.Background())
	if held, judged := c.Holds("t", "1", fp(1)); held || judged {
		t.Fatal("one miss judged the alert not open")
	}
	// Not asked again before a search could see what the first could not.
	clk.at = clk.at.Add(MissMinAge / 2)
	c.Holds("t", "1", fp(1))
	c.Pass(context.Background())
	if len(link.queries) != 1 {
		t.Fatalf("requests = %d", len(link.queries))
	}
	// The alert becomes visible: the second search finds it.
	link.open("t", "1", fp(1))
	clk.at = clk.at.Add(MissMinAge)
	c.Holds("t", "1", fp(1))
	c.Pass(context.Background())
	if held, judged := c.Holds("t", "1", fp(1)); !held || !judged {
		t.Fatal("the alert the first search could not see was not found")
	}
	// A run of two misses MissMinAge apart is the link's word.
	c.Holds("t", "1", fp(2))
	c.Pass(context.Background())
	clk.at = clk.at.Add(MissMinAge)
	c.Holds("t", "1", fp(2))
	c.Pass(context.Background())
	if held, judged := c.Holds("t", "1", fp(2)); held || !judged {
		t.Fatal("two misses MissMinAge apart were not judged not open")
	}
}

func TestOwnSendPassesWithoutARequestAndARecoveryForgetsIt(t *testing.T) {
	link := newFakeSearch(t)
	clk := &clock{at: time.Unix(1_800_000_000, 0)}
	c := newCopy(t, link, clk)
	ref := &contract.StrategySnapshotRef{TenantID: "t", StrategyID: 1, Revision: 1}
	abnormal := contract.TriggerEventV1{TenantID: "t", DedupeMD5: fp(1), StrategyRef: ref, EventKind: contract.TriggerEventAbnormal}
	abnormal.PlanRef.StrategyID = "1"
	c.Acknowledged([]contract.TriggerEventV1{abnormal})
	if !c.Contains("t", "1", fp(1)) || !c.KnowsOpen("t", "1") {
		t.Fatal("this process's own ABNORMAL did not pass")
	}
	if held, judged := c.Known("t", "1", fp(1)); !held || !judged {
		t.Fatal("Known does not carry the own send")
	}
	recovery := abnormal
	recovery.EventKind = contract.TriggerEventRecovery
	c.Acknowledged([]contract.TriggerEventV1{recovery})
	if c.Contains("t", "1", fp(1)) || c.KnowsOpen("t", "1") {
		t.Fatal("after its RECOVERY the fingerprint still passed")
	}
	if held, judged := c.Known("t", "1", fp(2)); held || judged {
		t.Fatal("Known judged a fingerprint it has no word on")
	}
	c.Pass(context.Background())
	if len(link.queries) != 1 {
		t.Fatalf("requests = %d: Known queued", len(link.queries))
	}
}

func TestAPassSendsAtMostItsBudget(t *testing.T) {
	link := newFakeSearch(t)
	clk := &clock{at: time.Unix(1_800_000_000, 0)}
	c := newCopy(t, link, clk)
	for n := 0; n < (RequestsPerPass+3)*BatchSize; n++ {
		c.Contains("t", "1", fp(n))
	}
	c.Pass(context.Background())
	if len(link.queries) != RequestsPerPass {
		t.Fatalf("requests = %d, want %d", len(link.queries), RequestsPerPass)
	}
	if got := c.Stats().Queued; got != 3*BatchSize {
		t.Fatalf("queued = %d", got)
	}
}

func TestAnItemOfAnotherStrategyOrSourceIsNotAHit(t *testing.T) {
	link := newFakeSearch(t)
	link.open("t", "2", fp(1))
	clk := &clock{at: time.Unix(1_800_000_000, 0)}
	c := newCopy(t, link, clk)
	c.Contains("t", "1", fp(1))
	c.Pass(context.Background())
	if c.Contains("t", "1", fp(1)) || c.Stats().Outcomes[OutcomeForeignItem] != 1 {
		t.Fatalf("stats = %+v", c.Stats())
	}
}

func TestAStrategyTrackedAgainAfterAGapStartsFromNothing(t *testing.T) {
	link := newFakeSearch(t)
	link.open("t", "1", fp(1))
	clk := &clock{at: time.Unix(1_800_000_000, 0)}
	c := newCopy(t, link, clk)
	key := StrategyKey{TenantID: "t", StrategyID: "1"}
	c.Track(key)
	c.Contains("t", "1", fp(1))
	c.Pass(context.Background())
	clk.at = clk.at.Add(untrackedGap / 2)
	c.Track(key)
	if !c.Contains("t", "1", fp(1)) {
		t.Fatal("dropped inside the gap")
	}
	clk.at = clk.at.Add(untrackedGap + time.Second)
	c.Track(key)
	if c.Contains("t", "1", fp(1)) {
		t.Fatal("kept across a gap another owner may have changed")
	}
}

func TestA502CutsTheBatch(t *testing.T) {
	link := newFakeSearch(t)
	clk := &clock{at: time.Unix(1_800_000_000, 0)}
	c := newCopy(t, link, clk)
	for n := 0; n < BatchSize; n++ {
		c.Contains("t", "1", fp(n))
	}
	link.errs = []error{&linkdmanage.StatusError{Route: "search", Status: 502}}
	c.Pass(context.Background())
	c.Pass(context.Background())
	if len(link.queries) != 3 {
		t.Fatalf("requests = %d, want the failed one then two halves", len(link.queries))
	}
}

func TestReadQueryRefusesOutsideItsScope(t *testing.T) {
	if _, err := ReadQuery("", []string{fp(1)}); err == nil {
		t.Fatal("no source accepted")
	}
	if _, err := ReadQuery(source, nil); err == nil {
		t.Fatal("no fingerprint accepted")
	}
	if _, err := ReadQuery(source, []string{"not-a-fingerprint"}); err == nil {
		t.Fatal("a foreign value accepted")
	}
	many := make([]string, BatchSize+1)
	for i := range many {
		many[i] = fp(i)
	}
	if _, err := ReadQuery(source, many); err == nil {
		t.Fatal("more than a batch accepted")
	}
}

// Acknowledged hands the episode record the envelopes that change it: an
// ABNORMAL the copy held no own send of - a search hit is not one - and
// every RECOVERY; the repeat of an own ABNORMAL changes nothing, and a
// compatibility-protocol envelope never reaches the link.
func TestAcknowledgedReturnsWhatChangesTheEpisodeRecord(t *testing.T) {
	link := newFakeSearch(t)
	clk := &clock{at: time.Unix(1_800_000_000, 0)}
	c := newCopy(t, link, clk)
	ref := &contract.StrategySnapshotRef{TenantID: "t", StrategyID: 1, Revision: 1}
	event := func(kind string, n int) contract.TriggerEventV1 {
		e := contract.TriggerEventV1{TenantID: "t", DedupeMD5: fp(n), StrategyRef: ref, EventKind: kind}
		e.PlanRef.StrategyID = "1"
		return e
	}
	legacy := event(contract.TriggerEventAbnormal, 9)
	legacy.LegacyOutput = &contract.LegacyEventContext{}
	if changed := c.Acknowledged([]contract.TriggerEventV1{event(contract.TriggerEventAbnormal, 1), legacy}); len(changed) != 1 ||
		changed[0].DedupeMD5 != fp(1) {
		t.Fatalf("first ABNORMAL: %v", changed)
	}
	if changed := c.Acknowledged([]contract.TriggerEventV1{event(contract.TriggerEventAbnormal, 1)}); len(changed) != 0 {
		t.Fatalf("a repeated own ABNORMAL changed %v", changed)
	}
	// fp(2) known as a search hit: its first own ABNORMAL is a change.
	link.open("t", "1", fp(2))
	c.Holds("t", "1", fp(2))
	c.Pass(context.Background())
	if held, judged := c.Holds("t", "1", fp(2)); !held || !judged {
		t.Fatal("the hit was not taken")
	}
	if changed := c.Acknowledged([]contract.TriggerEventV1{event(contract.TriggerEventAbnormal, 2)}); len(changed) != 1 {
		t.Fatalf("the first own ABNORMAL of a hit changed %v", changed)
	}
	if changed := c.Acknowledged([]contract.TriggerEventV1{event(contract.TriggerEventRecovery, 1), event(contract.TriggerEventRecovery, 3)}); len(changed) != 2 {
		t.Fatalf("RECOVERY changed %v", changed)
	}
	if changed := c.Acknowledged([]contract.TriggerEventV1{event(contract.TriggerEventAbnormal, 1)}); len(changed) != 1 {
		t.Fatalf("an ABNORMAL after the RECOVERY changed %v", changed)
	}
}

// A question first asked while the link is unsupported is queued all the
// same: the retry after the wait asks it, and the answer clears the
// unsupported state. Were it not queued, the retry would ask nothing and
// the copy would stay down.
func TestALookupWhileUnsupportedIsQueuedForTheRetry(t *testing.T) {
	link := newFakeSearch(t)
	clk := &clock{at: time.Unix(1_800_000_000, 0)}
	c, err := New(Options{Link: link, EventSourceID: source, Now: clk.now})
	if err != nil {
		t.Fatal(err)
	}
	link.open("t", "1", fp(2))
	link.errs = []error{&linkdmanage.StatusError{Route: "search", Status: 501}}
	c.Contains("t", "1", fp(1))
	c.Pass(context.Background())
	if !c.Stats().Unsupported {
		t.Fatalf("stats %+v", c.Stats())
	}
	if c.Contains("t", "1", fp(2)) {
		t.Fatal("answered while unsupported")
	}
	clk.at = clk.at.Add(UnsupportedRetryMin)
	c.Pass(context.Background())
	if c.Stats().Unsupported || !c.Contains("t", "1", fp(2)) {
		t.Fatalf("after the retry: stats %+v, queries %d", c.Stats(), len(link.queries))
	}
}

// A link that refuses the credential (401, 403) is counted link_refused,
// not read_failed: the fingerprints stay queued and held, and it is told
// once a run, again only after a search is answered. A 500 stays
// read_failed and tells nothing.
func TestARefusedCredentialIsItsOwnOutcome(t *testing.T) {
	link := newFakeSearch(t)
	clk := &clock{at: time.Unix(1_800_000_000, 0)}
	told := 0
	c, err := New(Options{Link: link, EventSourceID: source, Now: clk.now, Refused: func(error) { told++ }})
	if err != nil {
		t.Fatal(err)
	}
	link.open("t", "1", fp(1))
	link.errs = []error{&linkdmanage.StatusError{Route: "search", Status: 401}, &linkdmanage.StatusError{Route: "search", Status: 403},
		nil, &linkdmanage.StatusError{Route: "search", Status: 401}, &linkdmanage.StatusError{Route: "search", Status: 500}}
	c.Contains("t", "1", fp(1))
	for range 2 {
		c.Pass(context.Background())
	}
	if c.Stats().Outcomes[OutcomeLinkRefused] != 2 || c.Stats().Outcomes[OutcomeReadFailed] != 0 || told != 1 {
		t.Fatalf("after 401 and 403: told %d, stats %+v", told, c.Stats().Outcomes)
	}
	if c.Contains("t", "1", fp(1)) || c.Stats().Queued != 1 {
		t.Fatalf("a refused fingerprint was not held and queued: %+v", c.Stats())
	}
	c.Pass(context.Background())
	if !c.Contains("t", "1", fp(1)) {
		t.Fatalf("the answered search did not answer the fingerprint: %+v", c.Stats())
	}
	c.Contains("t", "1", fp(2))
	c.Pass(context.Background())
	if told != 2 || c.Stats().Outcomes[OutcomeLinkRefused] != 3 {
		t.Fatalf("a refusal after an answered search: told %d, stats %+v", told, c.Stats().Outcomes)
	}
	c.Pass(context.Background())
	if c.Stats().Outcomes[OutcomeReadFailed] != 1 || c.Stats().Outcomes[OutcomeLinkRefused] != 3 || told != 2 {
		t.Fatalf("a 500: told %d, stats %+v", told, c.Stats().Outcomes)
	}
}
