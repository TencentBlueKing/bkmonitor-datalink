// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

// StateVersionConflictKind mirrors execution.StateVersionConflictKind: which
// comparison refused a STATE_VERSION_CONFLICT. missing is a key the mutation
// expected at a revision and did not find, which is what a TTL that ran out
// between the Slot's read and its write leaves; revision_moved is a key
// another write advanced after the read; revision_reset is a key found at a
// revision below the one expected, so it was gone and written fresh since
// the read; same_version_other_statement is the expected revision holding
// this window's ApplyVersion with a different digest, two evaluations of one
// window disagreeing; version_incomparable is a view that reached the
// classifier without an ordering. The status alone reads the same for all of
// them and each points at a different fix.
type StateVersionConflictKind string

const (
	StateVersionConflictMissing                   StateVersionConflictKind = "missing"
	StateVersionConflictRevisionMoved             StateVersionConflictKind = "revision_moved"
	StateVersionConflictRevisionReset             StateVersionConflictKind = "revision_reset"
	StateVersionConflictSameVersionOtherStatement StateVersionConflictKind = "same_version_other_statement"
	StateVersionConflictVersionIncomparable       StateVersionConflictKind = "version_incomparable"
	stateVersionConflictOther                     StateVersionConflictKind = "other"
)

func AllStateVersionConflictKinds() []StateVersionConflictKind {
	return []StateVersionConflictKind{StateVersionConflictMissing, StateVersionConflictRevisionMoved, StateVersionConflictRevisionReset,
		StateVersionConflictSameVersionOtherStatement, StateVersionConflictVersionIncomparable, stateVersionConflictOther}
}

// NormalizeStateVersionConflictKind folds a kind this build does not name,
// including the empty kind of a store that does not say, into other. A
// conflict without a kind must not pose as any of the kinds a reader acts
// on, and other rising is a store that has stopped saying.
func NormalizeStateVersionConflictKind(kind StateVersionConflictKind) StateVersionConflictKind {
	switch kind {
	case StateVersionConflictMissing, StateVersionConflictRevisionMoved, StateVersionConflictRevisionReset,
		StateVersionConflictSameVersionOtherStatement, StateVersionConflictVersionIncomparable:
		return kind
	}
	return stateVersionConflictOther
}

// StateVersionConflictKey is one counted pair: where the conflict was decided
// (the same two sites an ALREADY_APPLIED is decided at) and which comparison
// refused it.
type StateVersionConflictKey struct {
	Site StateAlreadyAppliedSite
	Kind StateVersionConflictKind
}

// StateVersionConflictSample is the first conflict of one kind seen in the
// Slot, with the values the comparison used. One per kind rather than one
// overall because a Slot with two kinds has two causes, and the sample is
// what says which key and how far off; a Slot's conflicts of one kind share
// one cause.
type StateVersionConflictSample struct {
	Site              StateAlreadyAppliedSite  `json:"site"`
	Kind              StateVersionConflictKind `json:"kind"`
	SeriesIdentity    string                   `json:"series_identity_digest"`
	ExpectedRevision  uint64                   `json:"expected_revision"`
	StoredRevision    uint64                   `json:"stored_revision"`
	VersionComparison string                   `json:"stored_version_comparison,omitempty"`
	RepeatedKey       bool                     `json:"repeated_key,omitempty"`
}

// StateVersionConflictFacts counts one Slot's STATE_VERSION_CONFLICT refusals
// by site and kind, aggregated per Slot for the same reason
// StateAlreadyAppliedFacts is: one line per series would be the log quota.
type StateVersionConflictFacts struct {
	Counts  map[StateVersionConflictKey]int64 `json:"counts"`
	Samples []StateVersionConflictSample      `json:"samples,omitempty"`
}

func (facts *StateVersionConflictFacts) Record(site StateAlreadyAppliedSite, kind StateVersionConflictKind, series string,
	expected, stored uint64, comparison string, repeatedKey bool) {
	kind = NormalizeStateVersionConflictKind(kind)
	if facts.Counts == nil {
		facts.Counts = map[StateVersionConflictKey]int64{}
	}
	facts.Counts[StateVersionConflictKey{Site: site, Kind: kind}]++
	for _, sample := range facts.Samples {
		if sample.Kind == kind {
			return
		}
	}
	facts.Samples = append(facts.Samples, StateVersionConflictSample{Site: site, Kind: kind, SeriesIdentity: series,
		ExpectedRevision: expected, StoredRevision: stored, VersionComparison: comparison, RepeatedKey: repeatedKey})
}

func (facts StateVersionConflictFacts) Empty() bool {
	return len(facts.Counts) == 0
}
