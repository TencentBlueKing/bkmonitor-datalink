// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scopeclose

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/filterclose"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdmanage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openepisodes"
)

// closeRequest is one request the fake link received on its filtered close
// route, as the link's API documents it.
type closeRequest struct {
	Filter json.RawMessage `json:"filter"`
	DryRun bool            `json:"dry_run"`
}

// scopeLink is the link's filtered close route. Each request matches
// `active` alerts, all within the link's budget (no page: alert-close.md,
// Linkd c5e2443a3); a real close closes them. status, when set, answers every request with that status
// instead.
type scopeLink struct {
	mu     sync.Mutex
	active int
	status int
	// itemStatus, when set, answers each item of a real close with it:
	// nothing is closed.
	itemStatus int
	requests   []closeRequest
}

func (link *scopeLink) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	link.mu.Lock()
	defer link.mu.Unlock()
	if r.URL.Path != linkdmanage.RouteCloseFilter {
		http.NotFound(w, r)
		return
	}
	var request closeRequest
	_ = json.Unmarshal(body, &request)
	link.requests = append(link.requests, request)
	if link.status != 0 {
		http.Error(w, "refused", link.status)
		return
	}
	n := link.active
	items := make([]map[string]any, 0, n)
	status := http.StatusOK
	if !request.DryRun && link.itemStatus != 0 {
		status = link.itemStatus
	}
	for i := 0; i < n; i++ {
		items = append(items, map[string]any{"alert_id": fmt.Sprintf("alert-%d", i), "status": status})
	}
	if !request.DryRun && status == http.StatusOK {
		link.active -= n
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "has_more": false, "dry_run": request.DryRun, "closed": n})
}

func (link *scopeLink) set(active, status int) {
	link.mu.Lock()
	link.active, link.status = active, status
	link.mu.Unlock()
}

func (link *scopeLink) sent() (dry, real []closeRequest) {
	link.mu.Lock()
	defer link.mu.Unlock()
	for _, request := range link.requests {
		if request.DryRun {
			dry = append(dry, request)
		} else {
			real = append(real, request)
		}
	}
	return dry, real
}

// knownSet is the copy's word per fingerprint; a fingerprint not listed is
// judged not held.
type knownSet map[string]bool

func (set knownSet) KnowsOpen(string, string) bool { return true }

func (set knownSet) Known(_, _, fingerprint string) (bool, bool) {
	return set[fingerprint], true
}

// unjudged is a copy with no word on anything.
type unjudged struct{}

func (unjudged) KnowsOpen(string, string) bool             { return true }
func (unjudged) Known(string, string, string) (bool, bool) { return false, false }

const (
	testTenant   = "system"
	testSource   = "source-a"
	testStrategy = "42"
)

// newScopeHarness is the production closer (filterclose over the link's
// management client and a real Redis) behind a scope close bound to set.
func newScopeHarness(t *testing.T, set OpenSet) (*Closer, *scopeLink, *time.Time) {
	t.Helper()
	server := redistest.Start(t)
	if server == nil {
		t.Skip("no redis-server")
	}
	link := &scopeLink{}
	plane := httptest.NewServer(link)
	t.Cleanup(plane.Close)
	client, err := linkdmanage.New(linkdmanage.Options{BaseURL: plane.URL, Secret: "example-secret", Username: "alarmd", Client: plane.Client()})
	if err != nil {
		t.Fatal(err)
	}
	commands, err := filterclose.NewCommands(server.Client(), "alarmd-test")
	if err != nil {
		t.Fatal(err)
	}
	pending, err := filterclose.NewPending(server.Client(), "alarmd-test", filterclose.MaxPending)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	clock := func() time.Time { return now }
	record, err := openepisodes.NewStore(server.Client(), "alarmd-test")
	if err != nil {
		t.Fatal(err)
	}
	link2, err := filterclose.New(filterclose.Options{Link: client, Commands: commands, Pending: pending, Record: record, Now: clock})
	if err != nil {
		t.Fatal(err)
	}
	closer := New(Options{Now: clock})
	closer.Bind(set, link2, testSource)
	return closer, link, &now
}

// confirm observes the offer in two different Slots: a confirmed entry.
func confirm(closer *Closer, offer Offer) {
	offer.TenantID, offer.StrategyID, offer.StrategyRevision = testTenant, testStrategy, 3
	offer.Round = 600
	closer.Observe(offer)
	offer.Round = 660
	closer.Observe(offer)
}

// termsOf lists every term and terms clause of a filter as "field=value"
// texts, and every bool.filter group of terms as one joined text.
func termsOf(t *testing.T, raw json.RawMessage) (fields map[string]bool, groups []string) {
	t.Helper()
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatalf("filter %s: %v", raw, err)
	}
	fields = map[string]bool{}
	var walk func(node any)
	walk = func(node any) {
		switch value := node.(type) {
		case map[string]any:
			for key, child := range value {
				switch key {
				case "term":
					for field, v := range child.(map[string]any) {
						fields[fmt.Sprintf("%s=%v", field, v)] = true
					}
				case "terms":
					for field, v := range child.(map[string]any) {
						for _, one := range v.([]any) {
							fields[fmt.Sprintf("%s=%v", field, one)] = true
						}
					}
				case "filter":
					if list, ok := child.([]any); ok {
						var group []string
						for _, clause := range list {
							if term, ok := clause.(map[string]any)["term"].(map[string]any); ok {
								for field, v := range term {
									group = append(group, fmt.Sprintf("%s=%v", field, v))
								}
							}
						}
						groups = append(groups, strings.Join(group, ","))
					}
				}
				walk(child)
			}
		case []any:
			for _, child := range value {
				walk(child)
			}
		}
	}
	walk(root)
	return fields, groups
}

// A confirmed offer is closed by its old fingerprint, under the
// superseded-identity decision: the filter names the strategy's tenant, this
// source, the strategy and the fingerprint, and no dimension.
func TestAConfirmedOfferIsClosedByItsOldFingerprint(t *testing.T) {
	closer, link, _ := newScopeHarness(t, knownSet{"fp-old": true})
	link.set(1, 0)
	confirm(closer, Offer{Fingerprint: "fp-old"})
	closer.Step(context.Background())
	_, real := link.sent()
	if len(real) != 1 {
		t.Fatalf("closes %d, want 1", len(real))
	}
	fields, _ := termsOf(t, real[0].Filter)
	for _, want := range []string{"bk_tenant_id=" + testTenant, "event_source_id=" + testSource, "labels.strategy_id=" + testStrategy,
		"fingerprint=fp-old"} {
		if !fields[want] {
			t.Fatalf("close filter %s lacks %s", real[0].Filter, want)
		}
	}
	for field := range fields {
		if strings.HasPrefix(field, "dimensions.") {
			t.Fatalf("a fingerprint close names a dimension: %s", real[0].Filter)
		}
	}
	if stats := closer.Stats(); stats[OutcomeCloseSent] != 1 || stats[OutcomeSupersededCloseSent] != 1 {
		t.Fatalf("stats %v", stats)
	}
	// Decided: the Slots that keep offering it do not close it again.
	confirm(closer, Offer{Fingerprint: "fp-old"})
	closer.Step(context.Background())
	if _, real := link.sent(); len(real) != 1 || closer.Stats()[OutcomeCloseSent] != 1 {
		t.Fatalf("closes %d after the decision, stats %v", len(real), closer.Stats())
	}
}

// A link that is busy or fails holds the entry: it is counted send_failed
// and closed on a later step once the link answers. 501 is counted as
// filtered close unsupported, not closed.
func TestAFailedCloseIsHeldAndRetried(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			closer, link, _ := newScopeHarness(t, knownSet{"fp-1": true})
			link.set(1, status)
			confirm(closer, Offer{Fingerprint: "fp-1"})
			closer.Step(context.Background())
			if _, real := link.sent(); len(real) != 0 {
				t.Fatalf("closes %d while the link answers %d", len(real), status)
			}
			if stats := closer.Stats(); stats[OutcomeSendFailed] != 1 || stats[OutcomeCloseSent] != 0 {
				t.Fatalf("stats %v", stats)
			}
			link.set(1, 0)
			closer.Step(context.Background())
			if _, real := link.sent(); len(real) != 1 {
				t.Fatalf("the held entry was not closed after the link recovered: closes %d", len(real))
			}
			if stats := closer.Stats(); stats[OutcomeCloseSent] != 1 {
				t.Fatalf("stats %v", stats)
			}
		})
	}
	// Items the link certainly did not close (429 each): the batch is not
	// settled, the entry is kept and closed on the next step.
	closer, link, _ := newScopeHarness(t, knownSet{"fp-1": true})
	link.set(1, 0)
	link.itemStatus = http.StatusTooManyRequests
	confirm(closer, Offer{Fingerprint: "fp-1"})
	closer.Step(context.Background())
	if stats := closer.Stats(); stats[OutcomeSendFailed] != 1 || stats[OutcomeCloseSent] != 0 {
		t.Fatalf("stats %v, want the rederived batch kept", stats)
	}
	link.mu.Lock()
	link.itemStatus = 0
	link.mu.Unlock()
	closer.Step(context.Background())
	if stats := closer.Stats(); stats[OutcomeCloseSent] != 1 {
		t.Fatalf("stats %v, want the kept entry closed", stats)
	}

	closer, link, _ = newScopeHarness(t, knownSet{"fp-1": true})
	link.set(1, http.StatusNotImplemented)
	confirm(closer, Offer{Fingerprint: "fp-1"})
	closer.Step(context.Background())
	if stats := closer.Stats(); stats[OutcomeUnsupported] != 1 || stats[OutcomeCloseSent] != 0 {
		t.Fatalf("stats %v", stats)
	}
}

// A close the same command would be answered the same way - items
// refused (400 each: job_done_refused), the request refused (400:
// link_refused) - is counted close_refused and held: the steps within
// RefusedRetry send nothing, the first past it sends the close again. An
// offer under another revision is another decision and is not held.
func TestARefusedCloseWaitsBeforeItIsSentAgain(t *testing.T) {
	for _, tc := range []struct {
		name               string
		status, itemStatus int
	}{
		{"items refused", 0, http.StatusBadRequest},
		{"request refused", http.StatusBadRequest, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			closer, link, now := newScopeHarness(t, knownSet{"fp-1": true})
			link.set(1, tc.status)
			link.mu.Lock()
			link.itemStatus = tc.itemStatus
			link.mu.Unlock()
			sent := func() int {
				link.mu.Lock()
				defer link.mu.Unlock()
				return len(link.requests)
			}
			confirm(closer, Offer{Fingerprint: "fp-1"})
			closer.Step(context.Background())
			first := sent()
			if stats := closer.Stats(); first == 0 || stats[OutcomeRefused] != 1 || stats[OutcomeSendFailed] != 0 {
				t.Fatalf("%d requests, stats %v: want the close sent and counted close_refused", first, stats)
			}
			for _, d := range []time.Duration{30 * time.Second, RefusedRetry - 31*time.Second} {
				*now = now.Add(d)
				confirm(closer, Offer{Fingerprint: "fp-1"})
				closer.Step(context.Background())
				if n := sent(); n != first {
					t.Fatalf("%d requests within %s of the refusal, want none", n-first, RefusedRetry)
				}
			}
			*now = now.Add(time.Second)
			confirm(closer, Offer{Fingerprint: "fp-1"})
			closer.Step(context.Background())
			if n := sent(); n != 2*first || closer.Stats()[OutcomeRefused] != 2 {
				t.Fatalf("%d requests, stats %v at %s: want the close sent once more", n-first, closer.Stats(), RefusedRetry)
			}
			// Another revision: sent at once.
			for _, round := range []int64{720, 780} {
				closer.Observe(Offer{TenantID: testTenant, StrategyID: testStrategy, StrategyRevision: 4, Round: round, Fingerprint: "fp-1"})
			}
			closer.Step(context.Background())
			if n := sent(); n != 3*first {
				t.Fatalf("%d requests after an offer under another revision, want it sent", n-2*first)
			}
		})
	}
}

// Nothing is closed on the copy's silence or on a single Slot: an offer of
// a fingerprint the copy has no word on, or judges not open, is nothing
// this close holds and counts nothing; one observation waits for a second.
func TestNoCloseWithoutTheCopysWordAndASecondSlot(t *testing.T) {
	quiet := func(t *testing.T, closer *Closer) {
		t.Helper()
		for outcome, n := range closer.Stats() {
			if n != 0 {
				t.Fatalf("an offer the close does not hold counted %s %d", outcome, n)
			}
		}
	}
	closer, link, _ := newScopeHarness(t, unjudged{})
	link.set(1, 0)
	confirm(closer, Offer{Fingerprint: "fp-1"})
	closer.Step(context.Background())
	quiet(t, closer)
	if len(link.requests) != 0 {
		t.Fatalf("unjudged: requests %d", len(link.requests))
	}

	closer, link, _ = newScopeHarness(t, knownSet{"fp-1": false, "fp-2": true})
	link.set(1, 0)
	confirm(closer, Offer{Fingerprint: "fp-1"})
	quiet(t, closer)
	closer.Observe(Offer{TenantID: testTenant, StrategyID: testStrategy, StrategyRevision: 3, Round: 600, Fingerprint: "fp-2"})
	closer.Step(context.Background())
	if stats := closer.Stats(); stats[OutcomeUnconfirmed] != 1 || len(link.requests) != 0 {
		t.Fatalf("not held / one Slot: stats %v, requests %d", stats, len(link.requests))
	}
}

// The close is bounded to the alerts opened by the moment the entry was
// last observed rejected, the moment its decision was read; not the
// moment the step sends it.
func TestTheCloseIsBoundedToTheLastObservation(t *testing.T) {
	for _, offer := range []Offer{{Fingerprint: "fp-1"}, {Fingerprint: "fp-2"}} {
		closer, link, now := newScopeHarness(t, knownSet{offer.Fingerprint: true})
		link.set(1, 0)
		observed := *now
		confirm(closer, offer)
		*now = now.Add(time.Second)
		closer.Step(context.Background())
		dry, real := link.sent()
		if len(dry) == 0 || len(real) != 1 {
			t.Fatalf("dry runs %d, closes %d", len(dry), len(real))
		}
		want := `{"range":{"create_at":{"lte":"` + observed.UTC().Format(time.RFC3339Nano) + `"}}}`
		for _, request := range append(dry, real...) {
			if !strings.Contains(string(request.Filter), want) {
				t.Fatalf("filter %s does not carry %s", request.Filter, want)
			}
		}
	}
}
