// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
)

// The identity a process writes into its registration: a token minted once
// and never the same twice, and an endpoint on the HTTP listener's port at
// the address the listener binds -- or, for a wildcard bind, the address
// this process reaches Redis from. A listener that cannot be parsed leaves
// the endpoint empty and the token minted: the process serves the stream
// and is not advertised.
func TestTheStreamIdentityIsMintedOnceAndAdvertisedOnTheListenersPort(t *testing.T) {
	first, err := newViewStreamIdentity("10.1.2.3:8080", "127.0.0.1:6379")
	if err != nil {
		t.Fatal(err)
	}
	second, err := newViewStreamIdentity("10.1.2.3:8080", "127.0.0.1:6379")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Token) != 64 || first.Token == second.Token {
		t.Fatalf("tokens = %q / %q, want 32 random bytes each, different", first.Token, second.Token)
	}
	if first.Endpoint != "10.1.2.3:8080" {
		t.Fatalf("bound listener endpoint = %q, want the bound address", first.Endpoint)
	}
	wildcard, err := newViewStreamIdentity(":8080", "127.0.0.1:6379")
	if err != nil {
		t.Fatal(err)
	}
	host, port, err := net.SplitHostPort(wildcard.Endpoint)
	if err != nil || port != "8080" || host == "" || host == "0.0.0.0" {
		t.Fatalf("wildcard listener endpoint = %q, want the outbound address toward Redis on port 8080", wildcard.Endpoint)
	}
	if outbound := outboundAddress("127.0.0.1:6379"); outbound != host {
		t.Fatalf("endpoint host %s, outbound toward Redis %s", host, outbound)
	}
	unparsable, err := newViewStreamIdentity("not-a-listener", "127.0.0.1:6379")
	if err != nil || unparsable.Endpoint != "" || unparsable.Token == "" || unparsable.Unadvertised != "LISTENER_UNPARSABLE" {
		t.Fatalf("unparsable listener = %+v (%v), want no endpoint, a token and the reason", unparsable, err)
	}
}

// A Sentinel deployment has no Redis address: the routes the endpoint is
// derived from are the sentinels, and the empty address ahead of them must
// not end the derivation. This is #216: every process advertised no
// endpoint, and every Worker's discovery reported the Leader missing while
// it published. A process with no route at all falls back to an interface
// address, and only a process with neither is left unadvertised, by name.
func TestTheWildcardEndpointIsDerivedFromTheFirstRouteThatResolves(t *testing.T) {
	sentinel := config.RedisConnectionConfig{Mode: config.RedisModeSentinel, MasterName: "mymaster",
		SentinelAddress: []string{"127.0.0.1:26379", "127.0.0.1:26380"}}
	routes := viewStreamRoutes(sentinel)
	if strings.Join(routes, ",") != "127.0.0.1:26379,127.0.0.1:26380" {
		t.Fatalf("sentinel routes = %v, want the sentinels and no empty address ahead of them", routes)
	}
	viaSentinel, err := newViewStreamIdentity(":8080", routes...)
	if err != nil {
		t.Fatal(err)
	}
	host, port, err := net.SplitHostPort(viaSentinel.Endpoint)
	if err != nil || port != "8080" || host != outboundAddress("127.0.0.1:26379") || viaSentinel.Unadvertised != "" {
		t.Fatalf("endpoint under sentinel = %+v, want the outbound address toward the first sentinel on port 8080", viaSentinel)
	}
	standalone := viewStreamRoutes(config.RedisConnectionConfig{Mode: config.RedisModeStandalone, Address: "127.0.0.1:6379"})
	if strings.Join(standalone, ",") != "127.0.0.1:6379" {
		t.Fatalf("standalone routes = %v", standalone)
	}
	// An unresolvable route ahead of a good one is skipped, not fatal.
	skipped, err := newViewStreamIdentity(":8080", "", "256.1.1.1:1", "127.0.0.1:26379")
	if err != nil || skipped.Endpoint != viaSentinel.Endpoint {
		t.Fatalf("endpoint with dead routes ahead = %+v, want %s", skipped, viaSentinel.Endpoint)
	}
	noRoute, err := newViewStreamIdentity(":8080")
	if err != nil {
		t.Fatal(err)
	}
	if fallback := interfaceAddress(); fallback != "" {
		if noRoute.Endpoint != net.JoinHostPort(fallback, "8080") || noRoute.Unadvertised != "" {
			t.Fatalf("endpoint with no route = %+v, want the interface address %s", noRoute, fallback)
		}
	} else if noRoute.Endpoint != "" || noRoute.Unadvertised != "NO_ROUTE" {
		t.Fatalf("endpoint with no route and no interface = %+v, want unadvertised by name", noRoute)
	}
}

type fakeDiscoveryStore struct {
	leader        ownership.ControlLeader
	leaderFound   bool
	leaderErr     error
	registrations map[string]ownership.WorkerRegistration
	workerErr     error
}

func (store fakeDiscoveryStore) ReadControlLeader(context.Context) (ownership.ControlLeader, bool, error) {
	return store.leader, store.leaderFound, store.leaderErr
}

func (store fakeDiscoveryStore) ReadWorker(_ context.Context, workerID string) (ownership.WorkerRegistration, bool, error) {
	if store.workerErr != nil {
		return ownership.WorkerRegistration{}, false, store.workerErr
	}
	registration, found := store.registrations[workerID]
	return registration, found, nil
}

// The three ways of finding no Leader are named apart, because they are
// mended in different places: no lease, a lease naming a Worker that is not
// registered, and a registered Leader that advertises no endpoint. #216
// reported the third as NO_LEADER on every Worker.
func TestDiscoveryNamesEachWayOfFindingNoLeader(t *testing.T) {
	lease := ownership.ControlLeader{OwnerID: "leader-1", OwnerEpoch: 35}
	for name, test := range map[string]struct {
		store fakeDiscoveryStore
		miss  string
		found string
		fails bool
	}{
		"no lease":               {store: fakeDiscoveryStore{}, miss: viewstream.MissNoLeader},
		"lease, no registration": {store: fakeDiscoveryStore{leader: lease, leaderFound: true}, miss: viewstream.MissLeaderUnregistered},
		"lease, registration without endpoint": {store: fakeDiscoveryStore{leader: lease, leaderFound: true,
			registrations: map[string]ownership.WorkerRegistration{"leader-1": {WorkerID: "leader-1"}}}, miss: viewstream.MissLeaderNoEndpoint},
		"lease, advertised": {store: fakeDiscoveryStore{leader: lease, leaderFound: true,
			registrations: map[string]ownership.WorkerRegistration{"leader-1": {WorkerID: "leader-1", Endpoint: "10.0.0.1:8080"}}}, found: "10.0.0.1:8080"},
		"lease read fails":    {store: fakeDiscoveryStore{leaderErr: errors.New("redis down")}, fails: true},
		"registry read fails": {store: fakeDiscoveryStore{leader: lease, leaderFound: true, workerErr: errors.New("redis down")}, fails: true},
	} {
		t.Run(name, func(t *testing.T) {
			leader, miss, err := viewStreamDiscovery{store: test.store}.Leader(context.Background())
			if test.fails {
				if err == nil {
					t.Fatalf("Leader() = (%+v, %q, nil), want the read error", leader, miss)
				}
				return
			}
			if err != nil || miss != test.miss || leader.Endpoint != test.found {
				t.Fatalf("Leader() = (%+v, %q, %v), want miss %q endpoint %q", leader, miss, err, test.miss, test.found)
			}
			if test.found != "" && (leader.WorkerID != "leader-1" || leader.ControlEpoch != 35) {
				t.Fatalf("Leader() = %+v, want the lease's worker and epoch", leader)
			}
		})
	}
}

type fakeRegistry struct {
	registrations map[string]ownership.WorkerRegistration
	err           error
}

func (registry fakeRegistry) ReadWorker(_ context.Context, workerID string) (ownership.WorkerRegistration, bool, error) {
	if registry.err != nil {
		return ownership.WorkerRegistration{}, false, registry.err
	}
	registration, found := registry.registrations[workerID]
	return registration, found, nil
}

// Admission reads the Worker's own registration: the token there admits,
// any other token or none is BAD_TOKEN, no live registration is
// UNKNOWN_WORKER, and a registry that cannot be read is an error the
// server turns into REGISTRY_UNAVAILABLE.
func TestAdmissionTrustsTheRegistrationAndNothingElse(t *testing.T) {
	now := time.Unix(1000, 0)
	live := ownership.WorkerRegistration{WorkerID: "w1", StreamToken: "secret", ExpiresAt: now.Add(time.Minute)}
	old := ownership.WorkerRegistration{WorkerID: "w2", StreamToken: "secret", ExpiresAt: now.Add(-time.Minute)}
	legacy := ownership.WorkerRegistration{WorkerID: "w3", ExpiresAt: now.Add(time.Minute)}
	admission := viewStreamAdmission{registry: fakeRegistry{registrations: map[string]ownership.WorkerRegistration{"w1": live, "w2": old, "w3": legacy}}, now: func() time.Time { return now }}
	for name, test := range map[string]struct {
		worker, token, want string
	}{
		"right token":                {"w1", "secret", ""},
		"wrong token":                {"w1", "guess", viewstream.RefusalBadToken},
		"empty token":                {"w1", "", viewstream.RefusalBadToken},
		"expired registration":       {"w2", "secret", viewstream.RefusalUnknownWorker},
		"unknown worker":             {"w9", "secret", viewstream.RefusalUnknownWorker},
		"registration without token": {"w3", "", viewstream.RefusalBadToken},
	} {
		got, err := admission.Admit(context.Background(), test.worker, test.token)
		if err != nil || got != test.want {
			t.Fatalf("%s: (%q, %v), want %q", name, got, err, test.want)
		}
	}
	if _, err := (viewStreamAdmission{registry: fakeRegistry{err: context.DeadlineExceeded}, now: time.Now}).Admit(context.Background(), "w1", "secret"); err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("registry error = %v, want it returned", err)
	}
}
