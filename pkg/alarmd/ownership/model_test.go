// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package ownership

import (
	"testing"
	"time"
)

func TestWorkerCompatibilityUsesOnlyStaticDeploymentFacts(t *testing.T) {
	worker := WorkerRegistration{
		WorkerID: "worker-1", AssignmentReadiness: WorkerReady, DependencyStatus: DependencyDegraded,
		DeploymentProfile: "standard", CapabilitiesDigest: "cap-v1", ExpiresAt: time.Unix(1_700_000_000, 0),
	}
	want := WorkerCompatibility{DeploymentProfile: "standard", CapabilitiesDigest: "cap-v1"}
	if got := worker.Compatibility(); got != want {
		t.Fatalf("Compatibility() = %#v, want %#v", got, want)
	}
	if err := want.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	for _, invalid := range []WorkerCompatibility{
		{CapabilitiesDigest: "cap-v1"},
		{DeploymentProfile: "standard"},
	} {
		if err := invalid.Validate(); err == nil {
			t.Fatalf("Validate(%#v) succeeded", invalid)
		}
	}
}

func TestWorkerCompatibilityPrefersDeclaredExecutionContract(t *testing.T) {
	base := WorkerCompatibility{DeploymentProfile: "standard", CapabilitiesDigest: "resource-small", ExecutionContractDigest: "execution-v1"}
	for name, test := range map[string]struct {
		other WorkerCompatibility
		match bool
	}{
		"different resource digest":       {WorkerCompatibility{"standard", "resource-large", "execution-v1"}, true},
		"new mismatch does not fall back": {WorkerCompatibility{"standard", "resource-small", "execution-v2"}, false},
		"different profile":               {WorkerCompatibility{"another", "resource-small", "execution-v1"}, false},
		"old matching digest":             {WorkerCompatibility{"standard", "resource-small", ""}, true},
		"old different digest":            {WorkerCompatibility{"standard", "resource-large", ""}, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := base.Matches(test.other); got != test.match || test.other.Matches(base) != got {
				t.Fatalf("Matches(%+v) = %v, want symmetric %v", test.other, got, test.match)
			}
		})
	}
	old := WorkerCompatibility{DeploymentProfile: "standard", CapabilitiesDigest: "resource-small"}
	if !old.Matches(old) || old.Matches(WorkerCompatibility{DeploymentProfile: "standard", CapabilitiesDigest: "different"}) {
		t.Fatal("legacy digest comparison changed")
	}
}
