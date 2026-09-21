// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

// StateAlreadyAppliedSite is where an ALREADY_APPLIED was decided: at
// preflight, against the view the Slot loaded before evaluating, or at apply,
// from the bytes the CAS met when it tried to write.
type StateAlreadyAppliedSite string

const (
	StateAlreadyAppliedAtPreflight StateAlreadyAppliedSite = "preflight"
	StateAlreadyAppliedAtApply     StateAlreadyAppliedSite = "apply"
)

// StateAlreadyAppliedKind mirrors execution.StateAlreadyAppliedKind. stable is
// the retry that read the stored revision and found its own statement there;
// revision_skew is the same statement found at a revision the mutation did not
// expect -- a write that landed while its reply was lost, or one that was sent
// twice. Only revision_skew can say whether such re-sends happen and how
// often, which is why the two are never folded.
type StateAlreadyAppliedKind string

const (
	StateAlreadyAppliedStable       StateAlreadyAppliedKind = "stable"
	StateAlreadyAppliedRevisionSkew StateAlreadyAppliedKind = "revision_skew"
	// repeated_key: the same request carried the key twice and the later copy
	// met the earlier one -- a producer that made two mutations for one
	// series, not a re-sent write.
	StateAlreadyAppliedRepeatedKey StateAlreadyAppliedKind = "repeated_key"
	stateAlreadyAppliedOther       StateAlreadyAppliedKind = "other"
)

func AllStateAlreadyAppliedSites() []StateAlreadyAppliedSite {
	return []StateAlreadyAppliedSite{StateAlreadyAppliedAtPreflight, StateAlreadyAppliedAtApply}
}

func AllStateAlreadyAppliedKinds() []StateAlreadyAppliedKind {
	return []StateAlreadyAppliedKind{StateAlreadyAppliedStable, StateAlreadyAppliedRevisionSkew, StateAlreadyAppliedRepeatedKey, stateAlreadyAppliedOther}
}

// NormalizeStateAlreadyAppliedKind folds a kind this build does not name into
// other, so a store that starts reporting a new kind shows as a rising other
// rather than as a label set that grows with its input.
func NormalizeStateAlreadyAppliedKind(kind StateAlreadyAppliedKind) StateAlreadyAppliedKind {
	switch kind {
	case StateAlreadyAppliedStable, StateAlreadyAppliedRevisionSkew, StateAlreadyAppliedRepeatedKey:
		return kind
	}
	return stateAlreadyAppliedOther
}

type StateAlreadyAppliedKey struct {
	Site StateAlreadyAppliedSite
	Kind StateAlreadyAppliedKind
}

// StateRevisionSkewSample is one revision_skew, kept so the log line can say
// how far the expectation was from the stored revision: the first one seen in
// the Slot, because a Slot's skews share one cause.
type StateRevisionSkewSample struct {
	Site             StateAlreadyAppliedSite `json:"site"`
	SeriesIdentity   string                  `json:"series_identity_digest"`
	ExpectedRevision uint64                  `json:"expected_revision"`
	StoredRevision   uint64                  `json:"stored_revision"`
}

// StateAlreadyAppliedFacts counts one Slot's ALREADY_APPLIED decisions by site
// and kind. Aggregated per Slot for the same reason StateWriteReuseFacts is: a
// Slot carries up to thousands of series and one observation per series would
// be the log quota.
type StateAlreadyAppliedFacts struct {
	Counts map[StateAlreadyAppliedKey]int64 `json:"counts"`
	Skew   *StateRevisionSkewSample         `json:"skew,omitempty"`
}

func (facts *StateAlreadyAppliedFacts) Record(site StateAlreadyAppliedSite, kind StateAlreadyAppliedKind, series string, expected, stored uint64) {
	kind = NormalizeStateAlreadyAppliedKind(kind)
	if facts.Counts == nil {
		facts.Counts = map[StateAlreadyAppliedKey]int64{}
	}
	facts.Counts[StateAlreadyAppliedKey{Site: site, Kind: kind}]++
	if (kind == StateAlreadyAppliedRevisionSkew || kind == StateAlreadyAppliedRepeatedKey) && facts.Skew == nil {
		facts.Skew = &StateRevisionSkewSample{Site: site, SeriesIdentity: series, ExpectedRevision: expected, StoredRevision: stored}
	}
}

func (facts StateAlreadyAppliedFacts) Empty() bool {
	return len(facts.Counts) == 0
}
