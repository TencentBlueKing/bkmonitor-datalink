// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"sort"
	"time"
)

// SourceFacts is what the control leader's last refresh round found at the
// strategy source, before any object reached the fleet.
//
// The fleet's columns hold objects that were accepted into the Active Set. A
// strategy the source listed and the round withheld -- a document without the
// identity the contract requires, a document that is not there, a query the
// compiler refused -- is in no column, so a deployment whose source withholds
// every strategy used to read as a deployment with nothing to do: HEALTHY,
// expected 0. The counts existed as metrics and the names as log lines; this
// is the copy the page can read.
//
// Everything here is derived from one round's CatalogComposition. It holds
// strategy identifiers and nothing from the documents.
type SourceFacts struct {
	// At is when the round that produced these facts ran.
	At time.Time `json:"at"`
	// Listed is how many strategies the source listed: every object the round
	// recorded a disposition for. Accepted is how many of them became Plans.
	Listed   int `json:"listed"`
	Accepted int `json:"accepted"`
	// Objects is the partition of Listed by disposition, non-zero entries only.
	Objects map[string]int `json:"objects"`
	// Withheld is every (disposition, reason) pair that kept a strategy out,
	// with its count and a bounded sample of the strategies under it, largest
	// first. The reason is carried as the control plane wrote it.
	Withheld []WithheldGroup `json:"withheld"`
	// ChangeSignalPresent says the source offered its change marker this
	// round, and ChangeSignalAgeSeconds how long ago the writer moved it.
	// Absent age with a present marker is a marker this build could not read.
	ChangeSignalPresent    bool   `json:"change_signal_present"`
	ChangeSignalAgeSeconds *int64 `json:"change_signal_age_seconds,omitempty"`
}

// WithheldGroup is one (disposition, reason) pair the round withheld
// strategies under.
type WithheldGroup struct {
	Disposition string `json:"disposition"`
	Reason      string `json:"reason"`
	Count       int    `json:"count"`
	// Samples names up to SourceSampleLimit of the strategies, smallest
	// identifier first so the same round always names the same ones. Count
	// above len(Samples) is the rest; a reader can tell a full list from a cut
	// one without a second field.
	Samples []WithheldSample `json:"samples"`
}

// WithheldSample is one withheld strategy: which one, at what scope, and the
// field the refusal was about when it was about a field.
type WithheldSample struct {
	StrategyID string `json:"strategy_id"`
	// Scope is STRATEGY or LEVEL; LevelID says which level when the latter.
	Scope     string `json:"scope,omitempty"`
	LevelID   uint32 `json:"level_id,omitempty"`
	FieldPath string `json:"field_path,omitempty"`
}

// SourceSampleLimit bounds how many strategies one withheld group names. The
// count says how many there are; the sample is for finding one.
const SourceSampleLimit = 20

// Dispositions the control plane assigns, as this package reads them. The
// strings are the control plane's; they are repeated here rather than
// imported so the fleet stays a reader of published facts.
const (
	dispositionAccepted              = "ACCEPTED"
	dispositionSourceIncomplete      = "SOURCE_INCOMPLETE"
	dispositionConfigRejected        = "CONFIG_REJECTED"
	dispositionStaleConfig           = "STALE_CONFIG"
	dispositionCapabilityUnsupported = "UNSUPPORTED_PHASE2_CAPABILITY"
)

// WithheldObject is one withheld record as the control plane hands it over.
type WithheldObject struct {
	StrategyID  string
	Scope       string
	LevelID     uint32
	Disposition string
	Reason      string
	FieldPath   string
}

// NewSourceFacts folds one round's dispositions into facts. objects is the
// partition by disposition; withheld is every object that was not accepted,
// one record each.
func NewSourceFacts(at time.Time, objects map[string]int, withheld []WithheldObject) *SourceFacts {
	facts := &SourceFacts{At: at, Objects: map[string]int{}, Withheld: []WithheldGroup{}}
	for disposition, count := range objects {
		if count == 0 {
			continue
		}
		facts.Objects[disposition] = count
		facts.Listed += count
		if disposition == dispositionAccepted {
			facts.Accepted = count
		}
	}
	type key struct{ disposition, reason string }
	groups := map[key]*WithheldGroup{}
	members := map[key][]WithheldSample{}
	for _, object := range withheld {
		id := key{object.Disposition, object.Reason}
		group := groups[id]
		if group == nil {
			group = &WithheldGroup{Disposition: object.Disposition, Reason: object.Reason}
			groups[id] = group
		}
		group.Count++
		members[id] = append(members[id], WithheldSample{StrategyID: object.StrategyID, Scope: object.Scope,
			LevelID: object.LevelID, FieldPath: object.FieldPath})
	}
	for id, group := range groups {
		sample := members[id]
		sort.Slice(sample, func(i, j int) bool {
			if sample[i].StrategyID != sample[j].StrategyID {
				return sortableStrategyID(sample[i].StrategyID) < sortableStrategyID(sample[j].StrategyID)
			}
			return sample[i].LevelID < sample[j].LevelID
		})
		if len(sample) > SourceSampleLimit {
			sample = sample[:SourceSampleLimit]
		}
		group.Samples = sample
		facts.Withheld = append(facts.Withheld, *group)
	}
	sort.Slice(facts.Withheld, func(i, j int) bool {
		if facts.Withheld[i].Count != facts.Withheld[j].Count {
			return facts.Withheld[i].Count > facts.Withheld[j].Count
		}
		if facts.Withheld[i].Disposition != facts.Withheld[j].Disposition {
			return facts.Withheld[i].Disposition < facts.Withheld[j].Disposition
		}
		return facts.Withheld[i].Reason < facts.Withheld[j].Reason
	})
	return facts
}

// sortableStrategyID orders identifiers numerically where they are numbers,
// which strategy identifiers are; "9" then sorts before "81" rather than
// after it, and a reader comparing the sample against the console finds them
// in the console's order.
func sortableStrategyID(id string) string {
	const width = 20
	if len(id) >= width {
		return id
	}
	padded := make([]byte, width-len(id), width)
	for index := range padded {
		padded[index] = '0'
	}
	return string(append(padded, id...))
}

// WithheldCount is how many listed strategies the round kept out of detection
// under the given dispositions. STALE_CONFIG is not among a caller's usual
// choices: such a strategy runs its last good Plan and is withheld only from
// its own change.
func (facts *SourceFacts) WithheldCount(dispositions ...string) int {
	if facts == nil {
		return 0
	}
	total := 0
	for _, disposition := range dispositions {
		total += facts.Objects[disposition]
	}
	return total
}

// Blocked says the source lists strategies and the round accepted none of
// them: the whole source is being held at the configuration step. This is the
// one reading the verdict acts on. A source with some strategies withheld is
// a line on the first screen with its owner; a source with all of them
// withheld is a deployment that detects nothing, whatever the badge would
// otherwise say.
func (facts *SourceFacts) Blocked() bool {
	return facts != nil && facts.Listed > 0 && facts.Accepted == 0
}

// Groups returns the withheld groups under one disposition, in the facts'
// order.
func (facts *SourceFacts) Groups(disposition string) []WithheldGroup {
	if facts == nil {
		return nil
	}
	var groups []WithheldGroup
	for _, group := range facts.Withheld {
		if group.Disposition == disposition {
			groups = append(groups, group)
		}
	}
	return groups
}
