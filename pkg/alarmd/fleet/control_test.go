// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package fleet

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/roles"
)

func controlFixture(at time.Time) ControlFacts {
	return ControlFacts{Replica: "control-only", Incarnation: "process-control", ControlEpoch: 5, TakenAt: at,
		Source:        &SourceFacts{At: at, Listed: 1, Accepted: 1},
		Activation:    &ActivationFacts{Behind: true, BehindBeyondBound: true},
		ControlSource: &ControlSourceFacts{Role: "leader", Mode: "healthy"},
		Rebalance:     &RebalanceFacts{PlannedAt: at}, AssignmentScope: &AssignmentScopeFacts{At: at},
		AssignmentSweep: &AssignmentSweepFacts{At: at}, LeaderRound: &LeaderRoundFacts{At: at},
		ViewStream: &ViewStreamFacts{Leading: true, ControlEpoch: 5}}
}

func TestControlStoreUsesIndependentNamespaceRetentionAndBudget(t *testing.T) {
	ctx := context.Background()
	client := newFakeRedis()
	store := mustStore(t, client, time.Minute, 4096)
	facts := controlFixture(now)
	if err := store.PublishControl(ctx, facts); err != nil {
		t.Fatal(err)
	}
	got, found, err := store.LoadControl(ctx)
	if err != nil || !found || !reflect.DeepEqual(got, facts) || client.lastTTL != time.Minute {
		t.Fatalf("LoadControl() = (%+v, %v, %v), TTL %s", got, found, err, client.lastTTL)
	}
	if store.controlKey() == store.snapshotKey(facts.Replica) {
		t.Fatal("control and worker keys collide")
	}
	if workers, err := store.Load(ctx, []string{facts.Replica}); err != nil || len(workers) != 0 {
		t.Fatalf("control facts appeared as worker snapshots: (%+v, %v)", workers, err)
	}
	large := facts
	large.ControlSource = &ControlSourceFacts{LastFailure: strings.Repeat("x", 4096)}
	if err := store.PublishControl(ctx, large); ControlPublishResult(err) != "oversized" {
		t.Fatalf("oversized control facts = %v", err)
	}
	invalid := facts
	invalid.Incarnation = ""
	if err := store.PublishControl(ctx, invalid); ControlPublishResult(err) != "invalid" {
		t.Fatalf("control facts without incarnation = %v", err)
	}
	if ControlPublishResult(nil) != "written" || ControlPublishResult(errors.New("redis down")) != "failed" {
		t.Fatal("write results misnamed")
	}
	client.values[store.controlKey()] = "broken-json"
	if _, _, err := store.LoadControl(ctx); err == nil {
		t.Fatal("corrupt control facts accepted")
	}
	client.values[store.controlKey()] = strings.Repeat("x", 4097)
	if _, _, err := store.LoadControl(ctx); err == nil {
		t.Fatal("oversized stored facts accepted")
	}
	delete(client.values, store.controlKey())
	if _, found, err := store.LoadControl(ctx); err != nil || found {
		t.Fatalf("expired/missing control facts = (%v, %v)", found, err)
	}
}

type stubControlFacts struct {
	facts ControlFacts
	term  ControlTerm
	found bool
	err   error
}

func (source stubControlFacts) ControlFacts(context.Context, time.Time) (ControlFactsReading, bool, error) {
	term := source.term
	if term == "" {
		term = ControlTermCurrent
	}
	return ControlFactsReading{Facts: source.facts, Term: term}, source.found, source.err
}

func TestIndependentControlFactsReachViewSummaryAndHealthWithoutCountingAsWorker(t *testing.T) {
	expectations := stubExpectations{expectation: Expectation{QueryGroups: 949, Known: true}}
	workers := healthySnapshots()
	baseline := Aggregate(expectations.expectation, workers, replicas(), now, time.Minute)
	// A former leader can carry newer-looking facts until its Worker snapshot
	// expires. The active control term, rather than that local timestamp, wins.
	workers[0].Source = &SourceFacts{At: now.Add(time.Hour), Listed: 500, Accepted: 0}
	workers[0].Rebalance = &RebalanceFacts{PlannedAt: now.Add(time.Hour)}
	workers[0].ControlSource = &ControlSourceFacts{StaleBeyondBound: true}
	workers[0].ActivationHeader = &ActivationHeaderFacts{MissingSeconds: 100}
	service := mustService(t, expectations, stubRegistry{replicas: replicas()}, stubSnapshots{snapshots: workers})
	facts := controlFixture(now)
	service.SetControlFactsSource(stubControlFacts{facts: facts, found: true})
	view := service.View(context.Background())
	summary, part := service.Summarized(context.Background(), time.Minute)
	health := healthOf(&summary, part, now)
	for _, got := range []View{view, summary} {
		if got.Covered != baseline.Covered || got.Determined != baseline.Determined || got.Unknown != baseline.Unknown ||
			!reflect.DeepEqual(got.Workers, baseline.Workers) || !reflect.DeepEqual(got.Capacity, baseline.Capacity) ||
			len(got.Replicas) != len(baseline.Replicas) || len(got.PerReplica) != len(baseline.PerReplica) {
			t.Fatalf("control changed worker population or capacity: covered %d/%d workers %+v/%+v replicas %v",
				got.Covered, baseline.Covered, got.Workers, baseline.Workers, got.Replicas)
		}
		if got.SourceReplica != facts.Replica || got.Source.Accepted != 1 || got.ActivationReplica != facts.Replica ||
			got.RebalanceReplica != facts.Replica || got.AssignmentScopeReplica != facts.Replica ||
			got.AssignmentSweepReplica != facts.Replica || got.LeaderRoundReplica != facts.Replica || got.ViewStreamReplica != facts.Replica {
			t.Fatalf("independent control facts did not reach aggregate: %+v", got)
		}
		if got.Health != HealthDegraded || len(got.Degradations) != 1 || got.Degradations[0].Kind != DegradationActivationBehind {
			t.Fatalf("health = %s, degradations %+v; want only active control activation", got.Health, got.Degradations)
		}
	}
	if health.SourceReplica != facts.Replica || health.ActivationReplica != facts.Replica ||
		health.LeaderRoundReplica != facts.Replica || health.ViewStreamReplica != facts.Replica || health.Health != view.Health {
		t.Fatalf("health route lost control facts: %+v", health)
	}
	for _, standing := range []*ControlFactsStanding{view.ControlFacts, summary.ControlFacts, health.ControlFacts} {
		if standing == nil || standing.Source != ControlFactsFromRecord || standing.Replica != facts.Replica || standing.Unavailable != "" {
			t.Fatalf("control facts standing = %+v", standing)
		}
	}
}

func TestControlReadFailureAndStalenessFallBackToEmbeddedFacts(t *testing.T) {
	for name, source := range map[string]stubControlFacts{
		"unreadable": {err: errors.New("control registry failed")},
		"stale":      {facts: controlFixture(now.Add(-2 * time.Minute)), found: true},
	} {
		t.Run(name, func(t *testing.T) {
			workers := healthySnapshots()
			workers[0].Source = &SourceFacts{At: now, Accepted: 12, Listed: 12}
			service := mustService(t, stubExpectations{expectation: Expectation{QueryGroups: 949, Known: true}},
				stubRegistry{replicas: replicas()}, stubSnapshots{snapshots: workers})
			service.SetControlFactsSource(source)
			view := service.View(context.Background())
			summary, _ := service.Summarized(context.Background(), time.Minute)
			for _, got := range []View{view, summary} {
				if got.Health != HealthHealthy || hasGap(got, GapControlFactsUnavailable) || got.Source == nil ||
					got.Source.Accepted != 12 || got.SourceReplica != workers[0].Replica || got.Covered != 949 {
					t.Fatalf("fallback = health %s source %+v gaps %+v covered %d", got.Health, got.Source, got.Gaps, got.Covered)
				}
				if got.ControlFacts == nil || got.ControlFacts.Source != ControlFactsFromEmbedded || got.ControlFacts.Unavailable == "" {
					t.Fatalf("control facts standing = %+v", got.ControlFacts)
				}
			}
		})
	}
}

func TestControlFactsUnavailableOnlyWhenNoSourceHasThem(t *testing.T) {
	for name, source := range map[string]stubControlFacts{
		"unreadable": {err: errors.New("control registry failed")},
		"stale":      {facts: controlFixture(now.Add(-2 * time.Minute)), found: true},
	} {
		t.Run(name, func(t *testing.T) {
			// Worker-only snapshots: a follower's own control-source view and
			// activation are not the Leader's facts.
			workers := healthySnapshots()
			workers[0].ControlSource = &ControlSourceFacts{Role: "follower", Mode: "healthy"}
			workers[0].ViewStream = &ViewStreamFacts{Leading: false}
			service := mustService(t, stubExpectations{expectation: Expectation{QueryGroups: 949, Known: true}},
				stubRegistry{replicas: replicas()}, stubSnapshots{snapshots: workers})
			service.SetControlFactsSource(source)
			view := service.View(context.Background())
			summary, _ := service.Summarized(context.Background(), time.Minute)
			for _, got := range []View{view, summary} {
				if got.Health != HealthUnknown || !hasGap(got, GapControlFactsUnavailable) || got.Source != nil ||
					got.ControlFacts == nil || got.ControlFacts.Source != ControlFactsFromNone {
					t.Fatalf("no control facts = health %s gaps %+v standing %+v", got.Health, got.Gaps, got.ControlFacts)
				}
			}
		})
	}
}

func TestPreviousTermRecordStandsInUntilTheNewLeaderWrites(t *testing.T) {
	facts := controlFixture(now.Add(-90 * time.Second))
	facts.Activation = nil
	workers := healthySnapshots()
	service := mustService(t, stubExpectations{expectation: Expectation{QueryGroups: 949, Known: true}},
		stubRegistry{replicas: replicas()}, stubSnapshots{snapshots: workers})
	service.SetControlFactsSource(stubControlFacts{facts: facts, term: ControlTermPrevious, found: true})
	view := service.View(context.Background())
	summary, _ := service.Summarized(context.Background(), time.Minute)
	for _, got := range []View{view, summary} {
		if got.Health != HealthHealthy || hasGap(got, GapControlFactsUnavailable) || got.SourceReplica != facts.Replica ||
			got.ControlFacts == nil || got.ControlFacts.Source != ControlFactsFromPreviousTerm ||
			got.ControlFacts.Replica != facts.Replica || got.ControlFacts.Unavailable == "" {
			t.Fatalf("previous term = health %s gaps %+v standing %+v source replica %s", got.Health, got.Gaps, got.ControlFacts, got.SourceReplica)
		}
	}
	// Embedded Leader facts outrank the previous term's record.
	workers[1].LeaderRound = &LeaderRoundFacts{At: now}
	service = mustService(t, stubExpectations{expectation: Expectation{QueryGroups: 949, Known: true}},
		stubRegistry{replicas: replicas()}, stubSnapshots{snapshots: workers})
	service.SetControlFactsSource(stubControlFacts{facts: facts, term: ControlTermPrevious, found: true})
	if got := service.View(context.Background()); got.ControlFacts == nil || got.ControlFacts.Source != ControlFactsFromEmbedded ||
		got.SourceReplica == facts.Replica {
		t.Fatalf("embedded over previous term = standing %+v source replica %s", got.ControlFacts, got.SourceReplica)
	}
}

func TestIndependentControlDegradationsUseTheSameHealthDecision(t *testing.T) {
	for name, test := range map[string]struct {
		mutate func(*ControlFacts)
		kind   DegradationKind
	}{
		"control source stale":     {mutate: func(facts *ControlFacts) { facts.ControlSource.StaleBeyondBound = true }, kind: DegradationControlSourceStale},
		"activation header absent": {mutate: func(facts *ControlFacts) { facts.ActivationHeader = &ActivationHeaderFacts{MissingSeconds: 100} }, kind: DegradationActivationHeaderMissing},
		"view publish failing":     {mutate: func(facts *ControlFacts) { facts.ViewStream.PublishFailingBeyondBound = true }, kind: DegradationViewPublishFailing},
		"source blocked":           {mutate: func(facts *ControlFacts) { facts.Source.Accepted = 0 }, kind: DegradationSourceBlocked},
	} {
		t.Run(name, func(t *testing.T) {
			facts := controlFixture(now)
			facts.Activation = nil
			test.mutate(&facts)
			service := mustService(t, stubExpectations{expectation: Expectation{Known: true}},
				stubRegistry{replicas: []string{"worker"}}, stubSnapshots{snapshots: []Snapshot{{Replica: "worker", TakenAt: now}}})
			service.SetControlFactsSource(stubControlFacts{facts: facts, found: true})
			view := service.View(context.Background())
			summary, part := service.Summarized(context.Background(), time.Minute)
			health := healthOf(&summary, part, now)
			for _, got := range []View{view, summary} {
				if got.Health != HealthDegraded || len(got.Degradations) != 1 || got.Degradations[0].Kind != test.kind ||
					got.Degradations[0].Replica != facts.Replica || got.Covered != 0 || len(got.Replicas) != 1 {
					t.Fatalf("%s: health %s, degradations %+v, coverage %d", name, got.Health, got.Degradations, got.Covered)
				}
			}
			if health.Health != HealthDegraded || health.Degradations[0].Kind != test.kind {
				t.Fatalf("health route differs: %+v", health)
			}
		})
	}
}

func TestLegacyControlSourceFallbackPreservesEmbeddedFacts(t *testing.T) {
	workers := healthySnapshots()
	workers[0].Source = &SourceFacts{At: now, Accepted: 12, Listed: 12}
	service := mustService(t, stubExpectations{expectation: Expectation{QueryGroups: 949, Known: true}},
		stubRegistry{replicas: replicas()}, stubSnapshots{snapshots: workers})
	service.SetControlFactsSource(stubControlFacts{})
	view := service.View(context.Background())
	if view.Source == nil || view.Source.Accepted != 12 || view.SourceReplica != workers[0].Replica || view.Health != HealthHealthy ||
		view.ControlFacts == nil || view.ControlFacts.Source != ControlFactsFromEmbedded {
		t.Fatalf("legacy fallback = %+v", view)
	}
}

type stubControlRegistry struct {
	leaders  []ownership.QueryGroupOwner
	instance ownership.InstanceRegistration
	present  bool
	err      error
	reads    int
}

func (registry *stubControlRegistry) ReadActiveControlLeader(context.Context) (ownership.QueryGroupOwner, bool, error) {
	if len(registry.leaders) == 0 {
		return ownership.QueryGroupOwner{}, false, registry.err
	}
	index := registry.reads
	registry.reads++
	if index >= len(registry.leaders) {
		index = len(registry.leaders) - 1
	}
	return registry.leaders[index], true, registry.err
}

func (registry *stubControlRegistry) ReadInstance(context.Context, string) (ownership.InstanceRegistration, bool, error) {
	return registry.instance, registry.present, registry.err
}

type stubControlReader struct {
	facts ControlFacts
	found bool
	err   error
}

func (reader stubControlReader) LoadControl(context.Context) (ControlFacts, bool, error) {
	return reader.facts, reader.found, reader.err
}

func TestControlFactsSourceChecksLeaseRoleIncarnationAndTakeover(t *testing.T) {
	for name, test := range map[string]struct {
		mutate func(*stubControlRegistry, *stubControlReader)
		legacy bool
		fails  bool
		term   ControlTerm
	}{
		"current control":      {term: ControlTermCurrent},
		"legacy":               {mutate: func(registry *stubControlRegistry, _ *stubControlReader) { registry.present = false }, legacy: true},
		"no active lease":      {mutate: func(registry *stubControlRegistry, _ *stubControlReader) { registry.leaders = nil }, fails: true},
		"registration expired": {mutate: func(registry *stubControlRegistry, _ *stubControlReader) { registry.instance.ExpiresAt = now }, fails: true},
		"worker role": {mutate: func(registry *stubControlRegistry, _ *stubControlReader) {
			registry.instance.Roles = roles.Set{roles.Worker}
		}, fails: true},
		"worker standing as the fallback candidate": {mutate: func(registry *stubControlRegistry, _ *stubControlReader) {
			registry.instance.Roles = roles.Set{roles.Worker}
			registry.instance.ControlCandidacy = ownership.ControlCandidacyFallback
		}, term: ControlTermCurrent},
		"facts absent":         {mutate: func(_ *stubControlRegistry, reader *stubControlReader) { reader.found = false }, fails: true},
		"previous incarnation": {mutate: func(_ *stubControlRegistry, reader *stubControlReader) { reader.facts.Incarnation = "previous-process" }, fails: true},
		"previous epoch": {mutate: func(_ *stubControlRegistry, reader *stubControlReader) {
			reader.facts.ControlEpoch = 4
			reader.facts.Replica, reader.facts.Incarnation = "former-control", "former-process"
			reader.facts.ViewStream = nil
		}, term: ControlTermPrevious},
		"later epoch than the lease": {mutate: func(_ *stubControlRegistry, reader *stubControlReader) {
			reader.facts.ControlEpoch = 6
			reader.facts.ViewStream = nil
		}, fails: true},
		"previous epoch, takeover during read": {mutate: func(registry *stubControlRegistry, reader *stubControlReader) {
			reader.facts.ControlEpoch = 4
			reader.facts.ViewStream = nil
			registry.leaders = append(registry.leaders, ownership.QueryGroupOwner{OwnerID: "new-control", OwnerEpoch: 6})
		}, fails: true},
		"takeover during read": {mutate: func(registry *stubControlRegistry, _ *stubControlReader) {
			registry.leaders = append(registry.leaders, ownership.QueryGroupOwner{OwnerID: "new-control", OwnerEpoch: 6})
		}, fails: true},
		"read failure": {mutate: func(_ *stubControlRegistry, reader *stubControlReader) { reader.err = errors.New("redis failed") }, fails: true},
	} {
		t.Run(name, func(t *testing.T) {
			facts := controlFixture(now)
			registry := &stubControlRegistry{leaders: []ownership.QueryGroupOwner{{OwnerID: facts.Replica, OwnerEpoch: facts.ControlEpoch}}, present: true,
				instance: ownership.InstanceRegistration{InstanceID: facts.Replica, Roles: roles.Set{roles.Control}, Incarnation: facts.Incarnation, ExpiresAt: now.Add(time.Minute)}}
			reader := stubControlReader{facts: facts, found: true}
			if test.mutate != nil {
				test.mutate(registry, &reader)
			}
			source, err := NewControlFactsSource(registry, reader)
			if err != nil {
				t.Fatal(err)
			}
			got, found, err := source.ControlFacts(context.Background(), now)
			if (err != nil) != test.fails || found == (test.legacy || test.fails) || got.Term != test.term {
				t.Fatalf("ControlFacts() = (%+v, %v, %v)", got, found, err)
			}
		})
	}
}

func TestTheViewSaysWhichClassOfLeaderWroteTheControlRecord(t *testing.T) {
	for _, testCase := range []struct {
		term  ControlTerm
		class string
		from  string
	}{
		{term: ControlTermCurrent, class: ownership.ControlCandidacyFallback, from: ControlFactsFromRecord},
		{term: ControlTermCurrent, class: ownership.ControlCandidacyPreferred, from: ControlFactsFromRecord},
		{term: ControlTermCurrent, class: "", from: ControlFactsFromRecord},
		{term: ControlTermPrevious, class: ownership.ControlCandidacyFallback, from: ControlFactsFromPreviousTerm},
	} {
		facts := controlFixture(now)
		if testCase.term == ControlTermPrevious {
			facts = controlFixture(now.Add(-90 * time.Second))
		}
		facts.Activation = nil
		facts.CandidacyClass = testCase.class
		service := mustService(t, stubExpectations{expectation: Expectation{QueryGroups: 949, Known: true}},
			stubRegistry{replicas: replicas()}, stubSnapshots{snapshots: healthySnapshots()})
		service.SetControlFactsSource(stubControlFacts{facts: facts, term: testCase.term, found: true})
		summary, part := service.Summarized(context.Background(), time.Minute)
		health := healthOf(&summary, part, now)
		if standing := health.ControlFacts; standing == nil || standing.Source != testCase.from || standing.LeaderClass != testCase.class {
			t.Fatalf("%s record of a %q Leader: health control facts = %+v", testCase.term, testCase.class, standing)
		}
	}
}
