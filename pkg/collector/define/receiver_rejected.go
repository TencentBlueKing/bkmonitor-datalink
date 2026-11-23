// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// RejectReason is why the collector refused a request, or dropped part of
// it, before a pipeline handled it.
type RejectReason string

func (r RejectReason) S() string { return string(r) }

// Caused by the client.
const (
	// RejectTooLarge is an HTTP body whose Content-Length is over the limit
	// (answered 413), or a gRPC message over the receive size limit.
	RejectTooLarge RejectReason = "too_large"
	// RejectChunkedOverLimit is a chunked HTTP body that read past the
	// limit. It is only counted: the request is still handled in full.
	RejectChunkedOverLimit RejectReason = "chunked_over_limit"
	// RejectDecompress is a gzip or zlib body, or a compressed gRPC
	// message, that could not be decompressed.
	RejectDecompress RejectReason = "decompress"
	// RejectDecode is a gRPC message that could not be decoded.
	RejectDecode RejectReason = "decode"
	// RejectTokenPartial is a request some of whose resources carried an
	// invalid token: those resources were dropped, the rest handled.
	RejectTokenPartial RejectReason = "token_partial"
)

// Caused by the collector itself.
const (
	// RejectNoPipeline is a record that passed the precheck but found no
	// pipeline for its type when dispatched, and was dropped.
	RejectNoPipeline RejectReason = "no_pipeline"
)

var receiverRejected = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: MonitoringNamespace,
		Name:      "receiver_rejected_total",
		Help: "Requests refused, or partly dropped, before a pipeline handled them. " +
			"protocol is how the request came in (derived for records the collector made itself). " +
			"Caused by the client: too_large (HTTP Content-Length over the limit, answered 413, " +
			"or gRPC message over the receive limit), chunked_over_limit (chunked HTTP body read past the limit; " +
			"only counted, the request is still handled), decompress (body or gRPC message could not be decompressed), " +
			"decode (gRPC message could not be decoded), token_partial (resources with an invalid token were dropped " +
			"and the rest handled). Caused by the collector: no_pipeline (record passed the precheck " +
			"but found no pipeline for its type at dispatch and was dropped). One count per request, or per record for no_pipeline.",
	},
	[]string{"protocol", "reason"},
)

func init() {
	// Every cell that can be written exists from the start, so the first
	// rejection after a start moves an existing series and increase() sees it.
	for _, protocol := range []RequestType{RequestHttp, RequestGrpc} {
		for _, reason := range []RejectReason{
			RejectTooLarge, RejectChunkedOverLimit, RejectDecompress, RejectDecode, RejectTokenPartial, RejectNoPipeline,
		} {
			receiverRejected.WithLabelValues(protocol.S(), reason.S())
		}
	}
	// no_pipeline and token_partial are counted under the request type the
	// record carries, so they have a cell for every request type there is.
	for _, protocol := range []RequestType{RequestTars, RequestICMP, RequestDerived} {
		receiverRejected.WithLabelValues(protocol.S(), RejectNoPipeline.S())
		receiverRejected.WithLabelValues(protocol.S(), RejectTokenPartial.S())
	}
}

// IncReceiverRejected counts one request, or one record for no_pipeline,
// refused or partly dropped for reason.
func IncReceiverRejected(protocol RequestType, reason RejectReason) {
	receiverRejected.WithLabelValues(protocol.S(), reason.S()).Inc()
}
