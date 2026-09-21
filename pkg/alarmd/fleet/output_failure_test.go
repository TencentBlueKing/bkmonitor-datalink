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
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The live error chain of a client that refuses to send: the worker's
// wrapping, the sink's, the decision sink's batch description, and the
// client's own configuration error at the end.
const clientRefusalText = "alarmd worker: acknowledge events: kafka trigger event sink: publish batch: kafka decision sink: send batch: " +
	"1 of 1 messages failed, first: kafka: Failed to produce message to topic alarmd_event: kafka: invalid configuration " +
	"(Producing headers requires Kafka at least v0.11) (kafka: Failed to deliver 1 messages.)"

// The live error chain of a broker that could not be reached.
const brokerErrorText = "alarmd worker: acknowledge events: kafka trigger event sink: publish batch: kafka decision sink: send batch: " +
	"kafka: client has run out of available brokers to talk to (Is your cluster reachable?)"

// outputFailingObject runs an object through rounds whose event write fails
// with the given words, the way the coordinator emits them: the event_acked
// observation carrying the error and the round's reason, then the terminal
// naming the reason alone.
func outputFailingObject(t *testing.T, text string) Anomaly {
	t.Helper()
	tracker := newTracker(t, &clock{at: now})
	for round := 0; round < DefaultDegradedRounds; round++ {
		slot := int64(1_700_000_000 + 60*round)
		tracker.Observe(context.Background(), observability.Observation{
			Component: observability.ComponentOutput, Stage: observability.StageEventACKed,
			Result: observability.ResultFailed, ReasonCode: "OUTPUT_ACK_UNKNOWN", Err: errors.New(text),
			Trace: observability.TraceFields{QueryGroupKey: "qg-output", EvaluationTime: slot},
		})
		tracker.Observe(context.Background(), observability.Observation{
			ExecuteOutcome: "error", ReasonCode: "OUTPUT_ACK_UNKNOWN", Err: errors.New(text),
			Trace: observability.TraceFields{QueryGroupKey: "qg-output", EvaluationTime: slot},
		})
	}
	anomalies := tracker.Anomalies()
	if len(anomalies) != 1 {
		t.Fatalf("anomalies = %+v, want the one failing object", anomalies)
	}
	Attribute(anomalies, now)
	return anomalies[0]
}

// The words of a failed event write travel to the row as its failure, at the
// output stage, and decide the reading: a client that refused to send is this
// deployment's own defect with no dependency named, a broker that did not
// answer is the dependency's. Both arrive under the same code, and read by
// the code alone the two were one line for an afternoon.
func TestAFailedEventWriteIsReadByItsWordsNotItsCode(t *testing.T) {
	refused := outputFailingObject(t, clientRefusalText)
	if refused.Failure == nil || refused.Failure.Stage != observability.QueryFailureStageOutput ||
		refused.Failure.Category != observability.QueryFailureCategoryOutput || refused.Failure.Code != "OUTPUT_ACK_UNKNOWN" {
		t.Fatalf("failure = %+v, want the output stage under the round's code", refused.Failure)
	}
	if !strings.Contains(refused.Failure.Text, "Producing headers requires Kafka at least v0.11") {
		t.Fatalf("failure text = %q, want the client's own sentence", refused.Failure.Text)
	}
	if refused.Internal == nil || refused.Internal.Text != refused.Failure.Text {
		t.Fatalf("internal failure = %+v, want the refusal filed as this deployment's own", refused.Internal)
	}
	if refused.Finding.Check != CheckDefect || refused.Finding.Owner != OwnerAlarmd {
		t.Fatalf("finding = %+v, want the client's refusal on the defect line, ours", refused.Finding)
	}
	if b := refused.Blocked; b == nil || b.Stage != StageCommit || b.Class != ClassContract || b.Dependency != DependencyNone ||
		b.DependencyEvidence != OutputFailureClientRejected || b.Code != "OUTPUT_ACK_UNKNOWN" {
		t.Fatalf("blocked = %+v, want commit / contract / no dependency, evidence client_rejected", refused.Blocked)
	}

	unreachable := outputFailingObject(t, brokerErrorText)
	if unreachable.Internal != nil {
		t.Fatalf("a broker that did not answer was filed as this deployment's own: %+v", unreachable.Internal)
	}
	if unreachable.Finding.Check != CheckDependencyDown {
		t.Fatalf("finding = %+v, want the broker on the dependency line", unreachable.Finding)
	}
	if b := unreachable.Blocked; b == nil || b.Stage != StageCommit || b.Class != ClassUnavailable || b.Dependency != DependencyKafka ||
		b.DependencyEvidence != OutputFailureBrokerError {
		t.Fatalf("blocked = %+v, want commit / unavailable / kafka, evidence broker_error", unreachable.Blocked)
	}

	// Words nobody has a signature for: not the broker's by default, and not
	// ours either -- unlocated, and the code's own line.
	strange := outputFailingObject(t, "alarmd worker: acknowledge events: something new happened")
	if b := strange.Blocked; b == nil || b.DependencyEvidence != OutputFailureUnknown || b.Dependency != DependencyUnlocated {
		t.Fatalf("blocked = %+v, want evidence unknown, dependency unlocated", strange.Blocked)
	}
	if strange.Finding.Check != CheckDependencyDown {
		t.Fatalf("finding = %+v, want the code's own line when the words decide nothing", strange.Finding)
	}
}

// The reading's vocabulary is closed and every word has a producer: each
// signature list reaches its kind, and the evidence list is exactly the two
// naming words plus the three kinds.
func TestOutputFailureKindsAreClosedAndEachHasASignature(t *testing.T) {
	for _, signature := range outputClientSignatures {
		if OutputFailureKind("x "+signature+" y") != OutputFailureClientRejected {
			t.Errorf("client signature %q does not read as client_rejected", signature)
		}
	}
	for _, signature := range outputBrokerSignatures {
		if OutputFailureKind("x "+signature+" y") != OutputFailureBrokerError {
			t.Errorf("broker signature %q does not read as broker_error", signature)
		}
	}
	if OutputFailureKind("") != OutputFailureUnknown || OutputFailureKind("nothing anyone wrote") != OutputFailureUnknown {
		t.Error("words without a signature must read unknown")
	}
	// A refused batch carries both a delivery failure and the refusal; the
	// refusal is the cause and wins.
	if OutputFailureKind(clientRefusalText) != OutputFailureClientRejected {
		t.Error("a delivery failure wrapping a client refusal must read as the refusal")
	}
	if len(DependencyEvidences) != 5 {
		t.Fatalf("DependencyEvidences = %v, want code, text and the three kinds", DependencyEvidences)
	}
}

// statedRefusal runs an object through rounds whose event write the sink
// refused on its own account -- reason word and bare sentence as facts on the
// failed event write -- so the word alone has to decide.
func statedRefusal(t *testing.T, word, sentence string) Anomaly {
	t.Helper()
	tracker := newTracker(t, &clock{at: now})
	chain := "alarmd worker: acknowledge events: " + word + ": " + sentence + " (event evt-1, strategy 1001, business 2, format standard_raw_event)"
	for round := 0; round < DefaultDegradedRounds; round++ {
		slot := int64(1_700_000_000 + 60*round)
		tracker.Observe(context.Background(), observability.Observation{
			Component: observability.ComponentOutput, Stage: observability.StageEventACKed,
			Result: observability.ResultFailed, ReasonCode: observability.ReasonCode(word), Err: errors.New(chain),
			OutputRejection: &observability.OutputRejectionFacts{Reason: word, Detail: sentence},
			Trace:           observability.TraceFields{QueryGroupKey: "qg-rejected", EvaluationTime: slot},
		})
		tracker.Observe(context.Background(), observability.Observation{
			ExecuteOutcome: "error", ReasonCode: observability.ReasonCode(word), Err: errors.New(chain),
			Trace: observability.TraceFields{QueryGroupKey: "qg-rejected", EvaluationTime: slot},
		})
	}
	anomalies := tracker.Anomalies()
	if len(anomalies) != 1 {
		t.Fatalf("anomalies = %+v", anomalies)
	}
	Attribute(anomalies, now)
	return anomalies[0]
}

// When the sink states its own refusal, the row carries the sentence, not the
// chain, and the reason word decides the kind whatever the sentence says --
// for both of the sink's words, each with a sentence no signature matches.
// Each word is its own case because each is its own table row: a word
// dropped from the reading's list would fall back to the sentence, and with
// an unsigned sentence read unknown while the table still said commit.
func TestTheSinksOwnRefusalIsCarriedAsFactsAndDecidesTheKind(t *testing.T) {
	for _, test := range []struct {
		word, sentence string
		class          Class
	}{
		// The converter's refusal in words no signature knows.
		{contract.ReasonOutputConversionRejected, "two levels of one decision share the severity", ClassContract},
		// The client's refusal in words no signature knows: the client's own
		// message-size error, not its configuration error. Only the word can
		// make this client_rejected.
		{contract.ReasonOutputClientRejected, "Message was too large, the client refused it before sending", ClassConfig},
	} {
		t.Run(test.word, func(t *testing.T) {
			if OutputFailureKind(test.sentence) != OutputFailureUnknown {
				t.Fatalf("fixture sentence %q matches a signature; the test needs one no signature knows", test.sentence)
			}
			row := statedRefusal(t, test.word, test.sentence)
			if row.Failure == nil || row.Failure.Code != test.word || row.Failure.Text != test.sentence {
				t.Fatalf("failure = %+v, want the sink's word and its bare sentence, not the chain", row.Failure)
			}
			if row.Internal == nil {
				t.Fatal("a refusal the sink stated was not filed as this deployment's own")
			}
			// The word decides the kind; the word's own reading in the table
			// -- commit, no dependency, its class -- stands.
			if b := row.Blocked; b == nil || b.DependencyEvidence != OutputFailureClientRejected || b.Dependency != DependencyNone || b.Class != test.class || b.Stage != StageCommit {
				t.Fatalf("blocked = %+v, want client_rejected by the sink's own word with the word's reading (%s)", row.Blocked, test.class)
			}
			if row.Finding.Check != CheckDefect || row.Finding.Owner != OwnerAlarmd {
				t.Fatalf("finding = %+v, want the sink's refusal on the defect line", row.Finding)
			}
		})
	}
}
