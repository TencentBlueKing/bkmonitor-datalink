// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

// The kinds a failed write of the round's events is, as the error that the
// sink returns says by its type. Several of them share one completion reason
// (OUTPUT_ACK_UNKNOWN: the state is not written and the Slot is done again)
// and differ in what a reader should do: a broker that may have taken the
// write, a broker that answered it did not, a sink that never asked a
// broker, a Redis the compatible protocol's snapshot store lives on. They
// used to be guessed from the error's words. Closed: a reader shows these
// words and no others.
const (
	// OutputFailureAckUnknown: a broker was asked and whether the records
	// landed is not known - no answer, a timeout, an answer that the write
	// may have been taken. Kafka's.
	OutputFailureAckUnknown = "ack_unknown"
	// OutputFailureBrokerRefused: the broker answered, for every record that
	// failed, with an error that says the record was not written. Kafka's.
	OutputFailureBrokerRefused = "broker_refused"
	// OutputFailureClientRejected: refused before sending - the converter,
	// the client's own checks - or refused for good by the producer, in
	// whole or in part. This deployment's; a retry meets the same refusal.
	OutputFailureClientRejected = "client_rejected"
	// OutputFailureSnapshotStore: the compatible protocol's snapshot store
	// did not take the snapshot the message needs. Redis's.
	OutputFailureSnapshotStore = "snapshot_store"
	// OutputFailureSinkNotOpen: the sink is not open, or already closed, and
	// no broker was asked.
	OutputFailureSinkNotOpen = "sink_not_open"
	// OutputFailureUnknown: the error carries no kind - the caller's own
	// context, a failure from outside the sink, or a row from a publisher
	// that predates the kinds. No claim beyond that.
	//
	// Retire when: the upstream snapshot after the one that adds the kinds is
	// merged, the older-publisher case is gone; the word stays for errors
	// that carry no kind.
	OutputFailureUnknown = "unknown"
)

// OutputFailureKinds is every word OutputFailureKindOf can return.
var OutputFailureKinds = []string{OutputFailureAckUnknown, OutputFailureBrokerRefused, OutputFailureClientRejected,
	OutputFailureSnapshotStore, OutputFailureSinkNotOpen, OutputFailureUnknown}

// outputFailurePrecedence is the order in which a kind found anywhere in an
// error's chain decides: an error that wraps another names the outer
// circumstance, the inner one is the more specific cause. A dependency error
// wrapping a snapshot store failure is the store's; a sink that was not
// open asked nobody, whatever else the chain says; a client refusal is the
// cause of the delivery failure that reports it.
var outputFailurePrecedence = []string{OutputFailureSnapshotStore, OutputFailureSinkNotOpen, OutputFailureClientRejected,
	OutputFailureBrokerRefused, OutputFailureAckUnknown}

// OutputFailureKindOf is the kind a failed write's error carries: the first
// word of outputFailurePrecedence that any error in its chain - through
// Unwrap() error and Unwrap() []error - says by OutputFailureKind(), and
// OutputFailureUnknown when none does.
func OutputFailureKindOf(err error) string {
	if err == nil {
		return OutputFailureUnknown
	}
	found := map[string]bool{}
	collectOutputFailureKinds(err, found, 0)
	for _, kind := range outputFailurePrecedence {
		if found[kind] {
			return kind
		}
	}
	return OutputFailureUnknown
}

// outputFailureChainDepth bounds the walk: a chain is a handful of wrappers,
// and a cycle must not hang the write that failed.
const outputFailureChainDepth = 64

func collectOutputFailureKinds(err error, found map[string]bool, depth int) {
	if err == nil || depth > outputFailureChainDepth {
		return
	}
	if kinded, ok := err.(interface{ OutputFailureKind() string }); ok {
		found[kinded.OutputFailureKind()] = true
	}
	switch wrapped := err.(type) {
	case interface{ Unwrap() []error }:
		for _, inner := range wrapped.Unwrap() {
			collectOutputFailureKinds(inner, found, depth+1)
		}
	case interface{ Unwrap() error }:
		collectOutputFailureKinds(wrapped.Unwrap(), found, depth+1)
	}
}
