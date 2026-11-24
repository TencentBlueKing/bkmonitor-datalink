package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/absentalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/filterclose"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

type absentTestControl struct {
	snapshot    controlplane.ObservedSnapshot
	haveSnaphot bool
	departed    []controlplane.DepartedStrategy
	refused     uint64
	capability  map[string]string
}

func (control *absentTestControl) ObservedSnapshot() (controlplane.ObservedSnapshot, bool) {
	return control.snapshot, control.haveSnaphot
}
func (control *absentTestControl) DepartedStrategies() ([]controlplane.DepartedStrategy, uint64) {
	return control.departed, control.refused
}
func (control *absentTestControl) CapabilityRefused() map[string]string {
	return control.capability
}

// liveSnapshot is a source observation of the given size that never lists
// the deleted strategy, under its writer's statement.
func liveSnapshot(observation string, at time.Time, size int) controlplane.ObservedSnapshot {
	snapshot := controlplane.ObservedSnapshot{Observation: observation, ReadAt: at, HoldsLastGood: true}
	for i := 0; i < size; i++ {
		snapshot.Strategies = append(snapshot.Strategies, controlplane.DepartedStrategy{
			TenantID: "system", StrategyID: "live-" + strconv.Itoa(i), BusinessID: 2})
	}
	return snapshot
}

type absentTestFixture struct {
	loop    *absentStrategyClose
	control *absentTestControl
	link    *fakeLinkd
	now     time.Time
}

// absentAlert is an active alert of this deployment's source on the
// strategy, at version 7.
func absentAlert(id, strategyID string) *fakeLinkdAlert {
	return &fakeLinkdAlert{id: id, fingerprint: fmt.Sprintf("%032x", len(id)*7919+len(strategyID)),
		labels: map[string]string{"strategy_id": strategyID, "strategy_version": "7"}}
}

// newAbsentFixture is a deployment where strategy 10 was deleted before
// this process ever saw it: the link holds the alerts given (strategy 10's
// unless they say otherwise) and one alert of the running strategy live-1;
// the snapshot does not list 10, and nothing in the catalog remembers it.
func newAbsentFixture(t *testing.T, alerts []*fakeLinkdAlert) *absentTestFixture {
	t.Helper()
	start := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	fixture := &absentTestFixture{now: start, link: newFakeLinkd(t)}
	fixture.control = &absentTestControl{snapshot: liveSnapshot("observation-one", start, 100), haveSnaphot: true}
	fixture.link.add(&fakeLinkdAlert{id: "live-alert", fingerprint: strings.Repeat("e", 32),
		labels: map[string]string{"strategy_id": "live-1", "strategy_version": "3"}})
	for _, alert := range alerts {
		fixture.link.add(alert)
	}
	bundle := &phaseTwoWorkerBundle{dependencies: phaseTwoWorkerBundleDependencies{
		Now: func() time.Time { return fixture.now }, Observer: observability.NopObserver{}}}
	bundle.controlLeader = true
	fixture.loop = newAbsentStrategyClose(bundle, fixture.control, newFakeLinkdRoute(t, fixture.link))
	return fixture
}

// mature runs the first round, a second under the same observation, then a
// third under a second observation of the source: absentCloseMinReads reads,
// the earliest a close of a strategy with no mark can be sent.
func (fixture *absentTestFixture) mature(ctx context.Context) {
	fixture.loop.step(ctx)
	fixture.readAgain()
	fixture.control.snapshot = liveSnapshot("observation-two", fixture.now, 100)
	fixture.loop.step(ctx)
}

// readAgain is one more deciding round under the snapshot as it stands,
// absentCloseInterval after the last, and moves the clock on another
// interval: with the round before it and the one after, absentCloseMinReads
// reads.
func (fixture *absentTestFixture) readAgain() {
	fixture.now = fixture.now.Add(absentCloseInterval)
	fixture.loop.step(context.Background())
	fixture.now = fixture.now.Add(absentCloseInterval)
}

// closeFilters is the strategy ids the link's filtered closes named, in
// order, dry runs included.
func (fixture *absentTestFixture) closeFilters(t *testing.T) []string {
	t.Helper()
	var ids []string
	for _, body := range fixture.link.requests("/api/v1/alerts/close") {
		var request struct {
			Filter json.RawMessage `json:"filter"`
		}
		if err := json.Unmarshal([]byte(body), &request); err != nil {
			t.Fatalf("close body %s: %v", body, err)
		}
		text := string(request.Filter)
		at := strings.Index(text, `"labels.strategy_id":"`)
		if at < 0 {
			t.Fatalf("close filter %s names no strategy", text)
		}
		rest := text[at+len(`"labels.strategy_id":"`):]
		ids = append(ids, rest[:strings.Index(rest, `"`)])
	}
	return ids
}

// An empty strategy list under the writer's statement is a fact: the loop
// decides on it and, after its own grace and under a second observation,
// closes the alerts of a strategy that is gone - its tenant walked because
// the catalog remembers the strategy's departure. Without the statement
// the same empty list is refused as snapshot_empty and closes nothing.
func TestAnEmptyListIsDecidedOnOnlyUnderTheWritersStatement(t *testing.T) {
	for _, stated := range []bool{true, false} {
		fixture := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("alert-1", "10")})
		fixture.control.departed = []controlplane.DepartedStrategy{{TenantID: "system", StrategyID: "10", BusinessID: 2, Revision: 7}}
		ctx := context.Background()
		fixture.control.snapshot = liveSnapshot("observation-one", fixture.now, 0)
		fixture.control.snapshot.HoldsLastGood = stated
		fixture.loop.step(ctx)
		fixture.readAgain()
		fixture.control.snapshot = liveSnapshot("observation-two", fixture.now, 0)
		fixture.control.snapshot.HoldsLastGood = stated
		fixture.loop.step(ctx)
		rounds := fixture.loop.Rounds()
		closed := fixture.link.closed()
		if stated {
			if rounds[absentalerts.RefusalNone] != 3 || rounds[absentalerts.RefusalSnapshotEmpty] != 0 ||
				closed["alert-1"] != 1 || fixture.loop.Stats()[absentalerts.OutcomeCloseDecided] == 0 {
				t.Fatalf("empty list under the statement: rounds %+v stats %+v closed %+v, want decided and closed",
					rounds, fixture.loop.Stats(), closed)
			}
			continue
		}
		if rounds[absentalerts.RefusalSnapshotEmpty] != 3 || rounds[absentalerts.RefusalNone] != 0 || len(closed) != 0 {
			t.Fatalf("empty list without the statement: rounds %+v closed %+v, want refused as snapshot_empty", rounds, closed)
		}
	}
}

// A list that is not empty, without its writer's statement, closes
// nothing: every round is refused as writer_unstated and counted, the
// link's discovery still runs, and one line names the statement's key -
// once in the term, not once a round. A round with the statement re-arms
// the line; a new term does too.
func TestAListWithoutTheWritersStatementClosesNothingAndSaysSoOnce(t *testing.T) {
	fixture := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("alert-1", "10")})
	var lines bytes.Buffer
	fixture.loop.logger = observability.New(observability.ComponentRuntime, &lines)
	fixture.loop.statementKey = "cache.publication_semantics"
	ctx := context.Background()
	unstated := func(observation string) {
		fixture.control.snapshot = liveSnapshot(observation, fixture.now, 100)
		fixture.control.snapshot.HoldsLastGood = false
		fixture.loop.step(ctx)
		fixture.now = fixture.now.Add(absentCloseInterval)
	}
	for _, observation := range []string{"observation-one", "observation-two", "observation-three"} {
		unstated(observation)
	}
	said := func() int { return strings.Count(lines.String(), `"stage":"absent_close_writer_unstated"`) }
	if rounds := fixture.loop.Rounds(); rounds[absentalerts.RefusalWriterUnstated] != 3 || rounds[absentalerts.RefusalNone] != 0 ||
		len(fixture.link.closed()) != 0 || len(fixture.link.requests("/api/v1/alerts/search")) == 0 {
		t.Fatalf("rounds %+v closed %+v searches %d, want three writer_unstated rounds, discovery run and nothing closed",
			rounds, fixture.link.closed(), len(fixture.link.requests("/api/v1/alerts/search")))
	}
	if said() != 1 || !strings.Contains(lines.String(), `"statement_key":"cache.publication_semantics"`) {
		t.Fatalf("lines %q, want one naming the statement's key", lines.String())
	}
	fixture.control.snapshot = liveSnapshot("observation-four", fixture.now, 100)
	fixture.loop.step(ctx)
	unstated("observation-five")
	if said() != 2 {
		t.Fatalf("after a stated round the line was written %d times, want 2", said())
	}
}

// The whole capability, end to end: a strategy deleted before this process
// saw it, still holding an alert of this deployment's source, gets it
// closed through the link's filter - found by the link's search, not on
// the first round or against one observation - and the running strategy's
// alert stays open.
func TestTheAlertsOfAStrategyDeletedBeforeThisProcessSawItAreClosed(t *testing.T) {
	fixture := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("alert-1", "10")})
	ctx := context.Background()

	fixture.loop.step(ctx)
	if len(fixture.link.closed()) != 0 || fixture.loop.Stats()[absentalerts.OutcomeWithinGrace] != 1 {
		t.Fatalf("the first round closed, or did not say why it did not: %+v", fixture.loop.Stats())
	}
	fixture.readAgain()
	fixture.loop.step(ctx)
	if len(fixture.link.closed()) != 0 || fixture.loop.Stats()[absentalerts.OutcomeUnconfirmed] != 1 {
		t.Fatalf("one observation confirmed an absence: %+v", fixture.loop.Stats())
	}
	fixture.control.snapshot = liveSnapshot("observation-two", fixture.now, 100)
	fixture.loop.step(ctx)
	if closed := fixture.link.closed(); len(closed) != 1 || closed["alert-1"] != 1 {
		t.Fatalf("the alert of a deleted strategy was not closed alone, once: %+v", closed)
	}
	fixture.link.mu.Lock()
	reason := fixture.link.alerts["alert-1"].reason
	fixture.link.mu.Unlock()
	if reason != "策略已被停用或删除，告警关闭" {
		t.Fatalf("the close reason %q is not the platform's own for a strategy gone without the writer saying how", reason)
	}
	closes := fixture.link.requests("/api/v1/alerts/close")
	for _, field := range []string{`"bk_tenant_id":"system"`, `"event_source_id":"built_in_bk"`, `"labels.strategy_id":"10"`} {
		if len(closes) == 0 || !strings.Contains(closes[len(closes)-1], field) {
			t.Fatalf("the close %v does not carry %s", closes, field)
		}
	}
	if difference := fixture.loop.Difference(); difference["found_strategies"] != 1 || difference["discovery_tenants"] != 1 ||
		difference["discovery_complete"] != 1 || difference["candidates"] != 1 || difference["snapshot_strategies"] != 100 {
		t.Fatalf("the round's sizes are not what it read: %+v", difference)
	}
}

// A strategy that still exists and that its publication refused for a
// capability: its alert is closed as no longer detected by alarmd after
// the grace under two observations, as an absent strategy's is. The same
// strategy refused for nothing keeps its alert, one refused for a
// reversible configuration keeps it too, and one refused for a round and
// supported the next starts its grace again when it is refused once more.
func TestTheAlertsOfAStrategyRefusedForACapabilityAreClosedAsNoLongerDetected(t *testing.T) {
	listed := func(observation string, at time.Time) controlplane.ObservedSnapshot {
		snapshot := liveSnapshot(observation, at, 100)
		snapshot.Strategies = append(snapshot.Strategies, controlplane.DepartedStrategy{TenantID: "system", StrategyID: "10", BusinessID: 2})
		return snapshot
	}
	newRefusedFixture := func(capability map[string]string) *absentTestFixture {
		fixture := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("alert-1", "10")})
		fixture.control.snapshot = listed("observation-one", fixture.now)
		fixture.control.capability = capability
		return fixture
	}
	advance := func(fixture *absentTestFixture, observation string) {
		fixture.readAgain()
		fixture.control.snapshot = listed(observation, fixture.now)
	}
	ctx := context.Background()

	refused := newRefusedFixture(map[string]string{"10": "QUERY_SOURCE_RETIRED"})
	refused.loop.step(ctx)
	if len(refused.link.closed()) != 0 {
		t.Fatalf("the first round closed: %+v", refused.link.closed())
	}
	advance(refused, "observation-two")
	refused.loop.step(ctx)
	if closed := refused.link.closed(); len(closed) != 1 || closed["alert-1"] != 1 {
		t.Fatalf("the alert of a refused strategy was not closed: %+v (stats %+v)", closed, refused.loop.Stats())
	}
	refused.link.mu.Lock()
	reason := refused.link.alerts["alert-1"].reason
	refused.link.mu.Unlock()
	if !strings.Contains(reason, filterclose.DecisionUnsupported) {
		t.Fatalf("close reason %q, want the unsupported decision", reason)
	}
	page, ok := refused.loop.Page(ctx, fleet.AbsentCandidateQuery{Limit: 10})
	if !ok || len(page.Rows) != 1 || page.Rows[0].StrategyID != "10" || page.Rows[0].RefusedFor != "QUERY_SOURCE_RETIRED" {
		t.Fatalf("absent.list rows = %+v, want strategy 10 naming the capability it was refused for", page.Rows)
	}

	supported := newRefusedFixture(nil)
	supported.loop.step(ctx)
	advance(supported, "observation-two")
	supported.loop.step(ctx)
	if len(supported.link.closed()) != 0 {
		t.Fatalf("a listed strategy refused for nothing was closed: %+v", supported.link.closed())
	}
	reversible := newRefusedFixture(map[string]string{"10": "COMPLETION_OFFSET_BELOW_RESERVE"})
	reversible.loop.step(ctx)
	advance(reversible, "observation-two")
	reversible.loop.step(ctx)
	if len(reversible.link.closed()) != 0 {
		t.Fatalf("a strategy refused for a reversible configuration was closed: %+v", reversible.link.closed())
	}

	flapping := newRefusedFixture(map[string]string{"10": "QUERY_SOURCE_RETIRED"})
	flapping.loop.step(ctx)
	advance(flapping, "observation-two")
	flapping.control.capability = nil
	flapping.loop.step(ctx)
	advance(flapping, "observation-three")
	flapping.control.capability = map[string]string{"10": "QUERY_SOURCE_RETIRED"}
	flapping.loop.step(ctx)
	if len(flapping.link.closed()) != 0 {
		t.Fatalf("a strategy supported in between was closed on its earlier refusal: %+v", flapping.link.closed())
	}
}

// What the catalog remembers names the close: the page reads the identity
// as the catalog's, and the close is sent.
func TestARememberedIdentityNamesTheClose(t *testing.T) {
	fixture := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("alert-1", "10")})
	fixture.control.departed = []controlplane.DepartedStrategy{{TenantID: "system", StrategyID: "10", BusinessID: 5, Revision: 9}}
	fixture.mature(context.Background())
	row := absentRowOf(t, readAbsentPage(t, fixture.loop, fleet.AbsentCandidateQuery{}), "10")
	if fixture.link.closed()["alert-1"] != 1 || row.Execution == nil || row.Execution.Identity == nil ||
		row.Execution.Identity.Source != fleet.AbsentIdentityCatalog || row.Execution.Identity.Business != 5 || row.Execution.Identity.Revision != 9 {
		t.Fatalf("the remembered identity did not name the close: closed %+v, row %+v", fixture.link.closed(), row.Execution)
	}
}

// Whose alert it is, is a fact the alert carries: the filter names this
// deployment's source and the tenant, so another producer's alert of the
// same strategy, and another tenant's, stay open.
func TestOnlyThisDeploymentsOwnAlertsAreClosed(t *testing.T) {
	theirs := absentAlert("theirs", "10")
	theirs.source = "another-source"
	elsewhere := absentAlert("elsewhere", "10")
	elsewhere.tenant = "another-tenant"
	fixture := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("mine", "10"), theirs, elsewhere})
	fixture.mature(context.Background())
	if closed := fixture.link.closed(); len(closed) != 1 || closed["mine"] != 1 {
		t.Fatalf("the closes are not exactly this deployment's own alert: %+v", closed)
	}
	stats := fixture.loop.Stats()
	if stats[absentalerts.OutcomeCloseDecided] != 1 || stats[absentalerts.OutcomeCloseSent] != 1 {
		t.Fatalf("outcomes = %+v, want one strategy decided and its close sent", stats)
	}
}

func TestAFollowerClosesNothingAndSaysWhy(t *testing.T) {
	fixture := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("alert-1", "10")})
	fixture.loop.bundle.controlLeader = false
	fixture.loop.step(context.Background())
	if len(fixture.link.requests("/api/v1/alerts/search")) != 0 || fixture.loop.Stats()[absentalerts.OutcomeNotLeader] != 1 {
		t.Fatalf("a follower asked the link, or did not say it was one: %+v", fixture.loop.Stats())
	}
}

func TestLosingTheControlTermRestartsTheGrace(t *testing.T) {
	fixture := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("alert-1", "10")})
	ctx := context.Background()
	fixture.loop.step(ctx)
	fixture.loop.bundle.controlLeader = false
	fixture.loop.step(ctx)
	fixture.loop.bundle.controlLeader = true
	fixture.readAgain()
	fixture.control.snapshot = liveSnapshot("observation-two", fixture.now, 100)
	fixture.loop.step(ctx)
	if len(fixture.link.closed()) != 0 {
		t.Fatalf("a close matured on an absence observed under a lost term: %+v", fixture.link.closed())
	}
}

// No discovery, no difference, and the round names why: a busy link, and
// a link that cannot search at all - counted filter_close_unsupported by
// the closer, with no other path to close by.
func TestAnUnreachableLinkRefusesTheRoundByName(t *testing.T) {
	busy := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("alert-1", "10")})
	busy.link.busy = true
	busy.mature(context.Background())
	if len(busy.link.closed()) != 0 || busy.loop.Rounds()[absentalerts.RefusalLinkUnavailable] != 3 {
		t.Fatalf("a round without the discovery decided, or did not say why: %+v", busy.loop.Rounds())
	}

	unsupported := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("alert-1", "10")})
	unsupported.link.unsupported = true
	unsupported.mature(context.Background())
	if len(unsupported.link.closed()) != 0 || unsupported.loop.Rounds()[absentalerts.RefusalLinkUnavailable] != 3 ||
		unsupported.loop.filter.closer.Stats()[filterclose.OutcomeUnsupported] != 3 {
		t.Fatalf("a link that cannot search: rounds %+v closer %+v", unsupported.loop.Rounds(), unsupported.loop.filter.closer.Stats())
	}
	if searches := unsupported.link.requests("/api/v1/alerts/search"); len(searches) != 1 {
		t.Fatalf("the link was asked %d times after it said it cannot search, want once", len(searches))
	}
}

// A discovery that fails after its first search leaves the round unread:
// what it found before the failure is not decided on as a complete roster.
func TestADiscoveryThatBreaksOffRefusesTheRound(t *testing.T) {
	fixture := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("alert-10", "10"), absentAlert("alert-11", "11")})
	fixture.link.searchBusyAfter = 1
	fixture.mature(context.Background())
	if len(fixture.link.closed()) != 0 || fixture.loop.Rounds()[absentalerts.RefusalLinkUnavailable] != 3 ||
		fixture.loop.Difference()["discovery_complete"] != 0 {
		t.Fatalf("a broken discovery decided: rounds %+v difference %+v closed %+v",
			fixture.loop.Rounds(), fixture.loop.Difference(), fixture.link.closed())
	}
}

// A strategy list that lost most of its strategies since the round before
// is decided on, with or without the writer's statement: the writer takes a
// strategy out only when it is gone, and the reader keeps no size gate to
// second-guess it with.
func TestASnapshotThatShrankIsDecidedOn(t *testing.T) {
	fixture := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("alert-1", "10")})
	ctx := context.Background()
	fixture.loop.step(ctx)
	for round, observation := range []string{"observation-two", "observation-three"} {
		fixture.now = fixture.now.Add(absentCloseInterval)
		fixture.control.snapshot = liveSnapshot(observation, fixture.now, 40)
		fixture.loop.step(ctx)
		if decided := fixture.loop.Rounds()[absentalerts.RefusalNone]; decided != uint64(round+2) {
			t.Fatalf("round %d after the shrink: %d rounds decided, want %d: %+v", round+1, decided, round+2, fixture.loop.Rounds())
		}
	}
	if fixture.loop.Stats()[absentalerts.OutcomeCloseDecided] < 1 || fixture.link.closed()["alert-1"] != 1 {
		t.Fatalf("the absent strategy was not closed after the shrink: %+v %+v", fixture.loop.Stats(), fixture.link.closed())
	}
}

// absentSourceStrategyIDs is the strategy_ids value a writer stores for
// strategies 1001 up to 1000+n.
func absentSourceStrategyIDs(n int) string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = strconv.Itoa(1001 + i)
	}
	return "[" + strings.Join(ids, ",") + "]"
}

// absentSourceReconciler is the control plane the close loop asks in
// production: a SourceReconciler reading the Legacy strategy cache on a real
// Redis, which holds strategies 1001..1100 and nothing else.
func absentSourceReconciler(t *testing.T, client *redis.Client, now func() time.Time) (*controlplane.SourceReconciler, func()) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < 100; i++ {
		id := 1001 + i
		document := fmt.Sprintf(`{"id":%d,"bk_biz_id":2,"bk_tenant_id":"system","space_uid":"bkcc__2","update_time":1,`+
			`"items":[{"id":1,"query_md5":"absent-%d","expression":"a","unit":"","query_configs":[{"data_source_label":"bk_monitor",`+
			`"data_type_label":"time_series","metric_field":"usage","alias":"a","agg_dimension":["host"],"agg_method":"MAX",`+
			`"agg_interval":60,"result_table_id":"system.cpu"}],"algorithms":[{"level":1,"type":"Threshold","config":[[{"method":"gte","threshold":80}]]}]}],`+
			`"detects":[{"level":1,"priority":1,"connector":"and","trigger_config":{"count":1,"check_window":1}}]}`, id, id)
		if err := client.Set(ctx, "bkmonitor.cache.strategy_"+strconv.Itoa(id), document, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:absent-close", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), cfg.CompilerLimits())
	if err != nil {
		t.Fatal(err)
	}
	sm, err := state.RuntimeStateSemantics()
	if err != nil {
		t.Fatal(err)
	}
	semantics := strategy.StateSemantics{StateSchemaVersion: sm.StateSchemaVersion, CodecSemanticsVersion: sm.CodecSemanticsVersion,
		IdentitySchemaDigest: sm.IdentitySchemaDigest, SourceTimeSemanticsVersion: sm.SourceTimeSemanticsVersion, HistoryCellSemanticsVersion: sm.HistoryCellSemanticsVersion}
	accessBKData := true
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", controlplane.LegacyQueryRuntimeFacts{AccessBKData: &accessBKData,
		BKDataCMDBLevelTables: []string{}, SystemDiskFilter: controlplane.LegacyRuntimeFilterFact{FieldName: "device_type", Values: []string{"iso9660"}},
		SystemNetworkFilter: controlplane.LegacyRuntimeFilterFact{FieldName: "device_name", Values: []string{"lo"}}})
	if err != nil {
		t.Fatal(err)
	}
	source, err := controlplane.NewLegacyRedisStrategySource(client, "bkmonitor.cache")
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := controlplane.NewSourceReconciler(repository, compiler, semantics)
	if err != nil {
		t.Fatal(err)
	}
	if err := reconciler.ConfigureClock(now); err != nil {
		t.Fatal(err)
	}
	refresh := func() {
		t.Helper()
		result, err := reconciler.Refresh(ctx, source, planner)
		if err != nil || result.ReadMode != controlplane.SourceReadFull {
			t.Fatalf("Refresh() = (%+v, %v), want a full read of the source", result, err)
		}
	}
	return reconciler, refresh
}

// The production control plane, end to end: a writer rewrites strategy_ids
// and drops sixty of a hundred strategies. Without its statement every
// round is writer_unstated and nothing is closed; a statement about
// another set of ids (its digest not this list's) is no statement. Under
// a statement about this very list the rounds are decided on - nothing
// compares a list with the size of the one before it - and the absent
// strategy is closed after the grace under a second observation, while the
// running strategy's alert is never found as absent.
func TestAListThatLostMostOfItsStrategiesIsDecidedOnOnlyUnderItsStatement(t *testing.T) {
	_, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	fixture := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("alert-1", "10"), absentAlert("running", "1001")})
	reconciler, refresh := absentSourceReconciler(t, client, func() time.Time { return fixture.now })
	fixture.loop.control = reconciler
	later := func() { fixture.now = fixture.now.Add(absentCloseInterval) }
	// publish writes the list, moves last_updated, and states the
	// publication about the ids named (none: no statement).
	lastUpdated := int64(1_790_000_000)
	publish := func(ids, statedAbout string) {
		t.Helper()
		lastUpdated++
		pipe := client.TxPipeline()
		pipe.Set(ctx, "bkmonitor.cache.strategy_ids", ids, 0)
		pipe.Set(ctx, "bkmonitor.cache.last_updated", strconv.FormatInt(lastUpdated, 10), 0)
		pipe.Del(ctx, "bkmonitor.cache.publication_semantics")
		if statedAbout != "" {
			digest := sha256.Sum256([]byte(statedAbout))
			pipe.Set(ctx, "bkmonitor.cache.publication_semantics", fmt.Sprintf(
				`{"hold_last_good":true,"last_updated":%d,"strategy_ids_sha256":"%x","version":1}`, lastUpdated, digest), 0)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			t.Fatal(err)
		}
		refresh()
	}
	hundred, forty := absentSourceStrategyIDs(100), absentSourceStrategyIDs(40)

	publish(hundred, "")
	fixture.loop.step(ctx)
	for round, statedAbout := range []string{"", hundred} {
		later()
		publish(forty, statedAbout)
		fixture.loop.step(ctx)
		if rounds := fixture.loop.Rounds(); rounds[absentalerts.RefusalWriterUnstated] != uint64(round+2) || rounds[absentalerts.RefusalNone] != 0 ||
			len(fixture.link.closed()) != 0 {
			t.Fatalf("unstated round %d: %+v closed %+v", round+2, rounds, fixture.link.closed())
		}
	}
	for round := 1; round <= absentCloseMinReads; round++ {
		later()
		publish(forty, forty)
		fixture.loop.step(ctx)
		if fixture.loop.Rounds()[absentalerts.RefusalNone] != uint64(round) || fixture.loop.Difference()["snapshot_strategies"] != 40 {
			t.Fatalf("stated round %d on the forty: %+v %+v", round, fixture.loop.Rounds(), fixture.loop.Difference())
		}
		if closed := fixture.link.closed(); round < absentCloseMinReads && len(closed) != 0 {
			t.Fatalf("closed on stated round %d, before its reads: %+v", round, closed)
		}
	}
	if closed := fixture.link.closed(); fixture.loop.Stats()[absentalerts.OutcomeCloseDecided] < 1 || closed["alert-1"] != 1 || closed["running"] != 0 {
		t.Fatalf("the absent strategy was not closed, or the running one was: %+v %+v", fixture.loop.Stats(), closed)
	}
}

func TestARefusedRoundIsCountedAsARoundNotAsACandidate(t *testing.T) {
	fixture := newAbsentFixture(t, nil)
	fixture.control.haveSnaphot = false
	fixture.loop.step(context.Background())
	if fixture.loop.Rounds()[absentalerts.RefusalSnapshotUnusable] != 1 {
		t.Fatalf("the refused round was not counted as a round: %+v", fixture.loop.Rounds())
	}
	for outcome, count := range fixture.loop.Stats() {
		if count != 0 {
			t.Fatalf("a refused round put %s in the candidate family: %+v", outcome, fixture.loop.Stats())
		}
	}
}

// The reviewer's case end to end: the source document of strategy 10 lost its
// tenant, so the snapshot lists it under an empty one, and nothing runs a
// Plan of it. It still exists, its alert stays open, and the other
// tenant's walk still decides: the empty tenant is not searched.
func TestAStrategyWhoseDocumentLostItsTenantKeepsItsAlerts(t *testing.T) {
	fixture := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("alert-1", "10"), absentAlert("alert-11", "11")})
	withoutTenant := func(snapshot controlplane.ObservedSnapshot) controlplane.ObservedSnapshot {
		snapshot.Strategies = append(snapshot.Strategies, controlplane.DepartedStrategy{StrategyID: "10"})
		return snapshot
	}
	ctx := context.Background()
	fixture.control.snapshot = withoutTenant(fixture.control.snapshot)
	fixture.loop.step(ctx)
	fixture.readAgain()
	fixture.control.snapshot = withoutTenant(liveSnapshot("observation-two", fixture.now, 100))
	fixture.loop.step(ctx)
	if closed := fixture.link.closed(); closed["alert-1"] != 0 || closed["alert-11"] != 1 || fixture.loop.Difference()["candidates"] != 1 {
		t.Fatalf("closed %+v candidates %d: want strategy 10 kept (it exists without a tenant) and 11 closed",
			closed, fixture.loop.Difference()["candidates"])
	}
}

// A deleted strategy's alerts are all closed in the round that decides
// them, page after page, each once.
func TestEveryAlertOfADeletedStrategyIsClosedInOneRound(t *testing.T) {
	var alerts []*fakeLinkdAlert
	for i := 0; i < 600; i++ {
		alerts = append(alerts, absentAlert(fmt.Sprintf("alert-%04d", i), "10"))
	}
	fixture := newAbsentFixture(t, alerts)
	fixture.mature(context.Background())
	closed := fixture.link.closed()
	for _, alert := range alerts {
		if closed[alert.id] != 1 {
			t.Fatalf("alert %s closed %d times in the deciding round, want once (%d of %d closed)", alert.id, closed[alert.id], len(closed), len(alerts))
		}
	}
	if len(closed) != len(alerts) || fixture.loop.Stats()[absentalerts.OutcomeCloseSent] != 1 {
		t.Fatalf("closed %d of %d alerts (stats %+v)", len(closed), len(alerts), fixture.loop.Stats())
	}
}

// A round cut while it closes leaves the strategies it did not reach first
// in line: the next round starts after the close it was cut in, not at the
// first strategy again, which would be cut there every time.
func TestARoundCutWhileClosingResumesAtTheFirstCloseNotExecuted(t *testing.T) {
	fixture := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("alert-10", "10"), absentAlert("alert-11", "11"), absentAlert("alert-12", "12")})
	for _, id := range []string{"10", "11", "12"} {
		fixture.control.departed = append(fixture.control.departed, controlplane.DepartedStrategy{TenantID: "system", StrategyID: id, BusinessID: 5, Revision: 9})
	}
	ctx := context.Background()
	fixture.loop.step(ctx)
	fixture.readAgain()
	var attempted []string
	for round := 0; round < 3; round++ {
		cut, cancel := context.WithCancel(ctx)
		fixture.link.onClose = func() { cancel() }
		fixture.now = fixture.now.Add(absentCloseInterval)
		fixture.control.snapshot = liveSnapshot("observation-"+strconv.Itoa(round+2), fixture.now, 100)
		before := len(fixture.closeFilters(t))
		fixture.loop.step(cut)
		cancel()
		after := fixture.closeFilters(t)
		if len(after) == before {
			t.Fatalf("round %d attempted no close", round)
		}
		attempted = append(attempted, after[before])
	}
	if len(attempted) != 3 || attempted[0] == attempted[1] || attempted[1] == attempted[2] || attempted[0] == attempted[2] {
		t.Fatalf("rounds cut at their first close attempted %v, want each strategy once", attempted)
	}
}

// The absent-close round follows the source's re-read bound rather than
// repeating it as a second number: a round no slower than the re-read, and
// the reads a strategy with no mark waits for within the close bound.
func TestTheAbsentCloseRoundFollowsTheSourceReadBound(t *testing.T) {
	if absentCloseInterval > controlplane.SourceFullReadInterval {
		t.Fatalf("absent-close round %s is slower than the source re-read %s", absentCloseInterval, controlplane.SourceFullReadInterval)
	}
	if reads := time.Duration(absentCloseMinReads) * absentCloseInterval; reads > absentCloseBound {
		t.Fatalf("%d reads a round %s apart take %s, past the close bound %s", absentCloseMinReads, absentCloseInterval, reads, absentCloseBound)
	}
}

// A round's absent closes and its edit closes each have their own share of
// the round, and the two fit in it: a job now runs again while it gets
// somewhere, so either would otherwise fill the round and starve the other.
// Each share fits more than one request.
func TestTheAbsentAndEditClosesShareTheRound(t *testing.T) {
	if absentCloseRoundTime+editCloseRoundTime > absentCloseInterval {
		t.Fatalf("absent %s plus edit %s exceed the round %s", absentCloseRoundTime, editCloseRoundTime, absentCloseInterval)
	}
	if absentCloseRoundTime <= filterCloseHTTPTimeout || editCloseRoundTime <= filterCloseHTTPTimeout {
		t.Fatalf("a share (absent %s, edit %s) fits no more than one request of %s", absentCloseRoundTime, editCloseRoundTime, filterCloseHTTPTimeout)
	}
}

// The round's line and each strategy's close line carry how long they took,
// read from the log line as written: the round from its start to its
// decision (the link's searches in it), the close from its job's first
// request to its last.
func TestTheAbsentCloseLinesSayHowLongTheyTook(t *testing.T) {
	fixture := newAbsentFixture(t, []*fakeLinkdAlert{absentAlert("alert-1", "10"), absentAlert("alert-2", "10")})
	cost := 2 * time.Millisecond
	fixture.link.onSearch = func() { time.Sleep(cost) } // fixed-wait: the link's cost is what the lines measure
	fixture.link.onClose = func() { time.Sleep(cost) }  // fixed-wait: the link's cost is what the lines measure
	var output bytes.Buffer
	limiter, err := observability.NewScopedLogLimiter(observability.ScopedLogLimiterConfig{Window: time.Hour, MaxEvents: 100, MaxScopes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := observability.NewScopedBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatal(err)
	}
	fixture.loop.bundle.dependencies.Observer = observability.NewLoggingObserver(observability.New("alarmd", &output), policy)
	fixture.mature(context.Background())
	if closed := fixture.link.closed(); closed["alert-1"] != 1 || closed["alert-2"] != 1 {
		t.Fatalf("the deleted strategy's alerts were not closed: %+v", closed)
	}
	rounds, closes := 0, 0
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var entry struct {
			Stage      string `json:"stage"`
			Result     string `json:"result"`
			Reason     string `json:"reason_code"`
			DurationMS int64  `json:"duration_ms"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil || entry.Stage != observability.StageAbsentStrategyClose {
			continue
		}
		switch {
		case entry.Reason == absentalerts.OutcomeCloseSent:
			closes++
			if entry.DurationMS < cost.Milliseconds() {
				t.Fatalf("the close line says it took %d ms, want at least the link's %v a request: %s", entry.DurationMS, cost, line)
			}
		case entry.Result == string(observability.ResultSuccess):
			rounds++
			if entry.DurationMS < cost.Milliseconds() {
				t.Fatalf("the round line says it took %d ms, want at least the link's %v a search: %s", entry.DurationMS, cost, line)
			}
		}
	}
	if rounds == 0 || closes != 1 {
		t.Fatalf("read %d round lines and %d close lines, want rounds and one close:\n%s", rounds, closes, output.String())
	}
}
