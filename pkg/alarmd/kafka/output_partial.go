// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kafka

import (
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// refusal is one event the sink will not write: the rule it broke and the
// sentence that says why. The zero value is an event with no refusal.
type refusal struct {
	rule   string
	detail string
}

// RefusedEvent is one event of a batch the sink did not write because of
// its own content, by the rule it broke.
type RefusedEvent struct {
	EventID    string
	Rule       string
	Detail     string
	StrategyID string
	BusinessID string
	Format     string
	index      int
}

// OutputPartiallyRejectedError is a batch the sink wrote except for some of
// its events: Rejected broke a rule, and Withheld are the events that shared
// a series with one of them and so were not written either. Everything else
// of the batch was written and acknowledged; the caller moves the State of
// every other series and leaves these series where they were, so the next
// round decides them again from the State they had. It is not a dependency
// failure, and not a rejection of the batch.
type OutputPartiallyRejectedError struct {
	Rejected []RefusedEvent
	Withheld []string
	whole    bool
}

func (err *OutputPartiallyRejectedError) Error() string {
	if err == nil || len(err.Rejected) == 0 {
		return "kafka trigger event sink: some events rejected"
	}
	first := err.Rejected[0]
	return fmt.Sprintf("kafka trigger event sink: %s: %d events rejected and %d withheld beside them; first %s: %s (event %s, strategy %s, format %s)",
		contract.ReasonOutputConversionRejected, len(err.Rejected), len(err.Withheld), first.Rule, first.Detail, first.EventID, first.StrategyID, first.Format)
}

// OutputRejectionReason names the Slot's completion, as a whole rejection
// does.
func (err *OutputPartiallyRejectedError) OutputRejectionReason() string {
	if err == nil {
		return ""
	}
	return contract.ReasonOutputConversionRejected
}

// OutputRejectionDetail is the first refusal's sentence.
func (err *OutputPartiallyRejectedError) OutputRejectionDetail() string {
	if err == nil || len(err.Rejected) == 0 {
		return ""
	}
	return err.Rejected[0].Rule + ": " + err.Rejected[0].Detail
}

// OutputNotWrittenEventIDs is every event of the batch that was not written,
// rejected or withheld: the caller leaves the State of their series as it
// was and acknowledges only the others.
func (err *OutputPartiallyRejectedError) OutputNotWrittenEventIDs() []string {
	if err == nil {
		return nil
	}
	ids := make([]string, 0, len(err.Rejected)+len(err.Withheld))
	for _, rejected := range err.Rejected {
		ids = append(ids, rejected.EventID)
	}
	return append(ids, err.Withheld...)
}

func (err *OutputPartiallyRejectedError) facts() []observability.OutputRejectedEvent {
	facts := make([]observability.OutputRejectedEvent, len(err.Rejected))
	for i, rejected := range err.Rejected {
		facts[i] = observability.OutputRejectedEvent{Rule: rejected.Rule, StrategyID: rejected.StrategyID, Format: rejected.Format}
	}
	return facts
}

// err is the batch's answer once its messages are written: nil when nothing
// was refused.
func (err *OutputPartiallyRejectedError) err() error {
	if err == nil {
		return nil
	}
	return err
}

// seriesKeyOf is the series an event was decided for, as the State knows
// it: the Plan's identity and the series' dimension identity, which the
// evaluation derives the State key from. Two events with one key move one
// State.
func seriesKeyOf(event *contract.TriggerEventV1) string {
	return event.PlanRef.StrategyID + "\x00" + event.PlanRef.StrategyRevision + "\x00" +
		event.PlanRef.StateCompatibilityHash + "\x00" + event.RecordRef.DimensionIdentityDigest
}

// withholdSeriesOf is, per event, whether it shares a series with a refused
// event without being refused itself.
func withholdSeriesOf(events []contract.TriggerEventV1, refused []refusal) []bool {
	withheld := make([]bool, len(events))
	series := make(map[string]struct{})
	for index := range events {
		if refused[index].rule != "" {
			series[seriesKeyOf(&events[index])] = struct{}{}
		}
	}
	if len(series) == 0 {
		return withheld
	}
	for index := range events {
		if refused[index].rule != "" {
			continue
		}
		if _, shared := series[seriesKeyOf(&events[index])]; shared {
			withheld[index] = true
		}
	}
	return withheld
}

// partialRejection is the batch's refusals, or nil when there are none. It
// is whole when no event of the batch is left to go out or to count as the
// protocol's no-message: then the batch is refused as it always was.
func partialRejection(events []contract.TriggerEventV1, formats []string, refused []refusal, withheld []bool) *OutputPartiallyRejectedError {
	var partial *OutputPartiallyRejectedError
	left := 0
	for index := range events {
		if refused[index].rule == "" {
			if !withheld[index] {
				left++
			}
			continue
		}
		if partial == nil {
			partial = &OutputPartiallyRejectedError{}
		}
		partial.Rejected = append(partial.Rejected, RefusedEvent{
			EventID: events[index].EventID, Rule: refused[index].rule, Detail: refused[index].detail,
			StrategyID: events[index].PlanRef.StrategyID, BusinessID: events[index].BusinessID, Format: formats[index], index: index,
		})
	}
	if partial == nil {
		return nil
	}
	for index := range events {
		if withheld[index] {
			partial.Withheld = append(partial.Withheld, events[index].EventID)
		}
	}
	partial.whole = left == 0
	return partial
}
