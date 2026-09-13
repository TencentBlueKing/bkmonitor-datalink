// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package ownership_test

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// A registration carries the worker's acknowledgement and load only when the
// worker reports them. One written without them decodes with neither, which
// is how a registration from before the fields existed reads to a new leader:
// unknown, not lagging and not idle. One written with them comes back as
// written, and an impossible load is refused where every other field is.
func TestWorkerRegistrationCarriesAcknowledgementAndLoadOnlyWhenReported(t *testing.T) {
	base := ownership.WorkerRegistration{
		WorkerID: "worker-1", AssignmentReadiness: ownership.WorkerReady, DependencyStatus: ownership.DependencyHealthy,
		DeploymentProfile: "profile", CapabilitiesDigest: strings.Repeat("a", 64), ExpiresAt: time.Unix(1_700_000_000, 0).UTC(),
	}
	payload, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), `"applied"`) || strings.Contains(string(payload), `"load"`) {
		t.Fatalf("a registration without acknowledgement wrote the fields anyway: %s", payload)
	}
	var decoded ownership.WorkerRegistration
	if err := json.Unmarshal(payload, &decoded); err != nil || decoded.Applied != nil || decoded.Load != nil {
		t.Fatalf("decoded = %+v err=%v, want neither acknowledgement nor load", decoded, err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("a registration without the fields must stay valid: %v", err)
	}

	full := base
	full.Applied = &ownership.AppliedControlFacts{ActivationRecordRevision: 7}
	full.Load = &ownership.WorkerLoad{
		OwnedQueryGroups: 3, PermitsHeld: 1, PermitBudget: 4, PermitSeconds: 12.5, Waiting: 0,
		MemoryUsedBytes: 10, MemoryLimitBytes: 100,
	}
	if err := full.Validate(); err != nil {
		t.Fatal(err)
	}
	payload, err = json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	var roundTripped ownership.WorkerRegistration
	if err := json.Unmarshal(payload, &roundTripped); err != nil || !reflect.DeepEqual(roundTripped, full) {
		t.Fatalf("round trip = %+v err=%v, want %+v", roundTripped, err, full)
	}

	for name, load := range map[string]*ownership.WorkerLoad{
		"negative owned":     {OwnedQueryGroups: -1},
		"negative permits":   {PermitsHeld: -1},
		"negative budget":    {PermitBudget: -1},
		"negative waiting":   {Waiting: -1},
		"negative seconds":   {PermitSeconds: -1},
		"unmeasured seconds": {PermitSeconds: math.NaN()},
	} {
		invalid := full
		invalid.Load = load
		if err := invalid.Validate(); err == nil {
			t.Fatalf("%s: an impossible load was accepted", name)
		}
	}
}
