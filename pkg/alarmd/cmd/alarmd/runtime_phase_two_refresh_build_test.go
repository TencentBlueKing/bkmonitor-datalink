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
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The refresh round's build word reaches its observation, and the words the
// metric pre-creates are the control plane's, one for one.
func TestTheRefreshBuildReachesItsObservation(t *testing.T) {
	if len(observability.SourceRefreshBuilds) != len(controlplane.SourceRefreshBuilds) {
		t.Fatalf("observability has %v, the control plane %v", observability.SourceRefreshBuilds, controlplane.SourceRefreshBuilds)
	}
	for index, build := range controlplane.SourceRefreshBuilds {
		if string(observability.SourceRefreshBuilds[index]) != string(build) {
			t.Fatalf("word %d: observability %q, control plane %q", index, observability.SourceRefreshBuilds[index], build)
		}
		facts := sourceRefreshIdentity(controlplane.SourceRefreshResult{Build: build}, controlplane.SnapshotPublicationRef{})
		if string(facts.Build) != string(build) {
			t.Fatalf("a %q round is observed as %q", build, facts.Build)
		}
	}
}
