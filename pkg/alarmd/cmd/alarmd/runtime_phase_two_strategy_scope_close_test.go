package main

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/absentalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/filterclose"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/targetplan"
)

var scopeTestPlan = &contract.TargetPlanV1{SchemaVersion: 1, Rule: contract.TargetPlanRuleIPCloud,
	Identity: contract.TargetPlanIdentityV1{Dimensions: []string{contract.IPCloudIPDimension, contract.IPCloudCloudDimension}, Address: true}}

type scopeTestTargets struct{}

func (scopeTestTargets) StrategyTarget(_, strategyID string) (controlplane.StrategyTarget, bool, bool) {
	if strategyID != "51" {
		return controlplane.StrategyTarget{}, true, false
	}
	return controlplane.StrategyTarget{Revision: 7, Plan: scopeTestPlan, Interval: time.Minute, Agree: true}, true, true
}

// scopeTestResolver answers the strategy's members, Complete and fresh,
// unless told otherwise.
type scopeTestResolver struct {
	members []string
	state   targetplan.ResolutionState
	stale   time.Duration
	asked   int
}

func (resolver *scopeTestResolver) ResolveFor(_ context.Context, _ string, plan *contract.TargetPlanV1, _ time.Duration) *targetplan.Resolution {
	resolver.asked++
	resolution := &targetplan.Resolution{Static: map[string]struct{}{}, State: resolver.state, StaleAge: resolver.stale}
	for _, member := range resolver.members {
		resolution.Static[member] = struct{}{}
	}
	return resolution
}

type scopeFixture struct {
	loop     *strategyScopeClose
	resolver *scopeTestResolver
	link     *fakeLinkd
	recorded recordedLinkd
	snapshot map[absentalerts.Key]struct{}
}

// newScopeFixture is strategy 51, whose target is IP and cloud area, on the
// control leader, closed through the production filter route and the
// production episode record against the fake link.
func newScopeFixture(t *testing.T, members ...string) *scopeFixture {
	t.Helper()
	fixture := &scopeFixture{resolver: &scopeTestResolver{members: members, state: targetplan.ResolutionComplete}, link: newFakeLinkd(t),
		snapshot: map[absentalerts.Key]struct{}{{TenantID: "system", StrategyID: "51"}: {}, {TenantID: "system", StrategyID: "52"}: {}}}
	fixture.recorded = newRecordedLinkd(t, fixture.link)
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	bundle := &phaseTwoWorkerBundle{dependencies: phaseTwoWorkerBundleDependencies{
		Now: func() time.Time { return now }, Observer: observability.NopObserver{}}}
	bundle.controlLeader = true
	fixture.loop = newStrategyScopeClose(bundle, scopeTestTargets{}, fixture.resolver, fixture.recorded.route)
	return fixture
}

func (fixture *scopeFixture) alert(id, ip string) {
	dimensions := map[string]string{"device": "sda"}
	if ip != "" {
		dimensions[contract.IPCloudIPDimension], dimensions[contract.IPCloudCloudDimension] = ip, "0"
	}
	fixture.link.add(&fakeLinkdAlert{id: id, labels: map[string]string{"strategy_id": "51", "strategy_version": "7"}, dimensions: dimensions})
}

// opened acknowledges an ABNORMAL of series n on ip through the worker port.
func (fixture *scopeFixture) opened(t *testing.T, n int, ip string) {
	t.Helper()
	event := recordedAbnormal("51", 7, n)
	event.TargetDimensions = scopeTestPlan.Identity.Dimensions
	event.RecordRef.Dimensions = map[string]json.RawMessage{contract.IPCloudIPDimension: json.RawMessage(`"` + ip + `"`),
		contract.IPCloudCloudDimension: json.RawMessage(`0`)}
	fixture.recorded.acknowledge(t, event)
}

// confirmedStep runs the two rounds a scope closes on: the first reads it,
// the second reads the same and sends.
func (fixture *scopeFixture) confirmedStep(t *testing.T) {
	t.Helper()
	sent := fixture.closes()
	unconfirmed := fixture.loop.Stats()[scopeCloseUnconfirmed]
	fixture.loop.step(context.Background(), fixture.snapshot)
	if fixture.closes() != sent || fixture.loop.Stats()[scopeCloseUnconfirmed] != unconfirmed+1 {
		t.Fatalf("a scope read once sent %d requests, stats %v", fixture.closes()-sent, fixture.loop.Stats())
	}
	fixture.loop.step(context.Background(), fixture.snapshot)
}

func (fixture *scopeFixture) closes() int {
	return len(fixture.link.requests("/api/v1/alerts/close"))
}

// On the leader's start the scope close sends once per strategy, on the
// second round that reads the same scope: the alert
// of a removed target closes, the current target's and an alert without
// the target's dimensions stay. An unchanged scope sends nothing; a
// changed one sends again, and the alert without the dimensions still
// stays.
func TestTheScopeCloseSendsOnStartAndOnEachChange(t *testing.T) {
	fixture := newScopeFixture(t, "192.0.2.1|0", "192.0.2.2|0")
	fixture.alert("removed", "192.0.2.9")
	fixture.alert("current", "192.0.2.1")
	fixture.alert("leaving", "192.0.2.2")
	fixture.alert("aggregated", "")
	fixture.opened(t, 1, "192.0.2.9")
	fixture.opened(t, 2, "192.0.2.1")
	fixture.opened(t, 3, "192.0.2.2")
	fixture.confirmedStep(t)
	if got := fixture.link.closed(); len(got) != 1 || got["removed"] != 1 {
		t.Fatalf("closed %v, want removed only", got)
	}
	sent := fixture.closes()
	fixture.loop.step(context.Background(), fixture.snapshot)
	if fixture.closes() != sent || fixture.loop.Stats()[scopeCloseUnchanged] != 1 {
		t.Fatalf("an unchanged scope sent %d requests, stats %v", fixture.closes()-sent, fixture.loop.Stats())
	}
	fixture.resolver.members = []string{"192.0.2.1|0"}
	fixture.confirmedStep(t)
	if got := fixture.link.closed(); len(got) != 2 || got["leaving"] != 1 || got["current"] != 0 || got["aggregated"] != 0 {
		t.Fatalf("closed %v after the scope changed", got)
	}
	if stats := fixture.loop.Stats(); stats[scopeCloseSent] != 2 || stats[scopeCloseSettled] != 2 {
		t.Fatalf("stats %v", stats)
	}
}

// A scope resolved from a snapshot kept past its refresh closes nothing and
// is not a read of the scope: however many rounds read it, the first fresh
// read is still unconfirmed and the second closes. A member leaving under a
// stale snapshot closes only once fresh reads confirm it.
func TestAStaleResolutionClosesNothingUntilFreshReadsConfirm(t *testing.T) {
	fixture := newScopeFixture(t, "192.0.2.1|0", "192.0.2.2|0")
	fixture.alert("removed", "192.0.2.9")
	fixture.alert("current", "192.0.2.1")
	fixture.alert("leaving", "192.0.2.2")
	fixture.opened(t, 1, "192.0.2.9")
	fixture.opened(t, 2, "192.0.2.1")
	fixture.opened(t, 3, "192.0.2.2")
	fixture.resolver.stale = 3 * time.Minute
	for i := 0; i < 3; i++ {
		fixture.loop.step(context.Background(), fixture.snapshot)
	}
	if stats := fixture.loop.Stats(); fixture.closes() != 0 || stats[scopeCloseResolutionStale] != 3 ||
		stats[scopeCloseUnconfirmed] != 0 {
		t.Fatalf("a stale scope sent %d requests, stats %v", fixture.closes(), stats)
	}
	fixture.resolver.stale = 0
	fixture.confirmedStep(t)
	if got := fixture.link.closed(); len(got) != 1 || got["removed"] != 1 {
		t.Fatalf("closed %v, want removed only", got)
	}
	fixture.resolver.members = []string{"192.0.2.1|0"}
	fixture.resolver.stale = time.Minute
	sent := fixture.closes()
	fixture.loop.step(context.Background(), fixture.snapshot)
	fixture.loop.step(context.Background(), fixture.snapshot)
	if fixture.closes() != sent || fixture.loop.Stats()[scopeCloseResolutionStale] != 5 {
		t.Fatalf("a stale changed scope sent %d requests, stats %v", fixture.closes()-sent, fixture.loop.Stats())
	}
	fixture.resolver.stale = 0
	fixture.confirmedStep(t)
	if got := fixture.link.closed(); len(got) != 2 || got["leaving"] != 1 || got["current"] != 0 {
		t.Fatalf("closed %v after fresh reads of the changed scope", got)
	}
}

// A dry run past the record closes nothing: the record holds only current
// targets' episodes (it allows 20), the link 21 alerts of removed ones.
func TestTheScopeCloseIsRefusedPastTheRecord(t *testing.T) {
	fixture := newScopeFixture(t, "192.0.2.1|0")
	fixture.opened(t, 1, "192.0.2.1")
	for i := 0; i < 21; i++ {
		fixture.alert(fmt.Sprintf("removed-%02d", i), fmt.Sprintf("198.51.100.%d", i))
	}
	fixture.confirmedStep(t)
	if len(fixture.link.closed()) != 0 || fixture.recorded.route.closer.Stats()[filterclose.OutcomeExpectExceeded] != 1 {
		t.Fatalf("closed %v, stats %v", fixture.link.closed(), fixture.recorded.route.closer.Stats())
	}
	// The refusal is not settled: the strategy waits a few rounds, then is
	// tried again, and once the record holds the episodes the close goes.
	sent := fixture.closes()
	for i := 0; i < scopeRefusedRounds; i++ {
		fixture.loop.step(context.Background(), fixture.snapshot)
	}
	if fixture.closes() != sent || fixture.loop.Stats()[scopeCloseBackoff] != scopeRefusedRounds {
		t.Fatalf("requests %d while backing off, stats %v", fixture.closes()-sent, fixture.loop.Stats())
	}
	for i := 0; i < 21; i++ {
		fixture.opened(t, 100+i, fmt.Sprintf("198.51.100.%d", i))
	}
	fixture.loop.step(context.Background(), fixture.snapshot)
	if got := fixture.link.closed(); len(got) != 21 {
		t.Fatalf("closed %d after the record caught up, stats %v", len(got), fixture.recorded.route.closer.Stats())
	}
}

// A scope too large for one request is not sent, not cut: counted; and a
// resolution that is not Complete drives nothing.
func TestATooLargeOrIncompleteScopeSendsNothing(t *testing.T) {
	var members []string
	for i := 0; i < 300; i++ {
		members = append(members, fmt.Sprintf("198.51.100.%d|%d", i%250, i/250))
	}
	fixture := newScopeFixture(t, members...)
	fixture.alert("removed", "192.0.2.9")
	fixture.confirmedStep(t)
	if fixture.closes() != 0 || len(fixture.link.requests("/api/v1/alerts/search")) != 0 ||
		fixture.recorded.route.closer.Stats()[filterclose.OutcomeTargetListTooLarge] != 1 {
		t.Fatalf("requests %d, stats %v", fixture.closes(), fixture.recorded.route.closer.Stats())
	}
	incomplete := newScopeFixture(t, "192.0.2.1|0")
	incomplete.resolver.state = targetplan.ResolutionIncomplete
	incomplete.alert("removed", "192.0.2.9")
	incomplete.loop.step(context.Background(), incomplete.snapshot)
	if incomplete.closes() != 0 || incomplete.loop.Stats()[scopeCloseResolutionIncomplete] != 1 {
		t.Fatalf("an incomplete resolution sent %d, stats %v", incomplete.closes(), incomplete.loop.Stats())
	}
}

// A target that resolves Complete with no member closes nothing, counted
// scope_empty, however many rounds read it: the record would not stop it,
// since every entry with a target is outside an empty scope. The same
// strategy with a member closes the removed target's alert by the pairs.
func TestAnEmptyScopeClosesNothing(t *testing.T) {
	fixture := newScopeFixture(t)
	fixture.alert("removed", "192.0.2.9")
	fixture.alert("kept", "192.0.2.1")
	fixture.opened(t, 1, "192.0.2.9")
	fixture.opened(t, 2, "192.0.2.1")
	for i := 0; i < 3; i++ {
		fixture.loop.step(context.Background(), fixture.snapshot)
	}
	if fixture.closes() != 0 || fixture.loop.Stats()[scopeCloseEmpty] != 3 {
		t.Fatalf("an empty scope sent %d requests, stats %v", fixture.closes(), fixture.loop.Stats())
	}
	fixture.resolver.members = []string{"192.0.2.1|0"}
	fixture.confirmedStep(t)
	if got := fixture.link.closed(); len(got) != 1 || got["removed"] != 1 {
		t.Fatalf("closed %v, want removed only", got)
	}
}

// A busy link does not settle the scope: the next round sends again. A
// leader that lost and regained leadership sends again for an unchanged
// scope, after reading it twice.
func TestTheScopeCloseRetriesABusyLinkAndResendsAfterLeadershipReturns(t *testing.T) {
	fixture := newScopeFixture(t, "192.0.2.1|0")
	fixture.alert("removed", "192.0.2.9")
	fixture.opened(t, 1, "192.0.2.9")
	fixture.link.mu.Lock()
	fixture.link.busy = true
	fixture.link.mu.Unlock()
	fixture.confirmedStep(t)
	if len(fixture.link.closed()) != 0 || fixture.loop.Stats()[scopeCloseSettled] != 0 {
		t.Fatalf("closed %v, stats %v while busy", fixture.link.closed(), fixture.loop.Stats())
	}
	fixture.link.mu.Lock()
	fixture.link.busy = false
	fixture.link.mu.Unlock()
	fixture.loop.step(context.Background(), fixture.snapshot)
	if got := fixture.link.closed(); got["removed"] != 1 || fixture.loop.Stats()[scopeCloseSettled] != 1 {
		t.Fatalf("closed %v, stats %v after the link answered", got, fixture.loop.Stats())
	}

	fixture.alert("removed-later", "192.0.2.8")
	fixture.opened(t, 2, "192.0.2.8")
	fixture.loop.bundle.mu.Lock()
	fixture.loop.bundle.controlLeader = false
	fixture.loop.bundle.mu.Unlock()
	fixture.loop.step(context.Background(), fixture.snapshot)
	fixture.loop.bundle.mu.Lock()
	fixture.loop.bundle.controlLeader = true
	fixture.loop.bundle.mu.Unlock()
	fixture.confirmedStep(t)
	if got := fixture.link.closed(); got["removed-later"] != 1 {
		t.Fatalf("closed %v after leadership returned, stats %v", got, fixture.loop.Stats())
	}
}
