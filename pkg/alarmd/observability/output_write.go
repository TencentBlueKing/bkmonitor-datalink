// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import "context"

// What the sink did with the round's events, as the sink counts it.
//
// The event_acked line said "success, 14 events, 1 ms" for a round whose
// fourteen events were all recoveries, which the Python-compatible protocol
// has no message for: nothing was handed to the broker and the line read as
// a write. On a deployment whose brokers were refusing every write, those
// lines were the evidence that some writes still went through. They were
// not. The line now says how many messages the sink handed to its client and
// how many events produced none -- from the sink, which is the only party
// that knows, through the context the caller gave it.

// OutputWriteFacts is the sink's count for one batch. Published is the
// number of messages handed to the client; on a successful write it is the
// number acknowledged, on a failed one the number attempted. WithoutMessage
// is the events the protocol had no message for -- a zero-message batch is
// a success that reached no broker, and reads as one.
type OutputWriteFacts struct {
	Published      int64 `json:"messages_published"`
	WithoutMessage int64 `json:"events_without_message"`
}

type outputWriteReport struct {
	facts    OutputWriteFacts
	reported bool
}

type outputWriteReportKey struct{}

// ContextWithOutputWriteReport gives the caller of a sink a place for the
// sink's count. The returned function is the facts once the sink reported
// them, or nil for a sink that did not -- an older sink, a fake -- which is
// left absent rather than read as zero messages.
func ContextWithOutputWriteReport(ctx context.Context) (context.Context, func() *OutputWriteFacts) {
	if ctx == nil {
		ctx = context.Background()
	}
	report := &outputWriteReport{}
	return context.WithValue(ctx, outputWriteReportKey{}, report), func() *OutputWriteFacts {
		if !report.reported {
			return nil
		}
		facts := report.facts
		return &facts
	}
}

// ReportOutputWrite is the sink's count of one batch: messages handed to its
// client, events that produced none. A no-op when the caller gave no place
// for it.
func ReportOutputWrite(ctx context.Context, published, withoutMessage int) {
	if ctx == nil {
		return
	}
	report, _ := ctx.Value(outputWriteReportKey{}).(*outputWriteReport)
	if report == nil {
		return
	}
	report.facts = OutputWriteFacts{Published: int64(published), WithoutMessage: int64(withoutMessage)}
	report.reported = true
}
