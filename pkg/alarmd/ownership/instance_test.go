// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package ownership

import (
	"context"
	"encoding/json"
	"github.com/go-redis/redis/v8"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/roles"
)

func TestInstanceRegistrationIsIndependentOfWorkers(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Millisecond)
	worker := WorkerRegistration{WorkerID: "shared", AssignmentReadiness: WorkerReady, DependencyStatus: DependencyHealthy,
		DeploymentProfile: "standard", CapabilitiesDigest: "legacy", ExecutionContractDigest: "execution-v1", ExpiresAt: at.Add(time.Minute)}
	if err := store.RegisterWorker(ctx, worker); err != nil {
		t.Fatal(err)
	}
	for _, instance := range []InstanceRegistration{
		{InstanceID: "shared", Roles: roles.Set{roles.Control, roles.Worker, roles.Channel}, Endpoint: "127.0.0.1:8080",
			Incarnation: "shared-process", InternalToken: "secret", ExpiresAt: at.Add(time.Minute), RoleStatus: map[roles.Role]string{roles.Control: "READY"}},
		{InstanceID: "control-only", Roles: roles.Set{roles.Control}, Incarnation: "control-process", ExpiresAt: at.Add(time.Minute)},
		{InstanceID: "channel-only", Roles: roles.Set{roles.Channel}, Incarnation: "channel-process", ExpiresAt: at.Add(time.Minute)},
	} {
		if err := store.RegisterInstance(ctx, instance); err != nil {
			t.Fatal(err)
		}
		got, found, err := store.ReadInstance(ctx, instance.InstanceID)
		if err != nil || !found || got.InstanceID != instance.InstanceID || got.Incarnation != instance.Incarnation ||
			got.InternalToken != instance.InternalToken || !got.ExpiresAt.Equal(instance.ExpiresAt) {
			t.Fatalf("ReadInstance() = (%+v, %v, %v)", got, found, err)
		}
		if ttl := store.client.PTTL(ctx, store.instanceKey(instance.InstanceID)).Val(); ttl <= 0 || ttl > time.Minute {
			t.Fatalf("instance TTL = %s", ttl)
		}
	}
	ready, _, err := store.ListReadyWorkers(ctx, at)
	if err != nil || len(ready) != 1 || ready[0].WorkerID != "shared" || ready[0].ExecutionContractDigest != "execution-v1" {
		t.Fatalf("ready workers = (%+v, %v)", ready, err)
	}
	if store.instanceKey("shared") == store.workerKey("shared") {
		t.Fatal("instance and worker namespaces collide")
	}
	got, found, err := store.ReadInstance(ctx, "not-registered")
	if err != nil || found || got.InstanceID != "" {
		t.Fatalf("missing instance = (%+v, %v, %v)", got, found, err)
	}
}

func TestInstanceRegistrationRejectsInvalidAndExpiredWrites(t *testing.T) {
	store := newIntegrationStore(t)
	valid := InstanceRegistration{InstanceID: "control", Roles: roles.Set{roles.Control}, Incarnation: "process", ExpiresAt: time.Now().Add(time.Minute)}
	for _, mutate := range []func(*InstanceRegistration){
		func(instance *InstanceRegistration) { instance.InstanceID = "" },
		func(instance *InstanceRegistration) { instance.Incarnation = "" },
		func(instance *InstanceRegistration) { instance.Roles = nil },
		func(instance *InstanceRegistration) { instance.Roles = roles.Set{"unsupported"} },
		func(instance *InstanceRegistration) { instance.Roles = roles.Set{roles.Control, roles.Control} },
		func(instance *InstanceRegistration) { instance.ExpiresAt = time.Now().Add(-time.Second) },
		func(instance *InstanceRegistration) {
			instance.RoleStatus = map[roles.Role]string{roles.Worker: "READY"}
		},
	} {
		instance := valid
		mutate(&instance)
		if err := store.RegisterInstance(context.Background(), instance); err == nil {
			t.Fatalf("invalid registration accepted: %+v", instance)
		}
	}
}

func TestInstanceReadReturnsExpiryAndRejectsCorruptIdentity(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	instance := InstanceRegistration{InstanceID: "control", Roles: roles.Set{roles.Control}, Incarnation: "process", ExpiresAt: time.Now().Add(-time.Second)}
	payload, _ := json.Marshal(instance)
	if err := store.client.Set(ctx, store.instanceKey(instance.InstanceID), payload, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	got, found, err := store.ReadInstance(ctx, instance.InstanceID)
	if err != nil || !found || got.ExpiresAt.After(time.Now()) {
		t.Fatalf("expired stored record = (%+v, %v, %v)", got, found, err)
	}
	for _, payload := range []string{"broken-json", `{"instance_id":"control"}`} {
		if err := store.client.Set(ctx, store.instanceKey("control"), payload, time.Minute).Err(); err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.ReadInstance(ctx, "control"); err == nil {
			t.Fatal("corrupt record was accepted")
		}
	}
	instance.InstanceID = "different"
	payload, _ = json.Marshal(instance)
	if err := store.client.Set(ctx, store.instanceKey("control"), payload, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ReadInstance(ctx, "control"); err == nil {
		t.Fatal("record under another identity was accepted")
	}
}

func TestControlCandidacyIsCheckedAgainstTheRolesTheRecordDeclares(t *testing.T) {
	at := time.Now().Add(time.Minute)
	for _, accepted := range []InstanceRegistration{
		{InstanceID: "all", Roles: roles.Set{roles.Control, roles.Worker}, Incarnation: "p", ExpiresAt: at,
			ControlCandidacy: ControlCandidacyPreferred, ControlCandidacyReady: true},
		{InstanceID: "control", Roles: roles.Set{roles.Control}, Incarnation: "p", ExpiresAt: at, ControlCandidacy: ControlCandidacyPreferred},
		{InstanceID: "worker", Roles: roles.Set{roles.Worker}, Incarnation: "p", ExpiresAt: at,
			ControlCandidacy: ControlCandidacyFallback, ControlCandidacyReady: true},
		{InstanceID: "old-control", Roles: roles.Set{roles.Control}, Incarnation: "p", ExpiresAt: at},
	} {
		if err := accepted.Validate(); err != nil {
			t.Fatalf("Validate(%+v) = %v", accepted, err)
		}
	}
	for _, refused := range []InstanceRegistration{
		{InstanceID: "worker", Roles: roles.Set{roles.Worker}, Incarnation: "p", ExpiresAt: at, ControlCandidacy: ControlCandidacyPreferred},
		{InstanceID: "control", Roles: roles.Set{roles.Control}, Incarnation: "p", ExpiresAt: at, ControlCandidacy: ControlCandidacyFallback},
		{InstanceID: "channel", Roles: roles.Set{roles.Channel}, Incarnation: "p", ExpiresAt: at, ControlCandidacy: ControlCandidacyFallback},
		{InstanceID: "worker", Roles: roles.Set{roles.Worker}, Incarnation: "p", ExpiresAt: at, ControlCandidacyReady: true},
		{InstanceID: "worker", Roles: roles.Set{roles.Worker}, Incarnation: "p", ExpiresAt: at, ControlCandidacy: "standby"},
	} {
		if err := refused.Validate(); err == nil {
			t.Fatalf("Validate(%+v) accepted a candidacy its roles do not allow", refused)
		}
	}
	old := InstanceRegistration{InstanceID: "old-control", Roles: roles.Set{roles.Control}, Incarnation: "p", ExpiresAt: at}
	if old.CandidacyClass() != ControlCandidacyPreferred || !old.ControlCapable() {
		t.Fatalf("a control record from before candidacy reads as %q (capable %v), want preferred", old.CandidacyClass(), old.ControlCapable())
	}
	worker := InstanceRegistration{InstanceID: "w", Roles: roles.Set{roles.Worker}, Incarnation: "p", ExpiresAt: at}
	if worker.CandidacyClass() != "" || worker.ControlCapable() {
		t.Fatal("a Worker record without candidacy reads as a control candidate")
	}
}

func TestAPreferredCandidateIsListedOnlyWhileItsRecordIsLive(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Millisecond)
	for _, instance := range []InstanceRegistration{
		{InstanceID: "control-b", Roles: roles.Set{roles.Control}, Incarnation: "b", ExpiresAt: at.Add(time.Minute),
			ControlCandidacy: ControlCandidacyPreferred, ControlCandidacyReady: true},
		{InstanceID: "control-a", Roles: roles.Set{roles.Control}, Incarnation: "a", ExpiresAt: at.Add(10 * time.Second),
			ControlCandidacy: ControlCandidacyPreferred},
		{InstanceID: "worker", Roles: roles.Set{roles.Worker}, Incarnation: "w", ExpiresAt: at.Add(time.Minute),
			ControlCandidacy: ControlCandidacyFallback, ControlCandidacyReady: true},
	} {
		if err := store.RegisterInstance(ctx, instance); err != nil {
			t.Fatal(err)
		}
	}
	listed, err := store.ListPreferredControlCandidates(ctx, at)
	if err != nil || len(listed) != 2 || listed[0].InstanceID != "control-a" || listed[0].ControlCandidacyReady ||
		listed[1].InstanceID != "control-b" || !listed[1].ControlCandidacyReady {
		t.Fatalf("preferred candidates = (%+v, %v), want control-a not ready and control-b ready, and no Worker", listed, err)
	}
	// Past control-a's record expiry it is no longer listed, and the index
	// forgets it; a record that went before the index noticed is skipped.
	if err := store.client.Del(ctx, store.instanceKey("control-b")).Err(); err != nil {
		t.Fatal(err)
	}
	listed, err = store.ListPreferredControlCandidates(ctx, at.Add(20*time.Second))
	if err != nil || len(listed) != 0 {
		t.Fatalf("preferred candidates after expiry = (%+v, %v), want none", listed, err)
	}
	if members := store.client.ZRange(ctx, store.preferredCandidatesKey(), 0, -1).Val(); len(members) != 1 || members[0] != "control-b" {
		t.Fatalf("index after the read = %v, want only the entry whose score is still live", members)
	}
	if err := store.client.Set(ctx, store.instanceKey("control-b"), "broken-json", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListPreferredControlCandidates(ctx, at); err == nil {
		t.Fatal("an unreadable preferred record was answered with a short list")
	}
}

func TestACandidateIsStableOnlyAfterAWholeWindowOfReadinessFromItsOwnRecord(t *testing.T) {
	at := time.Unix(1_800_000_000, 0)
	since := at.Add(-time.Minute)
	record := InstanceRegistration{InstanceID: "c", Roles: roles.Set{roles.Control}, Incarnation: "p", ExpiresAt: at.Add(time.Minute),
		ControlCandidacy: ControlCandidacyPreferred, ControlCandidacyReady: true, ControlCandidacyReadySince: &since}
	if err := record.Validate(); err != nil || !record.CandidacyStable(at, time.Minute) {
		t.Fatalf("a record ready for the whole window: valid %v, stable %v", err, record.CandidacyStable(at, time.Minute))
	}
	if record.CandidacyStable(at.Add(-time.Second), time.Minute) {
		t.Fatal("stable a second before the window had passed")
	}
	if expired := record; expired.CandidacyStable(at.Add(time.Minute), time.Minute) {
		t.Fatal("an expired record reads as stable")
	}
	older := record
	older.ControlCandidacyReadySince = nil
	if older.CandidacyStable(at, time.Minute) {
		t.Fatal("a record without a ready-since time reads as stable")
	}
	notReady := record
	notReady.ControlCandidacyReady = false
	if err := notReady.Validate(); err == nil {
		t.Fatal("a ready-since time on a record that is not ready was accepted")
	}
	payload, _ := json.Marshal(older)
	if strings.Contains(string(payload), "control_candidacy_ready_since") {
		t.Fatalf("a record with no ready-since time writes the field: %s", payload)
	}
}

func TestAFallbackCandidateIsListedFromItsOwnIndex(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Millisecond)
	for _, instance := range []InstanceRegistration{
		{InstanceID: "worker-b", Roles: roles.Set{roles.Worker}, Incarnation: "b", ExpiresAt: at.Add(time.Minute),
			ControlCandidacy: ControlCandidacyFallback, ControlCandidacyReady: true},
		{InstanceID: "worker-a", Roles: roles.Set{roles.Worker}, Incarnation: "a", ExpiresAt: at.Add(10 * time.Second),
			ControlCandidacy: ControlCandidacyFallback},
		{InstanceID: "control", Roles: roles.Set{roles.Control}, Incarnation: "c", ExpiresAt: at.Add(time.Minute),
			ControlCandidacy: ControlCandidacyPreferred, ControlCandidacyReady: true},
		{InstanceID: "plain-worker", Roles: roles.Set{roles.Worker}, Incarnation: "w", ExpiresAt: at.Add(time.Minute)},
	} {
		if err := store.RegisterInstance(ctx, instance); err != nil {
			t.Fatal(err)
		}
	}
	listed, err := store.ListFallbackControlCandidates(ctx, at)
	if err != nil || len(listed) != 2 || listed[0].InstanceID != "worker-a" || listed[1].InstanceID != "worker-b" || !listed[1].ControlCandidacyReady {
		t.Fatalf("fallback candidates = (%+v, %v), want worker-a and worker-b and nothing else", listed, err)
	}
	if preferred, err := store.ListPreferredControlCandidates(ctx, at); err != nil || len(preferred) != 1 || preferred[0].InstanceID != "control" {
		t.Fatalf("preferred candidates = (%+v, %v), want only the control process", preferred, err)
	}
	listed, err = store.ListFallbackControlCandidates(ctx, at.Add(20*time.Second))
	if err != nil || len(listed) != 1 || listed[0].InstanceID != "worker-b" {
		t.Fatalf("fallback candidates past worker-a's expiry = (%+v, %v)", listed, err)
	}
	if members := store.client.ZRange(ctx, store.fallbackCandidatesKey(), 0, -1).Val(); len(members) != 1 || members[0] != "worker-b" {
		t.Fatalf("fallback index after the read = %v, want the lapsed entry pruned", members)
	}
}

// The indexes are pruned on read, and in the steady state nothing reads
// them; the preferred renewal also drops their expired entries, so names a
// rollout left behind do not pile up under a control process that never
// restarts. A Worker's renewal adds nothing for it.
func TestAPreferredRenewalDropsTheExpiredEntriesOfBothIndexes(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	for key, members := range map[string][]string{
		store.fallbackCandidatesKey():  {"rolled-worker-1", "rolled-worker-2"},
		store.preferredCandidatesKey(): {"rolled-control"},
	} {
		for _, member := range members {
			if err := store.client.ZAdd(ctx, key, &redis.Z{Score: float64(now.Add(-time.Hour).UnixMilli()), Member: member}).Err(); err != nil {
				t.Fatal(err)
			}
		}
	}
	worker := InstanceRegistration{InstanceID: "worker-live", Roles: roles.Set{roles.Worker}, Incarnation: "w", ExpiresAt: now.Add(time.Minute),
		ControlCandidacy: ControlCandidacyFallback}
	if err := store.RegisterInstance(ctx, worker); err != nil {
		t.Fatal(err)
	}
	if n := store.client.ZCard(ctx, store.fallbackCandidatesKey()).Val(); n != 3 {
		t.Fatalf("a Worker's renewal pruned the fallback index: %d entries, want the two expired and its own", n)
	}
	control := InstanceRegistration{InstanceID: "control-live", Roles: roles.Set{roles.Control}, Incarnation: "c", ExpiresAt: now.Add(time.Minute),
		ControlCandidacy: ControlCandidacyPreferred}
	if err := store.RegisterInstance(ctx, control); err != nil {
		t.Fatal(err)
	}
	fallbacks := store.client.ZRange(ctx, store.fallbackCandidatesKey(), 0, -1).Val()
	preferred := store.client.ZRange(ctx, store.preferredCandidatesKey(), 0, -1).Val()
	if len(fallbacks) != 1 || fallbacks[0] != "worker-live" || len(preferred) != 1 || preferred[0] != "control-live" {
		t.Fatalf("after a preferred renewal: fallbacks %v, preferred %v, want only the live ones", fallbacks, preferred)
	}
}
