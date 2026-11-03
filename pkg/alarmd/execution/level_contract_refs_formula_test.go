// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution_test

import (
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The Level contract references and the state generation are two formulas
// over one Plan, and a record is keyed by the second and held to the first.
// Moving the first alone - a new digest domain, a field added to or removed
// from either hashed shape, a changed field name - would meet every stored
// record under an unchanged key with a contract it does not match, on every
// build at once, with no formula skew to name it and no rollout to end it.
// The data axis is pinned next door (whatever moves the refs moves the
// generation); this pins the formula axis, which no input change can reach:
// both digests of a fixed Plan are frozen here.
//
// If you are here because this test failed:
//
//   - you changed how the refs are derived. The generation has to move in the
//     same release, or every deployment's records become unreadable for good.
//     Move it deliberately (the closure's domain in strategy.deriveStateCompatibilityHash
//     is the place), accept that every series re-warms once, and update both
//     digests below.
//   - you changed how the generation is derived. Records re-warm once, which
//     is the known cost; update the generation digest below.
//
// The values are this repository's fixed test Plan, not a deployment's.
func TestBothStateIdentityFormulasAreFrozenTogether(t *testing.T) {
	plan := compiledPlanWithTriggerConfig(t, json.RawMessage(`{"window_size":30,"required_anomalies":5,"step_seconds":60}`))
	refs, err := execution.DeriveRuntimeLevelContractRefs(plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 {
		t.Fatalf("the fixed Plan has %d Levels, want the one this test was frozen over", len(refs))
	}
	const (
		frozenGeneration    = "ea6cd3edce8d4e96b8733096a1548da37f29886561ac4c5ba8e5d129337fe43f"
		frozenCompatibility = "385f0e1b49dbc52ca3610c5f6186c57258d93a7a70b5eaefe644f4c41a420b08"
		frozenWarmup        = "745d9d48a7cd9813e97a7cd35d876d9d4bbb4b3a13e55bde5a32d1668b7a3557"
		frozenDetect        = "38ef7f0bf8254d618e39046d6586702b4403803d210398098397b0e4c3a56abb"
	)
	for name, pair := range map[string][2]string{
		"state generation":          {plan.StateCompatibilityHash(), frozenGeneration},
		"level state compatibility": {refs[0].LevelStateCompatibility, frozenCompatibility},
		"warmup requirement":        {refs[0].WarmupRequirementRef, frozenWarmup},
		"detect fingerprint":        {refs[0].DetectFingerprint, frozenDetect},
	} {
		if pair[0] != pair[1] {
			t.Fatalf("%s = %s, frozen as %s: read the note above this test before updating it", name, pair[0], pair[1])
		}
	}
}
