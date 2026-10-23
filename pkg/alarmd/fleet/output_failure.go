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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// Reading a failure to write the round's events.
//
// The sink reports several different things under one code: a broker that
// did not answer, a broker that answered no, a sink that never asked one,
// the snapshot store the compatible protocol writes - and, under codes of
// their own, its own client refusing to send. Six objects on a live
// deployment failed every round for an afternoon under "the broker is
// unavailable" while the client was refusing to produce headers to a broker
// version it had been told was too old. The words that told the two apart
// were on the failing observation and reached no row; they were then read
// back out of the error's text, which a Redis's EOF could pass as Kafka's.
// Now the sink's error says its kind by its type (observability's
// OutputFailureKinds), the row carries it, and this reads it.

// DependencyEvidences is every word Blocked.DependencyEvidence can carry: the
// two that say how a dependency was named, and the kinds an output failure
// is read as.
var DependencyEvidences = append([]string{dependencyByCode, dependencyByText}, observability.OutputFailureKinds...)

func validOutputFailureKind(kind string) bool {
	for _, known := range observability.OutputFailureKinds {
		if kind == known {
			return true
		}
	}
	return false
}

// outputFailureReading is where each kind of output failure is filed: the
// dependency it is the failure of, and the class. A kind not in the table -
// unknown, a failure from outside the sink - stays unlocated, on this
// deployment's side of the page rather than handed to the broker.
var outputFailureReading = map[string]struct {
	dependency Dependency
	class      Class
}{
	observability.OutputFailureAckUnknown:     {DependencyKafka, ClassUnavailable},
	observability.OutputFailureBrokerRefused:  {DependencyKafka, ClassUnavailable},
	observability.OutputFailureSinkNotOpen:    {DependencyKafka, ClassUnavailable},
	observability.OutputFailureSnapshotStore:  {DependencyRedis, ClassUnavailable},
	observability.OutputFailureClientRejected: {DependencyNone, ClassContract},
}

// outputNamedCodes are the codes the sink names a failure by when the code
// alone says what it was: the converter that could not build the message,
// the client that would not send it, the lease too short to start a batch.
// Each has its reading in the code table; the kind only adds which one.
var outputNamedCodes = map[string]bool{contract.ReasonOutputConversionRejected: true, contract.ReasonOutputClientRejected: true,
	contract.ReasonOutputLeaseExpiring: true}

// outputRejectionCodes are the sink's two refusal words: a failure under one
// of them is a client refusal by its code alone, which is how a row from a
// publisher that carried no kind is still read.
var outputRejectionCodes = map[string]bool{contract.ReasonOutputConversionRejected: true, contract.ReasonOutputClientRejected: true}

// outputFailureOf is the row's output failure when its failure is one and
// is this round's evidence to read: the reference and its kind - from the
// row, else client_rejected under a refusal code, else unknown.
func outputFailureOf(anomaly Anomaly) (*FailureRef, string, bool) {
	if anomaly.Failure == nil || anomaly.Failure.Stage != "output" {
		return nil, "", false
	}
	kind := anomaly.Failure.Kind
	switch {
	case validOutputFailureKind(kind):
	case outputRejectionCodes[anomaly.Failure.Code]:
		kind = observability.OutputFailureClientRejected
	default:
		kind = observability.OutputFailureUnknown
	}
	return anomaly.Failure, kind, true
}
