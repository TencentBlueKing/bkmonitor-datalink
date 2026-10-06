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
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The one deployment shape the page went blind on: every platform cache on
// the same instance as the runtime store, so one client serves four roles.
// The list says which, and no field of it carries a credential.
func TestResolvedEndpointsNameSharingAndCarryNoCredential(t *testing.T) {
	cfg := config.Default()
	cfg.Redis.RedisConnectionConfig = config.RedisConnectionConfig{Mode: config.RedisModeSentinel,
		MasterName: "mymaster", SentinelAddress: []string{"sentinel-a:26379", "sentinel-b:26379"},
		Username: "alarmd", Password: "top-secret", SentinelPassword: "sentinel-secret", DB: 8}
	cfg.Redis.StatePrefix = "alarmd:phase2:g2:runtime:v1"
	cfg.Kafka.LegacyAdapter.SnapshotPrefix = "bk_monitorv3.ee.cache"
	cfg.Kafka.Brokers = []string{"kafka-0:9092", "kafka-1:9092"}
	cfg.Kafka.TriggerEvent.Topic = "0bkmonitor_backend_event"
	cfg.PhaseTwo.Access.UQEndpoint = "http://unify-query:10205"
	cache := cfg.Redis.Connection()
	cache.DB = 0
	cfg.PlatformCache.Strategy = &cache
	cfg.PlatformCache.CMDB = &cache
	sharing := endpointSharing{runtimeIsSource: false, cmdbSharedWith: fleet.EndpointStrategyCache, compatOutputPresent: true}
	endpoints := resolveEndpoints(cfg, sharing)
	byRole := map[string]fleet.Endpoint{}
	for _, entry := range endpoints {
		byRole[entry.Role] = entry
	}
	if len(endpoints) != len(fleet.EndpointRoles) {
		t.Fatalf("%d endpoints for %d roles", len(endpoints), len(fleet.EndpointRoles))
	}
	for _, role := range fleet.EndpointRoles {
		if _, present := byRole[role]; !present {
			t.Errorf("no endpoint for role %s", role)
		}
	}
	state := byRole[fleet.EndpointStateRedis]
	if state.Address != "mymaster@sentinel-a:26379,sentinel-b:26379" || state.Mode != config.RedisModeSentinel || state.DB == nil || *state.DB != 8 ||
		state.Prefix != "alarmd:phase2:g2:runtime:v1" || state.SharedWith != "" || !state.Configured {
		t.Errorf("state redis = %+v", state)
	}
	strategy := byRole[fleet.EndpointStrategyCache]
	if strategy.DB == nil || *strategy.DB != 0 || strategy.Prefix != "bk_monitorv3.ee.cache" || strategy.SharedWith != "" {
		t.Errorf("strategy cache = %+v", strategy)
	}
	if cmdb := byRole[fleet.EndpointCMDBCache]; cmdb.SharedWith != fleet.EndpointStrategyCache {
		t.Errorf("cmdb cache does not say it shares the strategy cache's connection: %+v", cmdb)
	}
	if dynamic := byRole[fleet.EndpointDynamicConfig]; dynamic.Configured || dynamic.Address != "" {
		t.Errorf("an unrendered dynamic config reads as configured: %+v", dynamic)
	}
	if kafka := byRole[fleet.EndpointOutputKafka]; kafka.Address != "kafka-0:9092,kafka-1:9092" || kafka.Prefix != "0bkmonitor_backend_event" || !kafka.Configured {
		t.Errorf("kafka = %+v", kafka)
	}
	if query := byRole[fleet.EndpointQueryBackend]; query.Address != "http://unify-query:10205" || !query.Configured {
		t.Errorf("query backend = %+v", query)
	}
	// The red line: nothing that is a credential in the configuration appears
	// anywhere in the published list, whatever field it might be copied into.
	encoded, err := json.Marshal(endpoints)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"top-secret", "sentinel-secret", "alarmd\"", "\"username\"", "\"password\""} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("the endpoint list carries %q: %s", secret, encoded)
		}
	}
	// A standalone address is the address.
	standalone := config.RedisConnectionConfig{Mode: config.RedisModeStandalone, Address: "redis:6379", Password: "x"}
	if got := redisAddress(standalone); got != "redis:6379" {
		t.Errorf("standalone address = %q", got)
	}
}

// Which hook record each role reads: its own client where it has one, the
// client of the role it shares a connection with where it does not.
func TestRedisClientForRoleFollowsTheSharing(t *testing.T) {
	shared := endpointSharing{runtimeIsSource: true, cmdbSharedWith: fleet.EndpointStateRedis, dynamicSharedWith: fleet.EndpointCMDBCache}
	for role, want := range map[string]string{
		fleet.EndpointStrategyCache: "source", fleet.EndpointStateRedis: "source", fleet.EndpointCMDBCache: "source",
		fleet.EndpointDynamicConfig: "source", fleet.EndpointCompatOutput: "legacy_output", fleet.EndpointOutputKafka: "",
	} {
		if got := shared.redisClientForRole(role); got != want {
			t.Errorf("shared %s -> %q, want %q", role, got, want)
		}
	}
	separate := endpointSharing{}
	for role, want := range map[string]string{
		fleet.EndpointStateRedis: "runtime", fleet.EndpointCMDBCache: "cmdb", fleet.EndpointDynamicConfig: "dynamic_config",
	} {
		if got := separate.redisClientForRole(role); got != want {
			t.Errorf("separate %s -> %q, want %q", role, got, want)
		}
	}
}

// The health beside each entry comes from the hook record of the connection
// the role uses, and the writer evidence from the leader's source round.
func TestEndpointFactsReadTheSharedConnectionAndTheSourceRound(t *testing.T) {
	cfg := config.Default()
	cfg.Redis.Address = "redis:6379"
	cfg.Kafka.LegacyAdapter.SnapshotPrefix = "bk_monitorv3.ee.cache"
	recorder := metric.NewRecorder(metric.BuildInfo{})
	moment := time.Unix(5000, 0)
	hook := recorder.RedisHook("source")
	ctx, _ := hook.BeforeProcess(context.Background(), nil)
	ok := redis.NewStringCmd(ctx, "get", "a")
	_ = hook.AfterProcess(ctx, ok)
	sharing := endpointSharing{runtimeIsSource: true, cmdbSharedWith: fleet.EndpointStrategyCache, compatOutputPresent: true}
	age := int64(95)
	source := func() *fleet.SourceFacts {
		facts := fleet.NewSourceFacts(moment, map[string]int{"SOURCE_INCOMPLETE": 81}, nil)
		facts.ChangeSignalPresent, facts.ChangeSignalAgeSeconds = true, &age
		return facts
	}
	// The hook stamps with the wall clock, so the facts read the same clock
	// and the age is asserted small rather than exact.
	sinkState := outputSinkState{Ready: false, Attempts: 3, LastFailureAt: time.Now().Add(-2 * time.Second),
		LastFailure: "kafka trigger event sink: open producer: client has run out of available brokers"}
	facts := endpointFactsSource(cfg, sharing, recorder, nil, nil, source, func() outputSinkState { return sinkState }, nil, time.Now)()
	byRole := map[string]fleet.Endpoint{}
	for _, entry := range facts {
		byRole[entry.Role] = entry
	}
	// Four roles on one connection: all four read the one record -- the
	// consumer's publication is read through the state store's client.
	for _, role := range []string{fleet.EndpointStateRedis, fleet.EndpointStrategyCache, fleet.EndpointCMDBCache, fleet.EndpointOpenAlertSet} {
		entry := byRole[role]
		if entry.LastSuccessAgeSeconds == nil || *entry.LastSuccessAgeSeconds < 0 || *entry.LastSuccessAgeSeconds > 5 {
			t.Errorf("%s last success age = %v, want a few seconds from the shared source record", role, entry.LastSuccessAgeSeconds)
		}
		if entry.LastFailureAgeSeconds != nil || entry.LastFailure != "" {
			t.Errorf("%s carries a failure nobody recorded: %+v", role, entry)
		}
	}
	// The compat output client has issued nothing: no health, not zero.
	if compat := byRole[fleet.EndpointCompatOutput]; compat.LastSuccessAgeSeconds != nil {
		t.Errorf("a connection that issued nothing reads as recently succeeded: %+v", compat)
	}
	// The output sink's own record: not open, three attempts, and what the
	// last one said, with no success age because it has never been open.
	output := byRole[fleet.EndpointOutputKafka]
	if output.Ready == nil || *output.Ready || output.Attempts == nil || *output.Attempts != 3 ||
		output.LastFailureAgeSeconds == nil || *output.LastFailureAgeSeconds < 1 || *output.LastFailureAgeSeconds > 6 ||
		output.LastFailure != sinkState.LastFailure || output.LastSuccessAgeSeconds != nil {
		t.Errorf("output kafka = %+v, want not ready after 3 attempts with the last failure and no success age", output)
	}
	// Once open: ready, the time since it opened on its own field, the
	// attempt count is how many it took, and the failure is gone. No success
	// age: opening is not a message acknowledged, and on the field that means
	// one it read as a producer that last succeeded when it started.
	sinkState = outputSinkState{Ready: true, Since: time.Now().Add(-40 * time.Second), Attempts: 4}
	opened := endpointFactsSource(cfg, sharing, recorder, nil, nil, source, func() outputSinkState { return sinkState }, nil, time.Now)()
	for _, entry := range opened {
		if entry.Role != fleet.EndpointOutputKafka {
			continue
		}
		if entry.Ready == nil || !*entry.Ready || entry.Attempts == nil || *entry.Attempts != 4 ||
			entry.ReadySinceAgeSeconds == nil || *entry.ReadySinceAgeSeconds < 39 || *entry.ReadySinceAgeSeconds > 45 ||
			entry.LastSuccessAgeSeconds != nil || entry.LastFailureAgeSeconds != nil || entry.LastFailure != "" {
			t.Errorf("open output kafka = %+v, want ready, 4 attempts, ~40 s since open on ready_since, no success age, no failure", entry)
		}
	}
	writer := byRole[fleet.EndpointStrategyCache].Writer
	if writer == nil || !writer.Present || writer.Count != 81 || writer.State != "marker_present" || writer.AgeSeconds == nil || *writer.AgeSeconds != 95 {
		t.Errorf("strategy cache writer = %+v, want 81 listed with a 95-second-old marker", writer)
	}
	// A follower has no round and says nothing about the writer.
	none := endpointFactsSource(cfg, sharing, recorder, nil, nil, func() *fleet.SourceFacts { return nil }, nil, nil,
		func() time.Time { return moment })()
	for _, entry := range none {
		if entry.Role == fleet.EndpointStrategyCache && entry.Writer != nil {
			t.Errorf("a follower reports writer evidence it never read: %+v", entry.Writer)
		}
		// No sink record source: the entry says nothing about readiness
		// rather than inventing an answer.
		if entry.Role == fleet.EndpointOutputKafka && (entry.Ready != nil || entry.Attempts != nil) {
			t.Errorf("output kafka without a sink record reports readiness: %+v", entry)
		}
	}
}

// The composition becomes the fleet's facts as it is, with the change marker
// beside it; a round without a marker has no age.
func TestSourceFactsOfCarriesTheCompositionAndTheMarker(t *testing.T) {
	at := time.Unix(7000, 0)
	composition := &controlplane.CatalogComposition{
		PlansTotal: 5, RevisionedPlans: 2,
		Objects: map[controlplane.Disposition]int{controlplane.DispositionAccepted: 0, controlplane.DispositionSourceIncomplete: 2},
		WithheldObjects: []controlplane.ObjectDisposition{
			{SourceID: "9", Scope: "STRATEGY", Disposition: controlplane.DispositionSourceIncomplete, Reason: "SOURCE_IDENTITY_UNAVAILABLE"},
			{SourceID: "12", Scope: "LEVEL", LevelID: 3, Disposition: controlplane.DispositionSourceIncomplete, Reason: "SOURCE_OBJECT_INCOMPLETE", FieldPath: "items"},
		},
	}
	facts := sourceFactsOf(phaseTwoControlRefreshResult{Composition: composition, ChangeSignalPresent: true, ChangeSignalAgeSeconds: 42}, at)
	if !facts.At.Equal(at) || facts.Listed != 2 || facts.Accepted != 0 || !facts.Blocked() {
		t.Fatalf("facts = %+v", facts)
	}
	if !facts.ChangeSignalPresent || facts.ChangeSignalAgeSeconds == nil || *facts.ChangeSignalAgeSeconds != 42 {
		t.Errorf("marker = %v/%v, want present at 42", facts.ChangeSignalPresent, facts.ChangeSignalAgeSeconds)
	}
	// The Plan counts travel as the composition counted them, and say they
	// were reported: a round from this build is not read as zero Plans.
	if facts.Plans != 5 || facts.RevisionedPlans != 2 || !facts.PlansKnown {
		t.Errorf("plans = %d revisioned %d known %t, want 5, 2, true", facts.Plans, facts.RevisionedPlans, facts.PlansKnown)
	}
	if len(facts.Withheld) != 2 || facts.Withheld[0].Samples[0].StrategyID != "9" ||
		facts.Withheld[1].Samples[0].LevelID != 3 || facts.Withheld[1].Samples[0].FieldPath != "items" {
		t.Errorf("withheld = %+v", facts.Withheld)
	}
	without := sourceFactsOf(phaseTwoControlRefreshResult{Composition: composition}, at)
	if without.ChangeSignalPresent || without.ChangeSignalAgeSeconds != nil {
		t.Errorf("a round without a marker carries an age: %+v", without)
	}
}

// The readiness the fleet snapshot carries is the readiness endpoint's, from
// the same tracker, bit for bit: a process that is not ready because its
// output is not open publishes exactly that, and the fleet cannot say ready
// of a replica whose probe says no. No health source publishes no fact.
func TestReadinessFactsAreTheProbesOwn(t *testing.T) {
	if readinessFactsSource(nil) != nil {
		t.Fatal("a publisher without a health source has a readiness fact to publish")
	}
	health := newPhaseTwoApplicationHealth()
	health.Update(phaseTwoReadiness{
		State: observability.HealthNotReady, Reasons: []observability.ReasonCode{observability.ReasonCode("KAFKA_UNAVAILABLE")},
		SnapshotReady: true, AssignmentReady: true, RuntimeStateReady: true, OutputSinkReady: false,
	})
	probe := health.HealthSnapshot()
	facts := readinessFactsSource(health)()
	if facts.Ready != probe.Ready || facts.State != string(probe.State) ||
		facts.ConfigLoaded != probe.ConfigLoaded || facts.SchemaReady != probe.SchemaReady ||
		facts.AssignmentReady != probe.AssignmentReady || facts.RuntimeStateReady != probe.RuntimeStateReady ||
		facts.OutputSinkReady != probe.OutputSinkReady || facts.SnapshotReady != probe.SnapshotReady ||
		facts.Draining != probe.Draining {
		t.Fatalf("fleet readiness = %+v, probe = %+v: the two disagree about one process", facts, probe)
	}
	if facts.Ready || facts.OutputSinkReady || !facts.RuntimeStateReady || len(facts.Reasons) != 1 || facts.Reasons[0] != "KAFKA_UNAVAILABLE" {
		t.Fatalf("fleet readiness = %+v, want not ready on the output bit with the probe's one reason", facts)
	}
	// The sink opens: both say ready, and the reason is gone from both.
	health.Update(phaseTwoReadiness{State: observability.HealthReady, SnapshotReady: true, AssignmentReady: true,
		RuntimeStateReady: true, OutputSinkReady: true})
	probe, facts = health.HealthSnapshot(), readinessFactsSource(health)()
	if !facts.Ready || !probe.Ready || facts.OutputSinkReady != probe.OutputSinkReady || len(facts.Reasons) != len(probe.Reasons) {
		t.Fatalf("after the sink opened: fleet %+v, probe %+v", facts, probe)
	}
	// And the publisher puts it on the snapshot as given.
	publisher := fleetPublisher{
		tracker: fleet.NewTracker(nil, "replica-1", time.Now), replica: "replica-1", now: time.Now,
		owned:     func() []execution.QueryGroupIdentity { return nil },
		readiness: readinessFactsSource(health),
	}
	if snapshot := publisher.snapshot(context.Background()); snapshot.Readiness == nil || !snapshot.Readiness.Ready {
		t.Fatalf("snapshot readiness = %+v, want the probe's ready", snapshot.Readiness)
	}
	publisher.readiness = nil
	if snapshot := publisher.snapshot(context.Background()); snapshot.Readiness != nil {
		t.Fatalf("a publisher without a readiness source published %+v", snapshot.Readiness)
	}
}

// The protocol choice the fleet snapshot carries is the one the reconciler is
// configured with -- the same accessor -- and says whether the deployment
// spelled it. An empty configuration is auto by default and is published as
// the word auto, not as an empty word: the empty word is what an older build
// says by saying nothing, and the two must not read alike.
func TestTheFleetPublishesTheOutputProtocolTheReconcilerWasConfiguredWith(t *testing.T) {
	for name, test := range map[string]struct {
		configured string
		want       fleet.OutputProtocolFacts
	}{
		"unset is auto by default":   {configured: "", want: fleet.OutputProtocolFacts{Configured: "auto", Explicit: false}},
		"auto spelled is explicit":   {configured: "auto", want: fleet.OutputProtocolFacts{Configured: "auto", Explicit: true}},
		"native spelled is explicit": {configured: "native", want: fleet.OutputProtocolFacts{Configured: "native", Explicit: true}},
	} {
		name, test := name, test
		t.Run(name, func(t *testing.T) {
			var cfg config.Config
			cfg.PhaseTwo.Output.Protocol = test.configured
			facts := fleetOutputProtocolFacts(cfg)
			if facts == nil || *facts != test.want {
				t.Fatalf("fleet protocol facts = %+v, want %+v", facts, test.want)
			}
			// The same word the reconciler is given, so the two cannot drift.
			if facts.Configured != cfg.OutputProtocol() {
				t.Fatalf("fleet publishes %q, the reconciler is configured with %q", facts.Configured, cfg.OutputProtocol())
			}
			publisher := fleetPublisher{
				tracker: fleet.NewTracker(nil, "replica-1", time.Now), replica: "replica-1", now: time.Now,
				owned:          func() []execution.QueryGroupIdentity { return nil },
				outputProtocol: facts,
			}
			snapshot := publisher.snapshot(context.Background())
			if snapshot.OutputProtocol == nil || *snapshot.OutputProtocol != test.want {
				t.Fatalf("snapshot protocol = %+v, want %+v", snapshot.OutputProtocol, test.want)
			}
			// A copy, not the publisher's pointer.
			if snapshot.OutputProtocol == publisher.outputProtocol {
				t.Fatal("the snapshot aliases the publisher's protocol facts")
			}
		})
	}
	publisher := fleetPublisher{
		tracker: fleet.NewTracker(nil, "replica-1", time.Now), replica: "replica-1", now: time.Now,
		owned: func() []execution.QueryGroupIdentity { return nil },
	}
	if snapshot := publisher.snapshot(context.Background()); snapshot.OutputProtocol != nil {
		t.Fatalf("a publisher without a protocol published %+v", snapshot.OutputProtocol)
	}
}

// The output role's protocol checks are decided by what the brokers answered,
// not by the configuration. The configuration's own check is that its
// version parses; the other two are present and failing until the sink has
// asked, and thereafter read the answers: the live shape -- three brokers
// that accept Produce up to v2 -- reads as a client speaking 0.10.2.0 that
// every broker accepts and that cannot carry the record header, naming each
// broker; brokers that take v3 read as headers supported; a broker that did
// not answer fails both checks by name.
func TestTheOutputRoleReadsTheBrokersAnswerNotTheConfiguration(t *testing.T) {
	cfg := config.Default()
	cfg.Kafka.Brokers = []string{"kafka-0:9092"}
	entry := outputKafkaEndpoint(cfg)
	if entry.Role != fleet.EndpointOutputKafka || entry.ProtocolVersion != cfg.Kafka.BrokerVersion {
		t.Fatalf("entry = %+v, want the output role at the configured floor", entry)
	}
	if len(entry.Checks) != 1 || entry.Checks[0].Name != fleet.EndpointCheckBrokerVersion || !entry.Checks[0].OK {
		t.Fatalf("static checks = %+v, want only the configuration's own, passing", entry.Checks)
	}
	if entry.HeadersSupported != nil || entry.NegotiatedVersion != "" || entry.ProduceVersion != nil {
		t.Fatalf("static entry claims to know what the brokers speak: %+v", entry)
	}
	bad := cfg
	bad.Kafka.BrokerVersion = "not-a-version"
	if checks := outputKafkaEndpoint(bad).Checks; len(checks) != 1 || checks[0].OK || !strings.Contains(checks[0].Detail, "does not parse") {
		t.Fatalf("a version that does not parse: checks = %+v", checks)
	}

	checksOf := func(entry fleet.Endpoint) map[string]fleet.EndpointCheck {
		checks := map[string]fleet.EndpointCheck{}
		for _, check := range entry.Checks {
			if _, twice := checks[check.Name]; twice {
				t.Fatalf("check %q written twice: %+v", check.Name, entry.Checks)
			}
			checks[check.Name] = check
		}
		for _, check := range entry.Checks {
			known := false
			for _, name := range fleet.EndpointCheckNames {
				known = known || name == check.Name
			}
			if !known {
				t.Fatalf("check %q is not in EndpointCheckNames", check.Name)
			}
		}
		return checks
	}

	// Nobody asked yet: both rows there and failing, saying so.
	before := entry
	outputProtocolFacts(&before, nil)
	checks := checksOf(before)
	if record := checks[fleet.EndpointCheckRecordHeaders]; record.OK || !strings.Contains(record.Detail, "not been asked") {
		t.Fatalf("record_headers before asking = %+v", record)
	}
	if produce := checks[fleet.EndpointCheckProduceVersion]; produce.OK || !strings.Contains(produce.Detail, "not asked yet") {
		t.Fatalf("produce_version_accepted before asking = %+v", produce)
	}
	if before.HeadersSupported != nil {
		t.Fatalf("headers_supported before asking = %v, want absent", *before.HeadersSupported)
	}

	// The live shape: three brokers at 0.10.2, the client came down to it.
	old := func(addr string) enginekafka.BrokerProtocol {
		return enginekafka.BrokerProtocol{Address: addr, ID: -1, Answered: true, ProduceMinVersion: 0, ProduceMaxVersion: 2}
	}
	capped := &enginekafka.ProtocolNegotiation{
		Configured: "0.10.2.0", Wanted: "0.11.0.0", Negotiated: "0.10.2.0", ProduceVersion: 2, WantedProduceVersion: 3,
		HeadersSupported: false, Brokers: []enginekafka.BrokerProtocol{old("b1:9092"), old("b2:9092"), old("b3:9092")},
		Reason: enginekafka.ProtocolReasonUnsupportedByBroker,
	}
	live := entry
	outputProtocolFacts(&live, capped)
	if live.NegotiatedVersion != "0.10.2.0" || live.ProduceVersion == nil || *live.ProduceVersion != 2 ||
		live.HeadersSupported == nil || *live.HeadersSupported || len(live.Brokers) != 3 || live.Brokers[2].Address != "b3:9092" || live.Brokers[2].ProduceMaxVersion != 2 {
		t.Fatalf("live entry = %+v, want negotiated 0.10.2.0, Produce v2, no headers, three brokers", live)
	}
	checks = checksOf(live)
	record := checks[fleet.EndpointCheckRecordHeaders]
	if record.OK {
		t.Fatalf("record_headers on capped brokers = %+v, want not ok", record)
	}
	for _, fragment := range []string{"b1:9092", "b2:9092", "b3:9092", "v0..v2", "v3 (0.11.0.0)", "speaks 0.10.2.0", "no native event"} {
		if !strings.Contains(record.Detail, fragment) {
			t.Fatalf("record_headers detail = %q, want it to say %q", record.Detail, fragment)
		}
	}
	if produce := checks[fleet.EndpointCheckProduceVersion]; !produce.OK || produce.Detail != "" {
		t.Fatalf("produce_version_accepted = %+v, want ok: every broker accepts the v2 the client sends", produce)
	}
	if entry.Checks[0].Name != fleet.EndpointCheckBrokerVersion || len(entry.Checks) != 1 {
		t.Fatalf("the static entry was written through: %+v", entry.Checks)
	}

	// Brokers that take the header.
	modern := &enginekafka.ProtocolNegotiation{
		Configured: "0.10.2.0", Wanted: "0.11.0.0", Negotiated: "0.11.0.0", ProduceVersion: 3, WantedProduceVersion: 3, HeadersSupported: true,
		Brokers: []enginekafka.BrokerProtocol{{Address: "b1:9092", ID: -1, Answered: true, ProduceMinVersion: 0, ProduceMaxVersion: 7}},
	}
	fine := entry
	outputProtocolFacts(&fine, modern)
	checks = checksOf(fine)
	if !checks[fleet.EndpointCheckRecordHeaders].OK || !checks[fleet.EndpointCheckProduceVersion].OK ||
		fine.HeadersSupported == nil || !*fine.HeadersSupported || fine.NegotiatedVersion != "0.11.0.0" {
		t.Fatalf("modern entry = %+v checks %+v, want both ok", fine, fine.Checks)
	}

	// One broker did not answer: the partial answers of a failed open.
	partial := &enginekafka.ProtocolNegotiation{
		Configured: "0.10.2.0", Wanted: "0.11.0.0", Negotiated: "0.10.2.0", ProduceVersion: 2, WantedProduceVersion: 3,
		Brokers: []enginekafka.BrokerProtocol{old("b1:9092"), {Address: "b2:9092", ID: -1, ProduceMinVersion: -1, ProduceMaxVersion: -1, Error: "EOF"}},
	}
	silent := entry
	outputProtocolFacts(&silent, partial)
	checks = checksOf(silent)
	if produce := checks[fleet.EndpointCheckProduceVersion]; produce.OK || !strings.Contains(produce.Detail, "b2:9092") || !strings.Contains(produce.Detail, "did not answer") || strings.Contains(produce.Detail, "b1:9092") {
		t.Fatalf("produce_version_accepted with a silent broker = %+v, want it named alone", produce)
	}
	if record := checks[fleet.EndpointCheckRecordHeaders]; record.OK || !strings.Contains(record.Detail, "b2:9092 did not answer (EOF)") {
		t.Fatalf("record_headers with a silent broker = %+v", record)
	}
}

// The dependency list carries the brokers' answer from the sink's state, and
// the two protocol rows are on the output entry whether or not a sink was
// wired: absent, they read as passed.
func TestEndpointFactsCarryTheBrokersAnswerFromTheSink(t *testing.T) {
	cfg := config.Default()
	cfg.Kafka.Brokers = []string{"kafka-0:9092"}
	negotiation := &enginekafka.ProtocolNegotiation{
		Configured: "0.10.2.0", Wanted: "0.11.0.0", Negotiated: "0.10.2.0", ProduceVersion: 2, WantedProduceVersion: 3,
		Brokers: []enginekafka.BrokerProtocol{{Address: "kafka-0:9092", ID: -1, Answered: true, ProduceMinVersion: 0, ProduceMaxVersion: 2}},
		Reason:  enginekafka.ProtocolReasonUnsupportedByBroker,
	}
	now := func() time.Time { return time.Unix(1_700_000_000, 0) }
	for name, state := range map[string]func() outputSinkState{
		"no sink wired": nil,
		"sink open on the negotiated version": func() outputSinkState {
			return outputSinkState{Ready: true, Since: now().Add(-time.Minute), Attempts: 1, Protocol: negotiation}
		},
		"sink not open, nobody asked": func() outputSinkState {
			return outputSinkState{Attempts: 2, LastFailure: "dial tcp: refused", LastFailureAt: now()}
		},
	} {
		endpoints := endpointFactsSource(cfg, endpointSharing{}, nil, nil, nil, func() *fleet.SourceFacts { return nil }, state, nil, now)()
		var output *fleet.Endpoint
		for index := range endpoints {
			if endpoints[index].Role == fleet.EndpointOutputKafka {
				output = &endpoints[index]
			}
		}
		if output == nil {
			t.Fatalf("%s: no output entry in %+v", name, endpoints)
		}
		names := map[string]bool{}
		for _, check := range output.Checks {
			names[check.Name] = check.OK
		}
		if _, present := names[fleet.EndpointCheckRecordHeaders]; !present {
			t.Fatalf("%s: record_headers row absent: %+v", name, output.Checks)
		}
		if _, present := names[fleet.EndpointCheckProduceVersion]; !present {
			t.Fatalf("%s: produce_version_accepted row absent: %+v", name, output.Checks)
		}
		asked := state != nil && state().Protocol != nil
		if names[fleet.EndpointCheckProduceVersion] != asked || (output.NegotiatedVersion != "") != asked {
			t.Fatalf("%s: produce_version_accepted ok=%t negotiated=%q, want asked=%t", name, names[fleet.EndpointCheckProduceVersion], output.NegotiatedVersion, asked)
		}
	}
}

// A NOSCRIPT reply on the shared connection is on every role that reads it,
// as a script cache miss with its own count and clock, and the failure
// column stays empty: a live dependency table carried "NOSCRIPT No matching
// script" as the last failure of three roles for as long as nothing else
// failed. A connection that never saw one carries neither field.
func TestEndpointFactsKeepScriptCacheMissesOutOfTheFailureColumn(t *testing.T) {
	cfg := config.Default()
	cfg.Redis.Address = "redis:6379"
	recorder := metric.NewRecorder(metric.BuildInfo{})
	hook := recorder.RedisHook("source")
	ctx, _ := hook.BeforeProcess(context.Background(), nil)
	miss := redis.NewCmd(ctx, "evalsha", "deadbeef", 1, "k")
	miss.SetErr(errors.New("NOSCRIPT No matching script. Please use EVAL."))
	_ = hook.AfterProcess(ctx, miss)
	sharing := endpointSharing{runtimeIsSource: true, cmdbSharedWith: fleet.EndpointStrategyCache}
	// The hook stamps with the wall clock, so the facts read the same clock
	// and the age is asserted small rather than exact.
	facts := endpointFactsSource(cfg, sharing, recorder, nil, nil, func() *fleet.SourceFacts { return nil }, nil, nil, time.Now)()
	for _, entry := range facts {
		if entry.Kind != "redis" || !entry.Configured {
			continue
		}
		if entry.ScriptCacheMisses != 1 || entry.LastScriptCacheMissAgeSeconds == nil ||
			*entry.LastScriptCacheMissAgeSeconds < 0 || *entry.LastScriptCacheMissAgeSeconds > 5 {
			t.Errorf("%s = misses %d age %v, want 1 miss a moment ago", entry.Role, entry.ScriptCacheMisses, entry.LastScriptCacheMissAgeSeconds)
		}
		if entry.LastFailure != "" || entry.LastFailureAgeSeconds != nil {
			t.Errorf("%s carries the NOSCRIPT reply as a failure: %q", entry.Role, entry.LastFailure)
		}
	}
	// A connection with no miss: neither field, not zero.
	clean := metric.NewRecorder(metric.BuildInfo{})
	cleanHook := clean.RedisHook("source")
	cctx, _ := cleanHook.BeforeProcess(context.Background(), nil)
	_ = cleanHook.AfterProcess(cctx, redis.NewStringCmd(cctx, "get", "a"))
	for _, entry := range endpointFactsSource(cfg, sharing, clean, nil, nil, func() *fleet.SourceFacts { return nil }, nil, nil, time.Now)() {
		if entry.Kind == "redis" && entry.Configured && (entry.ScriptCacheMisses != 0 || entry.LastScriptCacheMissAgeSeconds != nil) {
			t.Errorf("%s reports script cache misses nobody recorded: %+v", entry.Role, entry)
		}
	}
}

// The consumer's open alert publication has a row of its own: the state
// Redis under the contract's fixed prefix, shared with the state role, and
// the reader's account of the publisher beside it -- present once read,
// the members as the count, the publisher's heartbeat as the age, and the
// mode or why it is unavailable as the state, with the full account on the
// row. A deployment whose dependency list named every Redis and Kafka it
// touched had no row for this, and whether the recovery gate was working on
// the consumer's word or its own could not be read anywhere. No source --
// a publisher built without the copy -- publishes the coordinates and no
// account, not an invented one.
func TestTheOpenAlertPublicationHasADependencyRowWithTheReadersAccount(t *testing.T) {
	cfg := config.Default()
	cfg.Redis.Address = "redis:6379"
	cfg.Redis.DB = 8
	cfg.Redis.StatePrefix = "alarmd:phase2:g2:runtime:v1"
	sharing := endpointSharing{runtimeIsSource: false}
	heartbeat, members := 42.0, 517
	account := &fleet.OpenAlertSetFacts{Mode: "self_maintained", Available: false, UnavailableReason: "heartbeat_stale",
		HeartbeatAgeSeconds: &heartbeat, CycleSeconds: 60, FingerprintVersion: "md5_v1", ReaderFingerprintVersion: "md5_v1",
		TrackedSets: 6, LoadedSets: 6, Members: members, Lookups: map[string]uint64{"authoritative_member": 3, "self_maintained": 1}}
	loaded := 300.0
	account.AuthoritativeAgeSeconds = &loaded
	facts := endpointFactsSource(cfg, sharing, nil, nil, nil, func() *fleet.SourceFacts { return nil }, nil,
		func() *fleet.OpenAlertSetFacts { return account }, time.Now)()
	var row *fleet.Endpoint
	for index := range facts {
		if facts[index].Role == fleet.EndpointOpenAlertSet {
			row = &facts[index]
		}
	}
	if row == nil {
		t.Fatalf("no row for %s among %d endpoints", fleet.EndpointOpenAlertSet, len(facts))
	}
	if row.Kind != "redis" || !row.Configured || row.Address != "redis:6379" || row.DB == nil || *row.DB != 8 ||
		row.Prefix != cfg.PhaseTwo.Linkd.Prefix() || row.SharedWith != fleet.EndpointStateRedis {
		t.Fatalf("row = %+v, want the state Redis, db 8, the contract's prefix %q, shared with the state role", row, cfg.PhaseTwo.Linkd.Prefix())
	}
	if row.Prefix == cfg.Redis.StatePrefix || strings.HasPrefix(row.Prefix, cfg.Redis.StatePrefix) {
		t.Fatalf("prefix %q is derived from the state prefix; the contract's prefix is fixed so the writer need not learn this deployment's configuration", row.Prefix)
	}
	if row.Writer == nil || !row.Writer.Present || row.Writer.Count != members || row.Writer.AgeSeconds == nil || *row.Writer.AgeSeconds != heartbeat ||
		row.Writer.State != "self_maintained:heartbeat_stale" {
		t.Fatalf("writer = %+v, want present, %d members, the publisher's heartbeat %.0f s old, the mode with why it is unavailable", row.Writer, members, heartbeat)
	}
	if row.OpenAlertSet != account {
		t.Fatalf("the full account is not on the row: %+v", row.OpenAlertSet)
	}
	never := &fleet.OpenAlertSetFacts{Mode: "never_loaded", ReaderFingerprintVersion: "md5_v1", Lookups: map[string]uint64{"passed_through": 9}}
	for _, entry := range endpointFactsSource(cfg, sharing, nil, nil, nil, func() *fleet.SourceFacts { return nil }, nil,
		func() *fleet.OpenAlertSetFacts { return never }, time.Now)() {
		if entry.Role == fleet.EndpointOpenAlertSet && (entry.Writer == nil || entry.Writer.Present || entry.Writer.AgeSeconds != nil || entry.Writer.State != "never_loaded") {
			t.Fatalf("a copy that never loaded = %+v, want absent, no age, the mode as the state", entry.Writer)
		}
	}
	for _, entry := range endpointFactsSource(cfg, sharing, nil, nil, nil, func() *fleet.SourceFacts { return nil }, nil, nil, time.Now)() {
		if entry.Role == fleet.EndpointOpenAlertSet && (entry.Writer != nil || entry.OpenAlertSet != nil || !entry.Configured) {
			t.Fatalf("without a source the row = %+v, want the coordinates and no account", entry)
		}
	}
}

func TestOpenAlertIndexReadDoesNotBecomeWriterHeartbeat(t *testing.T) {
	cfg := config.Default()
	age := 2.0
	account := &fleet.OpenAlertSetFacts{IndexProtocol: true, IndexReadAgeSeconds: &age, SubscriptionReady: true, Members: 3}
	rows := endpointFactsSource(cfg, endpointSharing{}, nil, nil, nil, func() *fleet.SourceFacts { return nil }, nil, func() *fleet.OpenAlertSetFacts { return account }, time.Now)()
	for _, row := range rows {
		if row.Role == fleet.EndpointOpenAlertSet {
			if row.Writer != nil || row.OpenAlertSet != account {
				t.Fatalf("reader evidence claimed a writer: %+v", row)
			}
			return
		}
	}
	t.Fatal("missing open alert endpoint")
}
