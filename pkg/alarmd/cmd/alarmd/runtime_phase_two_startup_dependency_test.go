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
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// Each store write startup makes after the control facts is one the reconcile
// tick already survives. Startup now survives it the same way: Start returns,
// the replica is not ready and holds nothing, and the first tick that reaches
// the store brings it in. Before this change each of them ended the process.
func TestStartSurvivesAStoreThatFailsAfterTheControlFacts(t *testing.T) {
	outage := errors.New("dial tcp: connection refused")
	for _, site := range []string{"register_starting", "register_ready", "publish", "assigned"} {
		t.Run(site, func(t *testing.T) {
			queryGroup := execution.QueryGroupIdentity("query-group-1")
			runner := newFakePhaseTwoQueryGroup()
			control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{queryGroup}}
			owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{queryGroup}, runner: runner}
			failing := true
			switch site {
			case "register_starting", "register_ready":
				want := ownership.WorkerStarting
				if site == "register_ready" {
					want = ownership.WorkerReady
				}
				owner.registerHook = func(_ context.Context, registration ownership.WorkerRegistration) error {
					if failing && registration.AssignmentReadiness == want {
						return outage
					}
					return nil
				}
			default:
				owner.injectFailure(site, outage, -1)
			}
			health := newPhaseTwoApplicationHealth()
			bundle := mustPhaseTwoWorkerBundle(t, validGoAccessRuntimeConfig(), health, control, owner)
			defer func() { _ = bundle.Shutdown(context.Background()) }()
			ctx := context.Background()

			if err := bundle.Start(ctx); err != nil {
				t.Fatalf("Start ended on a store outage: %v", err)
			}
			snapshot := health.HealthSnapshot()
			holds := len(bundle.ownedQueryGroups())
			switch site {
			case "publish", "assigned":
				if snapshot.Ready || holds != 0 {
					t.Fatalf("without an Assignment: ready=%v holds %d, want not ready and none (%+v)", snapshot.Ready, holds, snapshot)
				}
			}
			if !hasReason(snapshot.Reasons, phaseTwoControlDependencyReason) {
				t.Fatalf("the outage is not named on the page: %+v", snapshot)
			}

			failing = false
			owner.injectFailure(site, nil, 0)
			if err := bundle.refreshAndReconcile(ctx, false); err != nil {
				t.Fatalf("first tick after the outage: %v", err)
			}
			waitSignal(t, runner.leaseStarted, "the Query Group is opened on the first tick")
			waitForHealth(t, health, "ready after the first tick", func(snapshot observability.HealthSnapshot) bool {
				return snapshot.Ready && !hasReason(snapshot.Reasons, phaseTwoControlDependencyReason)
			})
		})
	}
}

// Invariant violations still end startup: they are this program being wrong,
// not a store being away.
func TestStartStillEndsOnAnInvariantViolationReadingTheAssignment(t *testing.T) {
	queryGroup := execution.QueryGroupIdentity("query-group-1")
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{queryGroup}}
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{queryGroup}, runner: newFakePhaseTwoQueryGroup()}
	invariant := newPhaseTwoInvariantError("phase-two production Assignment identity mismatch")
	owner.injectFailure("assigned", invariant, 1)
	bundle := mustPhaseTwoWorkerBundle(t, validGoAccessRuntimeConfig(), newPhaseTwoApplicationHealth(), control, owner)
	defer func() { _ = bundle.Shutdown(context.Background()) }()
	if err := bundle.Start(context.Background()); !errors.Is(err, invariant) {
		t.Fatalf("Start = %v, want the invariant violation", err)
	}
}

// A lease another replica took between acquiring and publishing is the
// tick's "stepped down", not an error: Start carries on as a follower and
// reads the Assignment the new Leader publishes.
func TestStartCarriesOnAsAFollowerWhenItsFenceIsStaleAtPublish(t *testing.T) {
	queryGroup := execution.QueryGroupIdentity("query-group-1")
	runner := newFakePhaseTwoQueryGroup()
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{queryGroup}}
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{queryGroup}, runner: runner}
	owner.injectFailure("publish", ownership.ErrStaleFence, 1)
	bundle := mustPhaseTwoWorkerBundle(t, validGoAccessRuntimeConfig(), newPhaseTwoApplicationHealth(), control, owner)
	defer func() { _ = bundle.Shutdown(context.Background()) }()
	if err := bundle.Start(context.Background()); err != nil {
		t.Fatalf("Start ended on a stale fence at publish: %v", err)
	}
	waitSignal(t, runner.leaseStarted, "the assigned Query Group is opened")
	bundle.mu.RLock()
	leader := bundle.controlLeader
	bundle.mu.RUnlock()
	if leader {
		t.Fatal("still the Control Leader after its fence went stale")
	}
}
