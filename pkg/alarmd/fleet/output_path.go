// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

// Whether any event can leave this deployment as the standard raw event.
//
// The answer is decided by two facts the fleet already holds and had never
// put side by side: the protocol choice each replica runs with, and how many
// of the leader's Plans carry the strategy revision the automatic choice
// needs. A deployment whose source publishes no revisions sends every event
// the Python-compatible way; on a live deployment that took two lines of
// investigation, a gate counter that only ever said "not gated" and a Kafka
// read to establish. It is one sentence, and it belongs on the first screen.

// OutputPathFacts is that sentence's arithmetic.
type OutputPathFacts struct {
	// Plans and RevisionedPlans are the leader's last round's: every accepted
	// Plan, and those whose strategy carries an authoritative revision. Zero
	// and zero with Reason SOURCE_UNKNOWN is a round that did not report them.
	Plans           int `json:"plans"`
	RevisionedPlans int `json:"revisioned_plans"`
	// StandardRawEventReachable is whether at least one Plan publishes as the
	// standard raw event under the choice the replicas run with, and Reason
	// says why or why not, one of OutputPathReasons.
	StandardRawEventReachable bool   `json:"standard_raw_event_reachable"`
	Reason                    string `json:"reason"`
	// Detail is the replica's own sentence when the reason is the brokers':
	// which broker accepts what, and what the standard raw event needs. Empty
	// for the other reasons, whose arithmetic is the fields above.
	Detail string `json:"detail,omitempty"`
}

// The reasons the standard output path is or is not reachable. Closed: a
// reader shows these words and no others.
const (
	// OutputPathReachable: some Plan publishes as the standard raw event.
	OutputPathReachable = "REACHABLE"
	// OutputPathNoRevisionedPlans: the choice would publish natively, and no
	// Plan carries the revision that makes a strategy publishable that way.
	OutputPathNoRevisionedPlans = "NO_REVISIONED_PLANS"
	// OutputPathProtocolLegacy: every replica that said runs the forced
	// compatible choice, under which nothing is published natively.
	OutputPathProtocolLegacy = "PROTOCOL_LEGACY"
	// OutputPathProtocolUnsupportedByBroker: a replica asked its brokers which
	// protocol they accept and came away on one that cannot carry the record
	// header the standard raw event puts the tenant in. The compatible
	// output still leaves; nothing native can, whatever the Plans carry.
	OutputPathProtocolUnsupportedByBroker = "PROTOCOL_UNSUPPORTED_BY_BROKER"
	// OutputPathSourceUnknown: no counted replica published a source round
	// that reports Plan counts (an older build, or no leader heard from), so
	// the question cannot be answered and is not answered as no.
	OutputPathSourceUnknown = "SOURCE_UNKNOWN"
)

// OutputPathReasons is every word Reason can carry.
var OutputPathReasons = []string{
	OutputPathReachable, OutputPathNoRevisionedPlans, OutputPathProtocolLegacy,
	OutputPathProtocolUnsupportedByBroker, OutputPathSourceUnknown,
}

// OutputPathOf decides from the view's source round and protocol groups.
// Protocol words are read as the control plane spells them; a replica that
// published no choice is left out of the decision rather than read as any.
func OutputPathOf(view *View) OutputPathFacts {
	facts := OutputPathFacts{Reason: OutputPathSourceUnknown}
	if view.Source == nil || !view.Source.PlansKnown {
		return facts
	}
	facts.Plans, facts.RevisionedPlans = view.Source.Plans, view.Source.RevisionedPlans
	reported, legacyOnly := 0, true
	for _, group := range view.OutputProtocols {
		if group.Protocol.Configured == "" {
			continue
		}
		reported++
		if group.Protocol.Configured != "legacy" {
			legacyOnly = false
		}
	}
	if reported > 0 && legacyOnly {
		facts.Reason = OutputPathProtocolLegacy
		return facts
	}
	// A replica whose brokers cannot take the header decides it for the
	// deployment: the brokers are shared, and one replica that asked is
	// enough to know. Its own sentence about them is the detail.
	for _, replica := range view.PerReplica {
		if output := endpointByRole(replica.Dependencies, EndpointOutputKafka); brokerCappedHeaders(output) {
			facts.Reason = OutputPathProtocolUnsupportedByBroker
			facts.Detail = checkDetail(output, EndpointCheckRecordHeaders)
			return facts
		}
	}
	// Auto publishes a revisioned strategy natively; native refuses an
	// unrevisioned one; either way it is the revisioned Plans that can reach
	// the path.
	if facts.RevisionedPlans > 0 {
		facts.StandardRawEventReachable = true
		facts.Reason = OutputPathReachable
		return facts
	}
	facts.Reason = OutputPathNoRevisionedPlans
	return facts
}
