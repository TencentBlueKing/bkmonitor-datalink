// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// With no placement injected - production's - the Leader places on what it
// always placed on: every worker of the expected compatibility, the same
// eligibility value it built before the seam existed.
func TestNoInjectedPlacementIsTheCompatibilityAlone(t *testing.T) {
	expected, err := phaseTwoExpectedWorkerCompatibility(config.Default())
	if err != nil {
		t.Fatal(err)
	}
	got, err := phaseTwoWorkerEligibility(expected, nil)
	if err != nil {
		t.Fatal(err)
	}
	want, err := scheduler.NewStaticWorkerEligibility(expected)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("eligibility with no placement = %#v, want the compatibility alone %#v", got, want)
	}
}

// An injected placement narrows the compatibility and does not replace it:
// a Query Group pinned to worker-b is not placed on worker-a, nor on a
// worker-b of another profile. The two-worker cases cannot show this on
// their own - the Leader's rendezvous may pick the pinned worker anyway.
func TestAnInjectedPlacementNarrowsTheCompatibility(t *testing.T) {
	expected, err := phaseTwoExpectedWorkerCompatibility(config.Default())
	if err != nil {
		t.Fatal(err)
	}
	placement := &pinnedWorkers{pins: map[execution.QueryGroupIdentity]string{"qg": "worker-b"}}
	eligibility, err := phaseTwoWorkerEligibility(expected, placement)
	if err != nil {
		t.Fatal(err)
	}
	worker := func(id string, compatibility ownership.WorkerCompatibility) ownership.WorkerRegistration {
		return ownership.WorkerRegistration{WorkerID: id, DeploymentProfile: compatibility.DeploymentProfile,
			CapabilitiesDigest: compatibility.CapabilitiesDigest, ExecutionContractDigest: compatibility.ExecutionContractDigest}
	}
	other := expected
	other.DeploymentProfile += "-other"
	now := time.Unix(1_700_000_000, 0)
	if eligibility.Eligible("qg", worker("worker-a", expected), now) || !eligibility.Eligible("qg", worker("worker-b", expected), now) ||
		eligibility.Eligible("qg", worker("worker-b", other), now) {
		t.Fatal("the eligibility under an injected placement is not where both the placement and the compatibility are")
	}
}
