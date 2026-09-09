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
	"sync"
	"time"
)

// GapRegistryUnavailable means the set of replicas that should have published
// could not be read, so there is no way to tell a quiet deployment from an
// unreadable one.
const GapRegistryUnavailable GapKind = "REGISTRY_UNAVAILABLE"

// ExpectationSource reads the authoritative object set from the control plane.
//
// It is deliberately not derived from the calling replica's own state: on a
// multi-replica deployment only the control leader observes the whole active
// set and the others observe an empty one, so a local reading would have most
// replicas reporting full coverage of nothing.
type ExpectationSource interface {
	Expectation(ctx context.Context) (Expectation, error)
}

// ReplicaRegistry lists the replicas expected to publish a snapshot.
type ReplicaRegistry interface {
	ReadyReplicas(ctx context.Context, at time.Time) ([]string, error)
}

// SnapshotReader reads published snapshots for the given replicas.
type SnapshotReader interface {
	Load(ctx context.Context, replicas []string) ([]Snapshot, error)
}

// Service answers deployment-wide questions from any replica.
type Service struct {
	expectations ExpectationSource
	registry     ReplicaRegistry
	snapshots    SnapshotReader
	freshness    time.Duration
	now          func() time.Time

	// The denominator and the replica list are read from the control plane on
	// the same Redis the pipeline depends on, and reading the active object set
	// decodes the whole set. This surface is meant to be embedded in a page, so
	// its cost would otherwise scale with how many people are looking at it.
	// The mutex is held across the refresh on purpose: concurrent viewers then
	// collapse into one read instead of racing to issue their own.
	sourceMu    sync.Mutex
	sourcesAt   time.Time
	sourcesFor  time.Duration
	replicas    []string
	replicasErr error
	expectation Expectation
	expectErr   error
}

// NewService wires the three sources. freshness is how old a snapshot may be
// before it stops counting as coverage, and must be shorter than the store's
// retention: equal budgets make the stale branch unreachable, because a
// snapshot would expire at the same moment it stopped being fresh.
func NewService(
	expectations ExpectationSource,
	registry ReplicaRegistry,
	snapshots SnapshotReader,
	freshness time.Duration,
	now func() time.Time,
) (*Service, error) {
	if expectations == nil || registry == nil || snapshots == nil {
		return nil, errors.New("alarmd fleet: service requires expectations, a registry and snapshots")
	}
	if freshness <= 0 {
		freshness = DefaultTTL / 4
	}
	// Clamped, not rejected. A freshness budget that outgrows retention is a
	// diagnostics setting, and refusing to build the service would let it stop
	// the process whose facts it exists to describe -- which is the one thing
	// this whole path is not allowed to do.
	if retention, ok := snapshots.(interface{ TTL() time.Duration }); ok {
		if budget := retention.TTL(); budget > 0 && freshness >= budget {
			freshness = budget / 2
		}
	}
	if now == nil {
		now = time.Now
	}
	// Half the freshness budget: long enough that a page refreshing every second
	// costs one control-plane read per publish cycle rather than one per view,
	// short enough that a replica cannot go stale without the next view seeing
	// it, since staleness is judged against the same budget.
	return &Service{
		expectations: expectations, registry: registry, snapshots: snapshots,
		freshness: freshness, sourcesFor: freshness / 2, now: now,
	}, nil
}

// sources returns the denominator and the replica list, reading them at most
// once per cache window. Failures are cached too: a control plane that is down
// stays down for the window, and retrying it per request would add load to a
// dependency that is already failing.
func (service *Service) sources(ctx context.Context, at time.Time) ([]string, error, Expectation, error) {
	service.sourceMu.Lock()
	defer service.sourceMu.Unlock()
	if !service.sourcesAt.IsZero() && at.Sub(service.sourcesAt) < service.sourcesFor {
		return service.replicas, service.replicasErr, service.expectation, service.expectErr
	}
	service.replicas, service.replicasErr = service.registry.ReadyReplicas(ctx, at)
	service.expectation, service.expectErr = service.expectations.Expectation(ctx)
	if service.expectErr != nil {
		service.expectation = Expectation{}
	}
	service.sourcesAt = at
	return service.replicas, service.replicasErr, service.expectation, service.expectErr
}

// View assembles the current answer.
//
// Every dependency failure becomes a gap rather than an error: a caller that
// gets an error learns nothing, while a caller that gets a view marked UNKNOWN
// learns exactly which part is missing. The one thing this must never do is
// return a healthy-looking view built from whatever happened to be readable.
func (service *Service) View(ctx context.Context) View {
	at := service.now()

	replicas, replicasErr, expectation, expectationErr := service.sources(ctx, at)
	if replicasErr != nil {
		return View{
			Health:    HealthUnknown,
			Gaps:      []Gap{{Kind: GapRegistryUnavailable, Detail: gapDetail(replicasErr)}},
			Anomalies: []Anomaly{},
			Replicas:  []string{},
		}
	}

	snapshots, err := service.snapshots.Load(ctx, replicas)
	if err != nil {
		// Reading snapshots failed as a whole, so no replica can be said to
		// have published. Coverage is therefore zero and every replica is
		// missing, which is what the aggregation is given.
		snapshots = nil
	}

	view := Aggregate(expectation, snapshots, replicas, at, service.freshness)
	if expectationErr != nil {
		for index := range view.Gaps {
			if view.Gaps[index].Kind == GapDenominatorUnavailable {
				view.Gaps[index].Detail = gapDetail(expectationErr)
			}
		}
	}
	return view
}

// gapDetail classifies a dependency failure instead of quoting it.
//
// This body is served on the listener a host platform may route to, so anything
// it carries is readable by whoever can reach that port. A raw error from a
// Redis client names the endpoint it failed to reach, which says more about the
// deployment's internals than a caller asking whether alarmd is healthy needs to
// know. The full text stays where it was already going: this process's logs and
// the failure metrics of the store that produced it.
func gapDetail(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "timed out"
	default:
		return "unavailable"
	}
}
