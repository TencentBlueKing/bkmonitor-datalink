// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/roles"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
)

type instanceDiscoveryStore struct {
	fakeDiscoveryStore
	instance    ownership.InstanceRegistration
	present     bool
	instanceErr error
	workerReads int
}

func (store *instanceDiscoveryStore) ReadInstance(context.Context, string) (ownership.InstanceRegistration, bool, error) {
	return store.instance, store.present, store.instanceErr
}

func (store *instanceDiscoveryStore) ReadWorker(ctx context.Context, id string) (ownership.WorkerRegistration, bool, error) {
	store.workerReads++
	return store.fakeDiscoveryStore.ReadWorker(ctx, id)
}

func TestDiscoveryPrefersControlInstanceAndLimitsLegacyFallback(t *testing.T) {
	at := time.Unix(1700000000, 0)
	valid := ownership.InstanceRegistration{InstanceID: "leader", Roles: roles.Set{roles.Control},
		Endpoint: "192.0.2.2:8080", Incarnation: "control-process", ExpiresAt: at.Add(time.Minute)}
	for name, test := range map[string]struct {
		mutate func(*instanceDiscoveryStore)
		miss   string
		found  string
		fails  bool
		legacy bool
	}{
		"control only":                {found: valid.Endpoint},
		"old leader without instance": {mutate: func(store *instanceDiscoveryStore) { store.present = false }, found: "192.0.2.1:8080", legacy: true},
		"expired instance":            {mutate: func(store *instanceDiscoveryStore) { store.instance.ExpiresAt = at }, miss: viewstream.MissLeaderUnregistered},
		"worker only":                 {mutate: func(store *instanceDiscoveryStore) { store.instance.Roles = roles.Set{roles.Worker} }, miss: viewstream.MissLeaderUnregistered},
		"channel only":                {mutate: func(store *instanceDiscoveryStore) { store.instance.Roles = roles.Set{roles.Channel} }, miss: viewstream.MissLeaderUnregistered},
		"endpoint absent":             {mutate: func(store *instanceDiscoveryStore) { store.instance.Endpoint = "" }, miss: viewstream.MissLeaderNoEndpoint},
		"malformed instance":          {mutate: func(store *instanceDiscoveryStore) { store.instance.Incarnation = "" }, fails: true},
		"read fails":                  {mutate: func(store *instanceDiscoveryStore) { store.instanceErr = errors.New("redis unavailable") }, fails: true},
	} {
		t.Run(name, func(t *testing.T) {
			store := &instanceDiscoveryStore{fakeDiscoveryStore: fakeDiscoveryStore{
				leader: ownership.ControlLeader{OwnerID: "leader", OwnerEpoch: 9}, leaderFound: true,
				registrations: map[string]ownership.WorkerRegistration{"leader": {WorkerID: "leader", Endpoint: "192.0.2.1:8080"}},
			}, instance: valid, present: true}
			if test.mutate != nil {
				test.mutate(store)
			}
			leader, miss, err := (viewStreamDiscovery{store: store, now: func() time.Time { return at }}).Leader(context.Background())
			if (err != nil) != test.fails || miss != test.miss || leader.Endpoint != test.found {
				t.Fatalf("Leader() = (%+v, %q, %v)", leader, miss, err)
			}
			if (store.workerReads > 0) != test.legacy {
				t.Fatalf("legacy reads = %d, want fallback %v", store.workerReads, test.legacy)
			}
			if test.found != "" && (leader.WorkerID != "leader" || leader.ControlEpoch != 9) {
				t.Fatalf("leader identity = %+v", leader)
			}
		})
	}
}
