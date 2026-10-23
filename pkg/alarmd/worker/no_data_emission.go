// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/legacyoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// noDataEmissionOf is what a write did with its no-data events, read off the
// write's own answer: everything acknowledged when it succeeded; on a partial
// write the events it names as not written, and only those, known unwritten;
// a whole batch whose acknowledgement is unknown counted apart, since it may
// have landed; any other failed batch known unwritten. Counted here, after
// the write, and nowhere earlier: every full round decides a NORMAL for
// every group that reported, so a count of decisions says nothing about
// what reached the alert store.
func noDataEmissionOf(events []contract.TriggerEventV1, err error) *observability.NoDataEmissionFacts {
	notWritten, partial := outputNotWritten(err)
	unknown := err != nil && !partial && isRetryableOutputDependency(err)
	var facts *observability.NoDataEmissionFacts
	var lastAbnormal, lastRecovery *contract.TriggerEventV1
	for i := range events {
		event := &events[i]
		if _, tagged := event.RecordRef.Dimensions[contract.NoDataDimensionTag]; !tagged {
			continue
		}
		if facts == nil {
			facts = &observability.NoDataEmissionFacts{}
		}
		_, skipped := notWritten[event.EventID]
		switch {
		case unknown:
			facts.AckUnknown++
			continue
		case err != nil && (!partial || skipped):
			facts.NotWritten++
			continue
		}
		latest := &lastAbnormal
		switch event.EventKind {
		case contract.TriggerEventAbnormal:
			facts.AbnormalSent++
		case contract.TriggerEventRecovery:
			facts.RecoverySent++
			latest = &lastRecovery
		default:
			continue
		}
		if *latest != nil && (*latest).EvaluationTime > event.EvaluationTime {
			continue
		}
		*latest = event
	}
	if facts != nil {
		facts.LastAbnormal, facts.LastRecovery = noDataEmitted(lastAbnormal), noDataEmitted(lastRecovery)
	}
	return facts
}

// noDataEmitted is one acknowledged no-data event as a reader looks it up,
// built once per write for the latest of each kind. Its key is the one the
// message was written under. Under the compatibility protocol that is the
// md5 of the group's dimensions and the tag (legacyoutput.NoDataDedupeMD5),
// not the event's own dedupe_md5, which a Plan without a frozen snapshot
// revision leaves empty and which otherwise hashes the Plan's identity
// fields; the converter computed the same key to write the message, so it
// does not fail here for an event that was written. Under the standard
// protocol it is the dedupe_md5, sent as the alert id.
func noDataEmitted(event *contract.TriggerEventV1) *observability.NoDataEmittedEvent {
	if event == nil {
		return nil
	}
	key := event.DedupeMD5
	if event.WireFormat == contract.WireFormatPythonCompatible {
		key, _ = legacyoutput.NoDataDedupeMD5(event)
	}
	group, truncated := observability.NoDataEmittedGroup(event.RecordRef.Dimensions, contract.NoDataDimensionTag)
	return &observability.NoDataEmittedEvent{EvaluationTime: event.EvaluationTime, AlertKey: key,
		Group: group, GroupTruncated: truncated}
}
