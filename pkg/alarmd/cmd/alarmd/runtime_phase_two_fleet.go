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
	"errors"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// controlPlaneExpectation reads the authoritative object set from the control
// plane. Every replica can read it, which is the point: only the control leader
// observes the whole active set in memory, so a locally derived denominator
// would have most replicas reporting full coverage of nothing.
type controlPlaneExpectation struct {
	repository *controlplane.RedisCatalogRepository
}

func (source controlPlaneExpectation) Expectation(ctx context.Context) (fleet.Expectation, error) {
	if source.repository == nil {
		return fleet.Expectation{}, errors.New("alarmd fleet: control plane repository is required")
	}
	activation, err := source.repository.LoadActivation(ctx)
	if err != nil {
		return fleet.Expectation{}, err
	}
	groups, err := source.repository.LoadActiveQueryGroupSet(ctx, activation.ActiveQGSetRef)
	if err != nil {
		return fleet.Expectation{}, err
	}
	return fleet.Expectation{QueryGroups: len(groups), Known: true}, nil
}

// registryReplicas lists the workers that should have published a snapshot.
// Using the ownership registry rather than a second index keeps one answer to
// "who is in this deployment" instead of two that can disagree.
type registryReplicas struct {
	store *ownership.RedisStore
}

func (source registryReplicas) ReadyReplicas(ctx context.Context, at time.Time) ([]string, error) {
	if source.store == nil {
		return nil, errors.New("alarmd fleet: ownership store is required")
	}
	workers, err := source.store.ListReadyWorkers(ctx, at)
	if err != nil {
		return nil, err
	}
	replicas := make([]string, 0, len(workers))
	for _, worker := range workers {
		replicas = append(replicas, worker.WorkerID)
	}
	return replicas, nil
}

// fleetPublisher writes this replica's snapshot on a timer.
type fleetPublisher struct {
	tracker  *fleet.Tracker
	store    *fleet.RedisStore
	replica  string
	interval time.Duration
	owned    func() []execution.QueryGroupIdentity
	now      func() time.Time
	observe  func(error)
}

// run publishes until the context ends. A publish failure is observed and
// retried on the next tick rather than propagated: the snapshot is diagnostics,
// and diagnostics must not be able to stop the pipeline that produces the
// facts they describe.
func (publisher fleetPublisher) run(ctx context.Context) {
	ticker := time.NewTicker(publisher.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			publisher.publishOnce(ctx)
		}
	}
}

func (publisher fleetPublisher) publishOnce(ctx context.Context) {
	owned := publisher.owned()
	retained := make(map[string]struct{}, len(owned))
	for _, queryGroup := range owned {
		retained[string(queryGroup)] = struct{}{}
	}
	// Objects this replica no longer owns are dropped before the list is built,
	// so a handover cannot leave their last known state to be republished for
	// as long as the process lives.
	publisher.tracker.Forget(retained)

	anomalies := publisher.tracker.Anomalies()
	snapshot := fleet.Snapshot{
		Replica:        publisher.replica,
		TakenAt:        publisher.now(),
		Owned:          len(owned),
		Anomalies:      anomalies,
		TotalAnomalies: len(anomalies),
	}
	if err := publisher.store.Publish(ctx, snapshot); err != nil && publisher.observe != nil {
		publisher.observe(err)
	}
}
