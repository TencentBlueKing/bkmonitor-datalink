// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane_test

import (
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// The repository reports the record revision of the Activation it last
// parsed, which is the one the process executes by: zero before it parsed
// one, the published record's revision after.
func TestRepositoryReportsTheActivationRevisionItLastParsed(t *testing.T) {
	harness, state, _, _ := activatedHarness(t)
	if applied := harness.repository.AppliedActivationRevision(); applied != state.RecordRevision {
		t.Fatalf("applied revision after activation = %d, want the record's %d", applied, state.RecordRevision)
	}
	fresh, err := controlplane.NewRedisCatalogRepository(harness.client, harness.prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if applied := fresh.AppliedActivationRevision(); applied != 0 {
		t.Fatalf("a repository that parsed nothing reports %d, want 0", applied)
	}
	if _, err := fresh.LoadActivation(harness.ctx); err != nil {
		t.Fatal(err)
	}
	if applied := fresh.AppliedActivationRevision(); applied != state.RecordRevision {
		t.Fatalf("applied revision after the first load = %d, want %d", applied, state.RecordRevision)
	}
}
