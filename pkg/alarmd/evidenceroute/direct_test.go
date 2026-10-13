// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package evidenceroute

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obchannel"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/roles"
)

type roleFixtureStore struct {
	*fixtureStore
	instances      map[string]ownership.InstanceRegistration
	instanceErrors map[string]error
	instanceReads  map[string]int
	changeOnRead   func(string, int, *ownership.InstanceRegistration)
	workerReads    int
}

func (s *roleFixtureStore) ReadInstance(_ context.Context, id string) (ownership.InstanceRegistration, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.instanceReads[id]++
	instance, found := s.instances[id]
	if s.changeOnRead != nil && found {
		s.changeOnRead(id, s.instanceReads[id], &instance)
		s.instances[id] = instance
	}
	return instance, found, s.instanceErrors[id]
}

func (s *roleFixtureStore) ReadWorker(ctx context.Context, id string) (ownership.WorkerRegistration, bool, error) {
	s.mu.Lock()
	s.workerReads++
	s.mu.Unlock()
	return s.fixtureStore.ReadWorker(ctx, id)
}

func roleFixture(t *testing.T) (map[string]*fixtureNode, *roleFixtureStore) {
	t.Helper()
	nodes, legacy := fixture(t)
	store := &roleFixtureStore{fixtureStore: legacy, instances: map[string]ownership.InstanceRegistration{}, instanceErrors: map[string]error{}, instanceReads: map[string]int{}}
	for id, role := range map[string]roles.Role{"entry": roles.Channel, "leader": roles.Control, "worker": roles.Worker} {
		worker := legacy.workers[id]
		store.instances[id] = ownership.InstanceRegistration{InstanceID: id, Roles: roles.Set{role}, Endpoint: worker.Endpoint,
			Incarnation: id + "-boot", InternalToken: worker.StreamToken, ExpiresAt: worker.ExpiresAt}
		nodes[id].router.options.Store = store
		nodes[id].router.options.AllowOperation = nodes[id].channel.AllowsTargetedOperation
	}
	return nodes, store
}

func TestChannelRoutesWorkerEvidenceWithoutControlLeader(t *testing.T) {
	nodes, store := roleFixture(t)
	store.leader.OwnerID = ""
	result := invoke(t, nodes["entry"], "runtime.get", obchannel.Params{"replica": "worker"})
	if result.Status != "ok" || result.Meta.AnsweredBy != "worker" || strings.Join(result.Meta.Via, ",") != "entry" || store.leaderReads != 0 || nodes["leader"].rpcCalls.Load() != 0 {
		t.Fatalf("direct Worker read depends on control: %+v", result)
	}
	if store.workerReads != 0 {
		t.Fatal("new identities fell back to Worker registration")
	}
	result = invoke(t, nodes["entry"], "runtime.get", obchannel.Params{"owner_query_group": "query-group"})
	if result.Status != "ok" || result.Meta.Owner == nil || result.Meta.Owner.OwnerEpoch != 19 || store.leaderReads != 0 {
		t.Fatalf("direct owner read lost owner facts: %+v", result)
	}
}

func TestChannelAndAuthlessWorkerUseOperationContractAcrossCatalogs(t *testing.T) {
	nodes, _ := roleFixture(t)
	worker := nodes["worker"]
	op := obchannel.Operation{ID: "runtime.get", Summary: "Read process", EvidenceScope: "process", Targetable: true,
		Run: func(context.Context, obchannel.Params) obchannel.Outcome {
			worker.runs.Add(1)
			return obchannel.Outcome{Complete: true}
		}}
	executor, err := obchannel.NewEvidenceExecutor(obchannel.ExecutorOptions{EnvironmentID: "fixture", Replica: "worker", Incarnation: "worker-boot", Operations: []obchannel.Operation{op}})
	if err != nil {
		t.Fatal(err)
	}
	worker.router.options.Execute, worker.router.options.AllowOperation = executor.ExecuteEvidence, executor.AllowsTargetedOperation
	result := invoke(t, nodes["entry"], "runtime.get", obchannel.Params{"replica": "worker"})
	if executor.CatalogRevision() == nodes["entry"].channel.CatalogRevision() {
		t.Fatal("fixture catalogs must differ")
	}
	if result.Status != "ok" || worker.runs.Load() != 1 || result.Meta.Revision != nodes["entry"].channel.CatalogRevision() || result.Meta.Session == nil || worker.auth.admissions.Load() != 0 {
		t.Fatalf("different catalog or authless Worker contract failed: %+v", result)
	}
}

func TestNewRegistrationFailureNeverFallsBack(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*roleFixtureStore)
	}{
		{"unreadable", func(s *roleFixtureStore) { s.instanceErrors["worker"] = errors.New("unreadable registry") }},
		{"malformed", func(s *roleFixtureStore) {
			instance := s.instances["worker"]
			instance.Roles = roles.Set{}
			s.instances["worker"] = instance
		}},
		{"expired", func(s *roleFixtureStore) {
			instance := s.instances["worker"]
			instance.ExpiresAt = time.Now().Add(-time.Minute)
			s.instances["worker"] = instance
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			nodes, store := roleFixture(t)
			test.change(store)
			result := invoke(t, nodes["entry"], "runtime.get", obchannel.Params{"replica": "worker"})
			if result.Error == nil || result.Error.Code != "target_unavailable" || store.workerReads != 0 || nodes["worker"].runs.Load() != 0 || nodes["leader"].rpcCalls.Load() != 0 {
				t.Fatalf("bad new identity fell back or executed: %+v", result)
			}
		})
	}
}

func TestAbsentInstanceUsesLegacyLeaderDispatch(t *testing.T) {
	nodes, store := roleFixture(t)
	delete(store.instances, "worker")
	result := invoke(t, nodes["entry"], "runtime.get", obchannel.Params{"replica": "worker"})
	if result.Status != "ok" || strings.Join(result.Meta.Via, ",") != "entry,leader" || nodes["leader"].rpcCalls.Load() != 1 || store.workerReads == 0 {
		t.Fatalf("legacy target contract lost: %+v", result)
	}
}

func TestDirectAdmissionRechecksCallerRoleIncarnationAndOwnerEpoch(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(string, int, *ownership.InstanceRegistration)
		owner  bool
		code   string
	}{
		{"caller_role", func(id string, read int, instance *ownership.InstanceRegistration) {
			if id == "entry" && read == 2 {
				instance.Roles = roles.Set{roles.Worker}
			}
		}, false, "worker_identity_rejected"},
		{"caller_incarnation", func(id string, read int, instance *ownership.InstanceRegistration) {
			if id == "entry" && read == 2 {
				instance.Incarnation = "new-entry-boot"
			}
		}, false, "worker_identity_rejected"},
		{"target_incarnation", func(id string, read int, instance *ownership.InstanceRegistration) {
			if id == "worker" && read == 2 {
				instance.Incarnation = "new-worker-boot"
			}
		}, false, "target_changed"},
		{"owner_epoch", nil, true, "target_changed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			nodes, store := roleFixture(t)
			store.changeOnRead = test.change
			params := obchannel.Params{"replica": "worker"}
			if test.owner {
				store.moveOnRead = 2
				params = obchannel.Params{"owner_query_group": "query-group"}
			}
			result := invoke(t, nodes["entry"], "runtime.get", params)
			if result.Error == nil || result.Error.Code != test.code || nodes["worker"].runs.Load() != 0 {
				t.Fatalf("changed identity or lease executed: %+v", result)
			}
		})
	}
}

func TestDirectControlLeaderRechecksTermAndKeepsResolvedLease(t *testing.T) {
	nodes, store := roleFixture(t)
	store.moveLeaderOnRead = 2
	result := invoke(t, nodes["entry"], "runtime.get", obchannel.Params{"control_leader": true})
	if result.Error == nil || result.Error.Code != "control_changed" || result.Meta.ControlLeader == nil || result.Meta.ControlLeader.OwnerEpoch != 7 || nodes["leader"].runs.Load() != 0 {
		t.Fatalf("changed control term executed or lost lease: %+v", result)
	}
}

// A Worker that holds the control lease as the fallback candidate is the
// Control Leader a direct read is routed to; a Worker that does not stand as a
// candidate is not, whatever the lease names.
func TestADirectControlLeaderReadReachesAFallbackLeader(t *testing.T) {
	for _, test := range []struct {
		name      string
		candidacy string
		code      string
	}{
		{name: "fallback candidate", candidacy: ownership.ControlCandidacyFallback},
		{name: "no candidacy", code: "target_role_mismatch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			nodes, store := roleFixture(t)
			leader := store.instances["leader"]
			leader.Roles, leader.ControlCandidacy = roles.Set{roles.Worker}, test.candidacy
			store.instances["leader"] = leader
			result := invoke(t, nodes["entry"], "runtime.get", obchannel.Params{"control_leader": true})
			if test.code == "" {
				if result.Status != "ok" || result.Meta.AnsweredBy != "leader" || nodes["leader"].runs.Load() != 1 {
					t.Fatalf("control Leader read of a fallback Leader = %+v", result)
				}
				return
			}
			if result.Error == nil || result.Error.Code != test.code || nodes["leader"].runs.Load() != 0 {
				t.Fatalf("control Leader read of a Worker that is no candidate = %+v", result)
			}
		})
	}
}
