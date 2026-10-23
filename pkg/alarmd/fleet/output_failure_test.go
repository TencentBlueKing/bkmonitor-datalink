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

// outputFailingObject runs an object through rounds whose event write fails
// with the given words and kind, the way the coordinator emits them: the
// event_acked observation carrying the error, the round's reason and the
// kind the sink's error said, then the terminal naming the reason alone.
func outputFailingObject(t *testing.T, text, kind string) Anomaly {
	t.Helper()
	tracker := newTracker(t, &clock{at: now})
	for round := 0; round < DefaultDegradedRounds; round++ {
		slot := int64(1_700_000_000 + 60*round)
		tracker.Observe(context.Background(), observability.Observation{
			Component: observability.ComponentOutput, Stage: observability.StageEventACKed,
			Result: observability.ResultFailed, ReasonCode: "OUTPUT_ACK_UNKNOWN", Err: errors.New(text), OutputFailureKind: kind,
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

// A failed event write is read by the kind the sink's error carried, not by
// its words and not by its code: every case below arrives under the same
// OUTPUT_ACK_UNKNOWN, and its words are chosen to say the opposite of its
// kind - the words used to decide, and a Redis's EOF read as Kafka's.
func TestAFailedEventWriteIsReadByItsKindNotItsWords(t *testing.T) {
	for _, test := range []struct {
		name, text, kind string
		dependency       Dependency
		class            Class
		ours             bool
	}{
		{"the client refused, words of a broker", "kafka server: Request was for a topic or partition that does not exist",
			observability.OutputFailureClientRejected, DependencyNone, ClassContract, true},
		{"no answer, words of a client refusal", "kafka: invalid configuration (Producing headers requires Kafka at least v0.11)",
			observability.OutputFailureAckUnknown, DependencyKafka, ClassUnavailable, false},
		{"the broker answered no", "kafka server: The client is not authorized to access this topic.",
			observability.OutputFailureBrokerRefused, DependencyKafka, ClassUnavailable, false},
		{"the sink was not open", "output sink is not open",
			observability.OutputFailureSinkNotOpen, DependencyKafka, ClassUnavailable, false},
		{"the snapshot store, words of a broker connection", "legacy conversion failed: EOF",
			observability.OutputFailureSnapshotStore, DependencyRedis, ClassUnavailable, false},
		{"no kind, words of a broker connection", "dial tcp 192.0.2.10:9092: connect: connection refused",
			"", DependencyUnlocated, ClassUnlocated, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			row := outputFailingObject(t, test.text, test.kind)
			if row.Failure == nil || row.Failure.Stage != observability.QueryFailureStageOutput || row.Failure.Code != "OUTPUT_ACK_UNKNOWN" ||
				row.Failure.Text != test.text {
				t.Fatalf("failure = %+v, want the output stage under the round's code with the words kept for the reader", row.Failure)
			}
			if row.Failure.Kind != test.kind {
				t.Fatalf("failure kind %q, want %q", row.Failure.Kind, test.kind)
			}
			want := test.kind
			if want == "" {
				want = observability.OutputFailureUnknown
			}
			b := row.Blocked
			if b == nil || b.Stage != StageCommit || b.Dependency != test.dependency || b.Class != test.class || b.DependencyEvidence != want {
				t.Fatalf("blocked = %+v, want commit / %s / %s, evidence %s", b, test.class, test.dependency, want)
			}
			if (row.Internal != nil) != test.ours {
				t.Fatalf("internal failure = %+v, want filed as this deployment's own: %v", row.Internal, test.ours)
			}
			wantCheck := CheckDependencyDown
			if test.ours {
				wantCheck = CheckDefect
			}
			if row.Finding.Check != wantCheck {
				t.Fatalf("finding = %+v, want %s", row.Finding, wantCheck)
			}
		})
	}
}

// The reading's vocabulary is closed: the evidence list is the two naming
// words and the sink's kinds, and the word the text reading used for a
// broker is gone.
func TestTheOutputEvidenceWordsAreClosed(t *testing.T) {
	want := append([]string{"code", "text"}, observability.OutputFailureKinds...)
	if strings.Join(DependencyEvidences, ",") != strings.Join(want, ",") {
		t.Fatalf("DependencyEvidences = %v, want %v", DependencyEvidences, want)
	}
	for _, word := range DependencyEvidences {
		if word == "broker_error" {
			t.Fatal("the retired text-reading word broker_error is still an evidence word")
		}
	}
	for kind := range outputFailureReading {
		if !validOutputFailureKind(kind) || kind == observability.OutputFailureUnknown {
			t.Errorf("reading table names %q, which is not a located kind", kind)
		}
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
			row := statedRefusal(t, test.word, test.sentence)
			if row.Failure == nil || row.Failure.Code != test.word || row.Failure.Text != test.sentence {
				t.Fatalf("failure = %+v, want the sink's word and its bare sentence, not the chain", row.Failure)
			}
			if row.Internal == nil {
				t.Fatal("a refusal the sink stated was not filed as this deployment's own")
			}
			// The word decides the kind; the word's own reading in the table
			// -- commit, no dependency, its class -- stands.
			if b := row.Blocked; b == nil || b.DependencyEvidence != observability.OutputFailureClientRejected || b.Dependency != DependencyNone || b.Class != test.class || b.Stage != StageCommit {
				t.Fatalf("blocked = %+v, want client_rejected by the sink's own word with the word's reading (%s)", row.Blocked, test.class)
			}
			if row.Finding.Check != CheckDefect || row.Finding.Owner != OwnerAlarmd {
				t.Fatalf("finding = %+v, want the sink's refusal on the defect line", row.Finding)
			}
		})
	}
}

// A lease too short to start a batch is the sink's own named code and
// carries no kind: its row keeps the code's reading - commit, unavailable -
// and no evidence word, rather than an "unknown" that would claim more than
// the code does.
func TestALeaseDeferralRowIsReadByItsCodeWithNoEvidenceWord(t *testing.T) {
	tracker := newTracker(t, &clock{at: now})
	for round := 0; round < DefaultDegradedRounds; round++ {
		slot := int64(1_700_000_000 + 60*round)
		tracker.Observe(context.Background(), observability.Observation{
			Component: observability.ComponentOutput, Stage: observability.StageEventACKed,
			Result: observability.ResultFailed, ReasonCode: "OUTPUT_LEASE_EXPIRING", Err: errors.New("the lease has 1s left and the batch needs 1m"),
			Trace: observability.TraceFields{QueryGroupKey: "qg-lease", EvaluationTime: slot},
		})
		tracker.Observe(context.Background(), observability.Observation{
			ExecuteOutcome: "error", ReasonCode: "OUTPUT_LEASE_EXPIRING", Err: errors.New("the lease has 1s left and the batch needs 1m"),
			Trace: observability.TraceFields{QueryGroupKey: "qg-lease", EvaluationTime: slot},
		})
	}
	anomalies := tracker.Anomalies()
	if len(anomalies) != 1 {
		t.Fatalf("anomalies = %+v", anomalies)
	}
	Attribute(anomalies, now)
	b := anomalies[0].Blocked
	if b == nil || b.Stage != StageCommit || b.Class != ClassUnavailable || b.DependencyEvidence != "" {
		t.Fatalf("blocked = %+v, want commit / unavailable by the code, and no evidence word", b)
	}
}
