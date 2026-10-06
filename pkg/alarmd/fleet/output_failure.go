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
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// Reading a failure to write the round's events.
//
// The sink reports two different things under one code: a broker that did
// not answer, and its own client refusing to send -- a configuration the
// client checks before any byte leaves, a converter that cannot build the
// message. The first is the dependency's; the second is this deployment's,
// and no amount of waiting for the broker fixes it. Six objects on a live
// deployment failed every round for an afternoon under "the broker is
// unavailable" while the client was refusing to produce headers to a broker
// version it had been told was too old. The words that told the two apart
// were on the failing observation and reached no row; these are the words.

// The kinds an output failure is read as, as the row's dependency evidence
// spells them. Closed: a reader shows these words and no others.
const (
	// OutputFailureBrokerError: the broker did not answer, or answered with
	// its own error. The dependency's.
	OutputFailureBrokerError = "broker_error"
	// OutputFailureClientRejected: this deployment's client refused to send
	// -- its configuration, its converter, its validation. Ours; retrying
	// meets the same refusal.
	OutputFailureClientRejected = "client_rejected"
	// OutputFailureUnknown: words this reader has no signature for. Stays on
	// this deployment's side of the page rather than being handed to the
	// broker.
	OutputFailureUnknown = "unknown"
)

// OutputFailureKinds is every word OutputFailureKind can return.
var OutputFailureKinds = []string{OutputFailureBrokerError, OutputFailureClientRejected, OutputFailureUnknown}

// DependencyEvidences is every word Blocked.DependencyEvidence can carry: the
// two that say how a dependency was named, and the three that read an output
// failure.
var DependencyEvidences = []string{dependencyByCode, dependencyByText, OutputFailureBrokerError, OutputFailureClientRejected, OutputFailureUnknown}

// outputClientSignatures are fragments of the errors this deployment's own
// Kafka client and converters write when they refuse to send. Each is taken
// from the emitter that writes it: the client's configuration error
// ("kafka: invalid configuration (...)", which is what "Producing headers
// requires Kafka at least v0.11" arrives wrapped in), the converters' own
// prefixes, and the sink's own validation words.
var outputClientSignatures = []string{
	"kafka: invalid configuration",
	"alarmd linkdoutput:",
	"legacy conversion",
	"legacy event has no frozen compatibility context",
	"unsupported output format",
	"kafka trigger event sink: validate event",
	"kafka trigger event sink: encode event",
}

// outputBrokerSignatures are fragments of what the client writes when the
// broker, or the way to it, is the problem: the broker's own errors are
// prefixed "kafka server:", the client's out-of-brokers and delivery words,
// and the transport's.
var outputBrokerSignatures = []string{
	"kafka server:",
	"client has run out of available brokers",
	"Failed to produce message",
	"Failed to deliver",
	"dial tcp",
	"i/o timeout",
	"connection refused",
	"connection reset",
	"broken pipe",
	"request timed out",
	"EOF",
}

// OutputFailureKind reads the words of an output failure. The client's
// refusals are checked first: a refused batch is reported as a delivery
// failure wrapping the refusal, so both signatures are present and the
// inner one is the cause.
func OutputFailureKind(text string) string {
	if text == "" {
		return OutputFailureUnknown
	}
	for _, signature := range outputClientSignatures {
		if strings.Contains(text, signature) {
			return OutputFailureClientRejected
		}
	}
	for _, signature := range outputBrokerSignatures {
		if strings.Contains(text, signature) {
			return OutputFailureBrokerError
		}
	}
	return OutputFailureUnknown
}

// outputRejectionCodes are the sink's own words for refusing to write: a
// converter that could not build the message, a client that would not send
// it. A failure under one of them is the client's whatever its sentence
// says -- the sink decided that, and the sentence is for the reader.
var outputRejectionCodes = map[string]bool{contract.ReasonOutputConversionRejected: true, contract.ReasonOutputClientRejected: true}

// outputFailureOf is the row's output failure when its failure is one and
// is this round's evidence to read: the reference and its kind. The sink's
// own reason word decides the kind when it gave one; the words decide when
// it did not.
func outputFailureOf(anomaly Anomaly) (*FailureRef, string, bool) {
	if anomaly.Failure == nil || anomaly.Failure.Stage != "output" {
		return nil, "", false
	}
	if outputRejectionCodes[anomaly.Failure.Code] {
		return anomaly.Failure, OutputFailureClientRejected, true
	}
	return anomaly.Failure, OutputFailureKind(anomaly.Failure.Text), true
}
