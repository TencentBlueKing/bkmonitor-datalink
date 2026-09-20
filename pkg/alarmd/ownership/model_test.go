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
