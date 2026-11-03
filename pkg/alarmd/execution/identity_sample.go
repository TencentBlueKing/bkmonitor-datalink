// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

// IdentitySample is one series a Plan's target refused because its key could
// not be built (target_key_missing): the target's identity fields as the
// series carries them, and the canonical instance fields beside them, names
// and values only.
type IdentitySample struct {
	Fields []IdentityField `json:"fields"`
}

// IdentityField is one field of an IdentitySample. Expected is set for the
// target's model gate (the value the gate requires); a key field has none.
// Present says the series carries the field; Actual is its value, cut to
// IdentityValueBytes. Canonical marks the instance fields the series may
// carry beside the target's own (model_id, model_inst_id).
type IdentityField struct {
	Name      string `json:"name"`
	Expected  string `json:"expected,omitempty"`
	Actual    string `json:"actual,omitempty"`
	Present   bool   `json:"present"`
	Canonical bool   `json:"canonical,omitempty"`
}

// The bounds of the identity evidence one query keeps: a few samples, each
// value cut, so a round that refuses every series costs a fixed few bytes.
const (
	IdentitySamplesPerQuery = 3
	IdentityValueBytes      = 64
)

// CanonicalIdentityFields are the instance fields a series may carry beside
// its target's own, shown on a sample so a reader sees whether the data does
// name the instance, only not the way the target reads it.
var CanonicalIdentityFields = []string{"model_id", "model_inst_id"}

// IdentityEvidence is what a round whose every withheld series was one its
// target's identity could not read carries to the tracker: the withheld
// series by reason -- definitively outside the target, key missing, and the
// rest -- and the samples.
type IdentityEvidence struct {
	Withheld      uint64           `json:"withheld"`
	OutsideTarget uint64           `json:"outside_target"`
	KeyMissing    uint64           `json:"key_missing"`
	Samples       []IdentitySample `json:"samples,omitempty"`
}
