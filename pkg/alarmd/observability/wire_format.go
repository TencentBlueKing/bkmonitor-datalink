// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"

// The wire format a Plan's events are published as, decided by the frozen
// Plan and its revision (contract.ResolveOutputWireFormat) and carried here
// so it can be read: on the evaluation line of the Plan that decided it, as
// counts on the event_acked line of the batch that carried it, and on the
// leader's account of the Catalog. Until this the word lived in the Plan and
// on the event and reached no log, no metric and no page -- a reader looking
// for how many strategies publish the standard raw event found zero lines
// and nearly filed "none do".
//
// WireFormatOther is the fold every metric cell uses for a word this build
// does not name, so the label set stays bounded while the word itself stays
// on the log line.
const WireFormatOther = "_other"

// WireFormats is every format a metric cell is created for at startup.
var WireFormats = []string{contract.WireFormatPythonCompatible, contract.WireFormatStandardRawEvent, WireFormatOther}

// NormalizeWireFormat folds a wire format onto the bounded metric label set.
func NormalizeWireFormat(format string) string {
	switch format {
	case contract.WireFormatPythonCompatible, contract.WireFormatStandardRawEvent:
		return format
	default:
		return WireFormatOther
	}
}

// OutputWireFormatCounts is how many events of a batch were published as
// each wire format, on an event_acked observation. A batch is one Query
// Group's Slot and its Plans may publish differently, so the line carries
// counts rather than one word.
type OutputWireFormatCounts map[string]int64

// OutputEventKindCounts is how many events of a batch there were of each
// kind under each wire format, on an event_acked observation: the same
// events as OutputWireFormatCounts, split once more. It is what says
// whether a RECOVERY left on the standard line after the window that held
// it filled -- the format alone counts the recovery and the anomaly as one.
type OutputEventKindCounts map[OutputEventKindKey]int64

// OutputEventKindKey is one bucket of OutputEventKindCounts.
type OutputEventKindKey struct {
	Format    string
	EventKind string
}
