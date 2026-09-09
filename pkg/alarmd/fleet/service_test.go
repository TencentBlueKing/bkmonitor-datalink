// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type stubExpectations struct {
	expectation Expectation
	err         error
}

func (stub stubExpectations) Expectation(context.Context) (Expectation, error) {
	return stub.expectation, stub.err
}

type stubRegistry struct {
	replicas []string
	err      error
}

func (stub stubRegistry) ReadyReplicas(context.Context, time.Time) ([]string, error) {
	return stub.replicas, stub.err
}

type stubSnapshots struct {
	snapshots []Snapshot
	err       error
}

func (stub stubSnapshots) Load(context.Context, []string) ([]Snapshot, error) {
	return stub.snapshots, stub.err
}

type countingExpectations struct {
	expectation Expectation
	calls       int
}

func (stub *countingExpectations) Expectation(context.Context) (Expectation, error) {
	stub.calls++
	return stub.expectation, nil
}

// Reading the denominator decodes the whole active object set from the same
// Redis the pipeline depends on. This surface is meant to be embedded in a
// page, so without a window its cost would scale with how many people are
// looking at it, and a dashboard refreshing every second would put that read on
// the hot path once a second forever.
func TestRepeatedViewsReadTheControlPlaneOncePerWindow(t *testing.T) {
	expectations := &countingExpectations{expectation: Expectation{QueryGroups: 949, Known: true}}
	at := now
	service, err := NewService(expectations, stubRegistry{replicas: replicas()},
		stubSnapshots{snapshots: healthySnapshots()}, time.Minute, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	for request := 0; request < 20; request++ {
		if health := service.View(context.Background()).Health; health != HealthHealthy {
			t.Fatalf("health = %s on request %d", health, request)
		}
	}
	if expectations.calls != 1 {
		t.Fatalf("control plane reads = %d, want one for the whole window", expectations.calls)
	}
	// Past the window the answer must be re-read, or a replica could leave the
	// deployment without any later view noticing.
	at = now.Add(time.Minute)
	service.View(context.Background())
	if expectations.calls != 2 {
		t.Fatalf("control plane reads = %d, want a refresh once the window passed", expectations.calls)
	}
}

func mustService(t *testing.T, expectations ExpectationSource, registry ReplicaRegistry, snapshots SnapshotReader) *Service {
	t.Helper()
	service, err := NewService(expectations, registry, snapshots, time.Minute, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestServiceReportsHealthyOnlyWhenEverySourceAgrees(t *testing.T) {
	service := mustService(t,
		stubExpectations{expectation: Expectation{QueryGroups: 949, Known: true}},
		stubRegistry{replicas: replicas()},
		stubSnapshots{snapshots: healthySnapshots()},
	)
	view := service.View(context.Background())
	if view.Health != HealthHealthy {
		t.Fatalf("health = %s, gaps = %+v", view.Health, view.Gaps)
	}
}

// A caller that receives an error learns nothing. A caller that receives a view
// marked unknown learns which part is missing, which is the difference between
// a page that says "cannot tell" and a page that says nothing at all.
func TestDependencyFailuresBecomeGapsRatherThanErrors(t *testing.T) {
	cases := []struct {
		name string
		want GapKind
		make func() *Service
	}{
		{
			name: "registry unreadable",
			want: GapRegistryUnavailable,
			make: func() *Service {
				return mustService(t,
					stubExpectations{expectation: Expectation{QueryGroups: 949, Known: true}},
					stubRegistry{err: errors.New("redis down")},
					stubSnapshots{snapshots: healthySnapshots()},
				)
			},
		},
		{
			name: "denominator unreadable",
			want: GapDenominatorUnavailable,
			make: func() *Service {
				return mustService(t,
					stubExpectations{err: errors.New("no active set")},
					stubRegistry{replicas: replicas()},
					stubSnapshots{snapshots: healthySnapshots()},
				)
			},
		},
		{
			name: "snapshots unreadable",
			want: GapReplicaMissing,
			make: func() *Service {
				return mustService(t,
					stubExpectations{expectation: Expectation{QueryGroups: 949, Known: true}},
					stubRegistry{replicas: replicas()},
					stubSnapshots{err: errors.New("mget failed")},
				)
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			view := testCase.make().View(context.Background())
			if view.Health != HealthUnknown {
				t.Fatalf("health = %s, want %s", view.Health, HealthUnknown)
			}
			if !hasGap(view, testCase.want) {
				t.Fatalf("gaps = %+v, want one of kind %s", view.Gaps, testCase.want)
			}
		})
	}
}

// The denominator failure has to carry why, or an operator sees "unknown" with
// no way to tell a missing control plane from an empty one.
// The gap says the denominator could not be read; it does not quote the error
// that says so. This body is served on the listener a host platform may route
// to, and a Redis client's error names the endpoint it failed to reach.
func TestUnreadableDenominatorClassifiesItsCauseWithoutQuotingIt(t *testing.T) {
	service := mustService(t,
		stubExpectations{err: errors.New("dial tcp 10.0.0.1:6379: connect: connection refused")},
		stubRegistry{replicas: replicas()},
		stubSnapshots{snapshots: healthySnapshots()},
	)
	view := service.View(context.Background())
	for _, gap := range view.Gaps {
		if gap.Kind == GapDenominatorUnavailable {
			if gap.Detail != "unavailable" {
				t.Fatalf("detail = %q, want a classification rather than the raw error", gap.Detail)
			}
			if strings.Contains(gap.Detail, "10.0.0.1") {
				t.Fatalf("detail = %q, leaks the dependency address", gap.Detail)
			}
			return
		}
	}
	t.Fatalf("gaps = %+v, want a denominator gap", view.Gaps)
}

// Losing every snapshot must not look like a deployment with no anomalies.
func TestUnreadableSnapshotsReportZeroCoverageNotZeroAnomalies(t *testing.T) {
	service := mustService(t,
		stubExpectations{expectation: Expectation{QueryGroups: 949, Known: true}},
		stubRegistry{replicas: replicas()},
		stubSnapshots{err: errors.New("mget failed")},
	)
	view := service.View(context.Background())
	if view.Covered != 0 || view.Unknown != 949 {
		t.Fatalf("coverage = %d covered / %d unknown, want 0 / 949", view.Covered, view.Unknown)
	}
	if view.Health != HealthUnknown {
		t.Fatalf("health = %s, want %s", view.Health, HealthUnknown)
	}
}

// If the freshness budget reaches the retention budget, a snapshot expires at
// the same moment it stops being fresh, so the stale branch never fires and a
// stuck-but-alive replica is reported as gone. The two mean different things to
// whoever is on call.
func TestFreshnessIsClampedBelowRetentionRatherThanRejected(t *testing.T) {
	store := mustStore(t, newFakeRedis(), time.Minute, 10)
	// Rejecting would let a diagnostics setting stop the process whose facts it
	// describes: a longer reconcile interval is ordinary tuning, not an error.
	for _, freshness := range []time.Duration{time.Minute, 2 * time.Minute} {
		service, err := NewService(stubExpectations{}, stubRegistry{}, store, freshness, nil)
		if err != nil {
			t.Fatalf("freshness %v was rejected instead of clamped: %v", freshness, err)
		}
		if service.freshness >= store.TTL() {
			t.Fatalf("clamped freshness = %v, want shorter than retention %v", service.freshness, store.TTL())
		}
	}
	service, err := NewService(stubExpectations{}, stubRegistry{}, store, 30*time.Second, nil)
	if err != nil || service.freshness != 30*time.Second {
		t.Fatalf("usable freshness was altered: %v %v", service.freshness, err)
	}
}

func TestNewServiceRequiresEverySource(t *testing.T) {
	if _, err := NewService(nil, stubRegistry{}, stubSnapshots{}, time.Minute, nil); err == nil {
		t.Fatal("service was built without an expectation source")
	}
	if _, err := NewService(stubExpectations{}, nil, stubSnapshots{}, time.Minute, nil); err == nil {
		t.Fatal("service was built without a registry")
	}
	if _, err := NewService(stubExpectations{}, stubRegistry{}, nil, time.Minute, nil); err == nil {
		t.Fatal("service was built without snapshots")
	}
}
