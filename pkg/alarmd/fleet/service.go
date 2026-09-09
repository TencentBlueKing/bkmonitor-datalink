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
	if retention, ok := snapshots.(interface{ TTL() time.Duration }); ok {
		if budget := retention.TTL(); budget > 0 && freshness >= budget {
			return nil, errors.New("alarmd fleet: snapshot freshness must be shorter than retention, or a stuck replica is never reported as stale")
		}
	}
	if now == nil {
		now = time.Now
	}
	return &Service{expectations: expectations, registry: registry, snapshots: snapshots, freshness: freshness, now: now}, nil
}

// View assembles the current answer.
//
// Every dependency failure becomes a gap rather than an error: a caller that
// gets an error learns nothing, while a caller that gets a view marked UNKNOWN
// learns exactly which part is missing. The one thing this must never do is
// return a healthy-looking view built from whatever happened to be readable.
func (service *Service) View(ctx context.Context) View {
	at := service.now()

	replicas, err := service.registry.ReadyReplicas(ctx, at)
	if err != nil {
		return View{
			Health:    HealthUnknown,
			Gaps:      []Gap{{Kind: GapRegistryUnavailable, Detail: err.Error()}},
			Anomalies: []Anomaly{},
			Replicas:  []string{},
		}
	}

	expectation := Expectation{}
	expectationErr := ""
	if resolved, err := service.expectations.Expectation(ctx); err != nil {
		expectationErr = err.Error()
	} else {
		expectation = resolved
	}

	snapshots, err := service.snapshots.Load(ctx, replicas)
	if err != nil {
		// Reading snapshots failed as a whole, so no replica can be said to
		// have published. Coverage is therefore zero and every replica is
		// missing, which is what the aggregation is given.
		snapshots = nil
	}

	view := Aggregate(expectation, snapshots, replicas, at, service.freshness)
	if expectationErr != "" {
		for index := range view.Gaps {
			if view.Gaps[index].Kind == GapDenominatorUnavailable {
				view.Gaps[index].Detail = expectationErr
			}
		}
	}
	return view
}
