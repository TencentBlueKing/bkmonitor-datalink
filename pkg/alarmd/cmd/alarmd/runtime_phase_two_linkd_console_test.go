// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/absentalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
)

var consoleTestNow = time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)

func consoleTestBinding() openalerts.TargetBinding {
	return openalerts.TargetBinding{EventSourceID: "source", HookName: "active", KeyPrefix: "test:active",
		Address: "192.0.2.10:6379", Database: 3, Sources: []string{"source"}}
}

// consoleTestServer is a Console that lists this deployment's target and
// whose browse answers with the handler given; every other path is a 404.
func consoleTestServer(t *testing.T, browse http.HandlerFunc) *openalerts.HTTPReconciler {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/local-api/strategy-index/targets":
			_ = json.NewEncoder(w).Encode([]openalerts.TargetBinding{consoleTestBinding()})
			return
		case "/local-api/strategy-index/browse":
			browse(w, r)
			return
		}
		// The event source definition is the test's to answer, like browse.
		if strings.HasPrefix(r.URL.Path, "/local-api/event-sources/") {
			browse(w, r)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	console, err := openalerts.NewHTTPReconciler(openalerts.HTTPReconcilerOptions{BaseURL: server.URL, Client: server.Client(),
		Username: "user", Password: "secret", MaxResponseBytes: 1 << 20,
		Index: openalerts.IndexLocation{KeyPrefix: "test:active", Address: "192.0.2.10:6379", Database: 3},
		Now:   func() time.Time { return consoleTestNow }})
	if err != nil {
		t.Fatal(err)
	}
	return console
}

// browsePage answers one final roster page whose health says the link last
// discovered successfully at lastSuccess (nil for never) with the error given.
func browsePage(lastSuccess *time.Time, linkError string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		health := map[string]any{"lastSuccess": nil, "lastAttempt": nil, "error": nil, "pendingCount": 4}
		if lastSuccess != nil {
			health["lastSuccess"] = lastSuccess.Format(time.RFC3339Nano)
		}
		if linkError != "" {
			health["error"] = linkError
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"target": consoleTestBinding(), "nextCursor": nil, "phase": "sets",
			"health": health, "rows": []any{}})
	}
}

func readRoster(console *openalerts.HTTPReconciler) {
	_, _ = console.Roster(context.Background(), "")
}

// A deployment without a Console carries the reading from the configuration
// alone, before anything has run.
func TestADeploymentWithoutAConsoleSaysSoFromTheStart(t *testing.T) {
	var cfg config.Config
	entry := linkdConsoleEndpoint(cfg)
	if entry.Role != fleet.EndpointLinkdConsole || entry.Configured || entry.Console == nil ||
		entry.Console.State != fleet.LinkdConsoleNotConfigured || len(entry.Console.Calls) != len(openalerts.ConsoleOps) {
		t.Fatalf("an unconfigured Console reads as %+v (facts %+v)", entry, entry.Console)
	}
	cfg.PhaseTwo.Linkd.ConsoleURL = "http://192.0.2.20:8080"
	if configured := linkdConsoleEndpoint(cfg); !configured.Configured || configured.Address != "http://192.0.2.20:8080" ||
		configured.Kind != "http" || configured.Console != nil {
		t.Fatalf("a configured Console reads as %+v", configured)
	}
}

// The readings a configured Console can give, each its own word, and the
// link's own word decided by the same function and bound the close's
// link_unhealthy refusal reads -- checked against the close itself on both
// sides of the bound.
func TestAConsoleReadsAsNotCalledUnreadableUnhealthyOrReachable(t *testing.T) {
	if facts := linkdConsoleFacts(consoleTestServer(t, http.NotFound), consoleTestNow); facts.State != fleet.LinkdConsoleNotCalled {
		t.Fatalf("a Console never called reads as %+v", facts)
	}
	refused := consoleTestServer(t, http.NotFound)
	readRoster(refused)
	if facts := linkdConsoleFacts(refused, consoleTestNow); facts.State != fleet.LinkdConsoleUnreadable || facts.Reason != openalerts.ConsoleOpRoster {
		t.Fatalf("a refused roster reads as %+v", facts)
	}
	bound := absentCloseMaxLinkHealthAge
	atBound, pastBound := consoleTestNow.Add(-bound), consoleTestNow.Add(-bound-time.Second)
	cases := []struct {
		name        string
		lastSuccess *time.Time
		linkError   string
		state       string
		reason      string
	}{
		{"last success exactly at the bound", &atBound, "", fleet.LinkdConsoleReachable, ""},
		{"last success a second past the bound", &pastBound, "", fleet.LinkdConsoleLinkUnhealthy, absentalerts.LinkDiscoveryStale},
		{"latest discovery failed", &atBound, "discovery_failed", fleet.LinkdConsoleLinkUnhealthy, absentalerts.LinkDiscoveryFailing},
		{"no discovery ever succeeded", nil, "", fleet.LinkdConsoleLinkUnhealthy, absentalerts.LinkDiscoveryNeverSucceeded},
	}
	for _, c := range cases {
		console := consoleTestServer(t, browsePage(c.lastSuccess, c.linkError))
		readRoster(console)
		facts := linkdConsoleFacts(console, consoleTestNow)
		if facts.State != c.state || facts.Reason != c.reason {
			t.Errorf("%s: state %q reason %q, want %q %q", c.name, facts.State, facts.Reason, c.state, c.reason)
		}
		if facts.LinkPending == nil || *facts.LinkPending != 4 || facts.MaxLinkHealthAgeSeconds != int(bound/time.Second) {
			t.Errorf("%s: the link's reading is not beside its bound: %+v", c.name, facts)
		}
		round := absentalerts.Round{LinkRead: true, LinkError: c.linkError, Now: consoleTestNow}
		if c.lastSuccess != nil {
			round.LinkLastSuccess = *c.lastSuccess
		}
		_, _, refusal := absentalerts.Candidates(round, absentalerts.Bounds{MaxLinkHealthAge: bound})
		if (refusal == absentalerts.RefusalLinkUnhealthy) != (facts.State == fleet.LinkdConsoleLinkUnhealthy) {
			t.Errorf("%s: the page says %q and the close's round says %q", c.name, facts.State, refusal)
		}
	}
}

// A Console that answered once and is refused now reads as unreadable, not
// as whatever the earlier answer said about the link -- and that holds when
// the two calls fall inside one tick of the clock.
func TestALaterFailureOutranksAnEarlierReadingOfTheLink(t *testing.T) {
	stale := consoleTestNow.Add(-time.Hour)
	failing := false
	good := browsePage(&stale, "")
	console := consoleTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if failing {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		good(w, r)
	})
	readRoster(console)
	if facts := linkdConsoleFacts(console, consoleTestNow); facts.State != fleet.LinkdConsoleLinkUnhealthy {
		t.Fatalf("before the failure: %+v", facts)
	}
	failing = true
	readRoster(console)
	facts := linkdConsoleFacts(console, consoleTestNow)
	if facts.State != fleet.LinkdConsoleUnreadable || facts.Reason != openalerts.ConsoleOpRoster ||
		facts.Calls[0].Calls != 2 || facts.Calls[0].Failures != 1 {
		t.Fatalf("after the failure: %+v", facts)
	}
}

// The metric reads the same decision as the entry.
func TestTheConsoleMetricReadsTheSameStateAsTheEntry(t *testing.T) {
	clock := func() time.Time { return consoleTestNow }
	if reading := linkdConsoleReading(nil, clock)(); reading.State != fleet.LinkdConsoleNotConfigured ||
		len(reading.Calls) != len(openalerts.ConsoleOps) {
		t.Fatalf("an unconfigured Console's metric reading: %+v", reading)
	}
	refused := consoleTestServer(t, http.NotFound)
	readRoster(refused)
	if reading := linkdConsoleReading(refused, clock)(); reading.State != fleet.LinkdConsoleUnreadable ||
		reading.Calls[openalerts.ConsoleOpRoster].Failures != 1 {
		t.Fatalf("a refused roster's metric reading: %+v", reading)
	}
}

// The source facts carry how many Plans publish the standard raw event: the
// count the deployment's need for the Console is decided from.
func TestTheSourceFactsCountThePlansThatGoToTheLink(t *testing.T) {
	composition := &controlplane.CatalogComposition{PlansTotal: 5,
		PlansByWireFormat: map[string]int{contract.WireFormatStandardRawEvent: 3, contract.WireFormatPythonCompatible: 2}}
	facts := sourceFactsOf(phaseTwoControlRefreshResult{Composition: composition}, consoleTestNow)
	if !facts.PlansKnown || facts.StandardPlans != 3 {
		t.Fatalf("source facts = %+v, want 3 standard plans", facts)
	}
}

// The entry carries where the link writes -- the target this replica
// resolved and the startup discovery -- so a refusal that names two
// locations can be read without the logs.
func TestTheConsoleEntryCarriesTheResolvedTargetAndTheDiscovery(t *testing.T) {
	console := consoleTestServer(t, browsePage(nil, ""))
	readRoster(console)
	discovery := &fleet.LinkdDiscoveryFacts{Outcome: fleet.LinkdDiscoveryFailed, Attempts: 3, Error: "alarmd openalerts: Console HTTP status 401"}
	endpoints := func() []fleet.Endpoint { return []fleet.Endpoint{{Role: fleet.EndpointLinkdConsole, Configured: true}} }
	location := &linkdLocationSwitch{discovery: *discovery, replaced: make(chan struct{})}
	entry := withLinkdConsole(endpoints, console, location, func() time.Time { return consoleTestNow })()[0]
	if entry.Console == nil || entry.Console.Discovery == nil || *entry.Console.Discovery != *discovery || entry.Console.Target == nil ||
		*entry.Console.Target != (fleet.LinkdTargetFacts{EventSourceID: "source", HookName: "active", Address: "192.0.2.10:6379", Database: 3, KeyPrefix: "test:active"}) ||
		entry.Console.TargetAgeSeconds == nil || *entry.Console.TargetAgeSeconds != 0 {
		t.Fatalf("console facts = %+v", entry.Console)
	}
	if fresh := linkdConsoleFacts(consoleTestServer(t, http.NotFound), consoleTestNow); fresh.Target != nil {
		t.Fatalf("a Console never resolved carries a target: %+v", fresh.Target)
	}
}

// keyingLink is an alert link that also says how it keys this deployment's
// alerts, and counts the asking.
type keyingLink struct {
	absentTestLink
	asked int
	err   error
}

func (link *keyingLink) EventSource(context.Context) (openalerts.EventSourceKeying, error) {
	link.asked++
	return openalerts.EventSourceKeying{}, link.err
}

// Each roster walk asks the link how it keys this deployment's alerts,
// once, and a failure to answer is the Console record's to show: the walk
// reads the roster as before.
func TestEachRosterWalkAsksTheLinkHowItKeysOurAlerts(t *testing.T) {
	link := &keyingLink{absentTestLink: absentTestLink{pages: []openalerts.RosterPage{{}}}, err: errors.New("unreachable")}
	loop := &absentStrategyClose{link: link}
	round := &absentalerts.Round{}
	loop.readRoster(context.Background(), round)
	if link.asked != 1 || !round.LinkRead {
		t.Fatalf("asked %d times, roster read %v; want one ask per walk and the walk unaffected by its failure", link.asked, round.LinkRead)
	}
}

// The keying reaches the endpoint entry once read, with its age; before it
// is read there is none, not an empty one.
func TestTheLinksKeyingReachesTheConsoleFacts(t *testing.T) {
	at := time.Date(2026, 9, 24, 5, 0, 0, 0, time.UTC)
	if linkdEventSourceFacts(openalerts.ConsoleRecord{}, at) != nil {
		t.Fatal("an unread keying was published")
	}
	facts := linkdEventSourceFacts(openalerts.ConsoleRecord{EventSourceReadAt: at.Add(-30 * time.Second),
		EventSource: openalerts.EventSourceKeying{EventSourceID: "alarmd", FingerprintMode: "field", FingerprintField: "source_alert_id", Revision: 3, Published: 3,
			InEffect: true, KeyedByAlertID: true}}, at)
	if facts == nil || facts.EventSourceID != "alarmd" || facts.FingerprintMode != "field" || facts.FingerprintField != "source_alert_id" ||
		facts.Published != 3 || facts.ReadAgeSeconds != 30 || !facts.InEffect || !facts.KeyedByAlertID {
		t.Fatalf("facts = %+v", facts)
	}
}

// Read through the Console, the keying is on the facts the endpoint entry
// publishes.
func TestTheConsoleFactsCarryTheKeyingOnceRead(t *testing.T) {
	console := consoleTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/local-api/event-sources/"+consoleTestBinding().EventSourceID {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": consoleTestBinding().EventSourceID, "revision": 2, "published": 2,
			"spec": map[string]any{"fingerprint_mode": "field", "fingerprint_field": "source_alert_id"}})
	})
	if facts := linkdConsoleFacts(console, consoleTestNow); facts.EventSource != nil {
		t.Fatalf("keying published before it was read: %+v", facts.EventSource)
	}
	if _, err := console.EventSource(context.Background()); err != nil {
		t.Fatal(err)
	}
	facts := linkdConsoleFacts(console, consoleTestNow)
	if facts.EventSource == nil || facts.EventSource.FingerprintMode != "field" || facts.EventSource.FingerprintField != "source_alert_id" {
		t.Fatalf("keying on the facts = %+v", facts.EventSource)
	}
}
