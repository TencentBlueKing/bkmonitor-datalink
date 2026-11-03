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
	"time"

	model "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// How far the content contract has reached the Assignment records.
//
// Each record may name the content its Query Group runs under, so a worker
// whose view is behind cannot run new time against an old Segment. Whether
// the records actually carry it was, after the release that introduced it,
// a question answered by a script in a Pod: SCAN the records, HMGET three
// fields, count. The leader reads every record once a round to settle it
// anyway; these are the counts of that read, so the page answers the
// question and no Redis is opened for it.

// AssignmentScopeFacts is one reconcile round's census of the content scope
// on the Assignment records, as the leader settled them. Every record the
// round read lands in exactly one of the five buckets under a declaring
// round; under any other policy the two plain tallies are all there is, and
// Policy says why.
type AssignmentScopeFacts struct {
	At time.Time `json:"at"`
	// Policy is what the round did about scopes, one of AssignmentScopePolicies.
	Policy string `json:"policy"`
	// Total is the records the round read; Declared how many name a content,
	// Pending how many carry a change towards another. Counted under every
	// policy.
	Total    int `json:"total"`
	Declared int `json:"declared"`
	Pending  int `json:"pending"`
	// Under a declaring round, the partition of Total: Current names the
	// content the Query Group is published with and pends nothing; Moving
	// pends towards it (a change under a live lease, waiting its effective
	// time); Stale names other content with no change towards the current
	// one; Undeclared names nothing; ContentUnknown is a Query Group whose
	// current content the round did not have, left as it was.
	Current        int `json:"current"`
	Moving         int `json:"moving"`
	Stale          int `json:"stale"`
	Undeclared     int `json:"undeclared"`
	ContentUnknown int `json:"content_unknown"`
}

// The words Policy carries, as the reconcile round spells its decision.
const (
	// AssignmentScopePolicyDeclared: every ready worker declares the
	// capability, so the round brings each record to its current content.
	AssignmentScopePolicyDeclared = "declared"
	// AssignmentScopePolicyWithdrawn: a ready worker does not declare it, so
	// the round withdraws every scope.
	AssignmentScopePolicyWithdrawn = "withdrawn"
	// AssignmentScopePolicyUntouched: the round left scopes as they were --
	// it could not read the current content this round.
	AssignmentScopePolicyUntouched = "untouched"
)

// AssignmentScopePolicies is every word Policy can carry.
var AssignmentScopePolicies = []string{AssignmentScopePolicyDeclared, AssignmentScopePolicyWithdrawn, AssignmentScopePolicyUntouched}

// AssignmentScopeOf counts one round's settled records. digests is the
// content each Query Group is published with, consulted only under a
// declaring round; the buckets are decided the way the round decided them,
// so a record the round left alone because it had no content for it is
// counted as such and not as stale.
func AssignmentScopeOf(at time.Time, policy string, digests map[model.QueryGroupIdentity]string, records map[model.QueryGroupIdentity]ownership.AssignmentRecord) *AssignmentScopeFacts {
	facts := &AssignmentScopeFacts{At: at, Policy: policy, Total: len(records)}
	for queryGroup, record := range records {
		if record.ContentScope != "" {
			facts.Declared++
		}
		if record.PendingContentScope != "" {
			facts.Pending++
		}
		if policy != AssignmentScopePolicyDeclared {
			continue
		}
		digest, known := digests[queryGroup]
		switch {
		case !known || digest == "":
			facts.ContentUnknown++
		case record.ContentScope == digest && record.PendingContentScope == "":
			facts.Current++
		case record.PendingContentScope == digest:
			facts.Moving++
		case record.ContentScope == "":
			facts.Undeclared++
		default:
			facts.Stale++
		}
	}
	if policy != AssignmentScopePolicyDeclared {
		facts.Undeclared = facts.Total - facts.Declared
	}
	return facts
}

// Consistent is the census identity a reader checks before trusting any one
// bucket: under a declaring round the five buckets sum to Total; under any
// other, Declared and Undeclared do.
func (facts *AssignmentScopeFacts) Consistent() bool {
	if facts == nil {
		return false
	}
	if facts.Policy == AssignmentScopePolicyDeclared {
		return facts.Total == facts.Current+facts.Moving+facts.Stale+facts.Undeclared+facts.ContentUnknown
	}
	return facts.Total == facts.Declared+facts.Undeclared
}

// AssignmentSweepFacts is the leader's last sweep of the Assignment records:
// what it walked, what it found retired and what it did about them, or why
// it failed. The census above counts the round's own Query Groups and never
// sees a retired record; the sweep is the only reading of those, and on one
// deployment it ran, reclaimed six, and no line said so. With this beside
// the census, "never swept", "swept and found nothing" and "swept and
// reclaimed" are three different readings.
type AssignmentSweepFacts struct {
	At time.Time `json:"at"`
	// Result is success or failed; Reason the failure's word when failed.
	Result string `json:"result"`
	Reason string `json:"reason,omitempty"`
	// Scanned is how many records the key space held; Retired how many named
	// a Query Group the leader no longer runs; of those, Reclaimed were
	// deleted, HeldByLease left for a live lease, Changed left because the
	// record moved under the sweep.
	Scanned         int     `json:"scanned"`
	Retired         int     `json:"retired"`
	Reclaimed       int     `json:"reclaimed"`
	HeldByLease     int     `json:"held_by_lease"`
	Changed         int     `json:"changed"`
	DurationSeconds float64 `json:"duration_seconds"`
}

// Consistent is the sweep's own identity: every retired record was
// reclaimed, held or changed.
func (facts *AssignmentSweepFacts) Consistent() bool {
	return facts != nil && facts.Retired == facts.Reclaimed+facts.HeldByLease+facts.Changed
}
