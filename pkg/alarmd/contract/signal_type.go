// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package contract

// The kinds of thing an event can be observed from, as the alert consumer's
// contract names them.
//
// They are the data type half of a query's source semantics, not the source
// half: which backend the points came from is the platform's business, and
// what the operator is watching is what a consumer routes and displays on.
const (
	SignalTypeMetric = "metric"
	SignalTypeLog    = "log"
	SignalTypeEvent  = "event"
)

// SignalTypeForDataType maps one query config's data_type_label to the signal
// type an event from it carries. It answers "" for a label this build has no
// mapping for, which the caller reports as no signal type rather than as a
// guess.
func SignalTypeForDataType(label string) string {
	switch label {
	case "time_series":
		return SignalTypeMetric
	case "log":
		return SignalTypeLog
	case "event":
		return SignalTypeEvent
	default:
		return ""
	}
}

// SignalTypeForDataTypes is the signal type of an item whose query configs may
// be several.
//
// Every config has to agree. An item that read a metric for one input and a log
// for another would have no single answer, and the two plausible tie-breaks --
// take the first, or give one kind precedence -- are both a choice about what
// the operator meant that nothing in the strategy states. So a disagreement
// answers "", the Plan carries no signal type, and the event omits the field
// rather than telling a consumer something the strategy never said.
//
// It is not reachable from the product today: the strategy editor builds an
// item's inputs from one data source kind. That is why it answers rather than
// refuses -- a Plan this build cannot label is still a Plan that detects, and
// failing to compile it would take a working strategy down over a field the
// consumer can do without.
func SignalTypeForDataTypes(labels []string) string {
	signal := ""
	for _, label := range labels {
		mapped := SignalTypeForDataType(label)
		if mapped == "" {
			return ""
		}
		if signal == "" {
			signal = mapped
			continue
		}
		if signal != mapped {
			return ""
		}
	}
	return signal
}
