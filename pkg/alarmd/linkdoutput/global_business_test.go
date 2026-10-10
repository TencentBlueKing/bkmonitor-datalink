// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package linkdoutput

import (
	"bytes"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// A global business Plan's event is filed under the business it was
// attributed to: labels.bk_biz_id is that business. Only the label moves -
// the alert id, the event id, the strategy and every other field are the
// message the same decision writes without an attribution, so the alert is
// the same alert and the consumer enriches it from the business it is about.
func TestAnAttributedEventIsLabelledWithTheBusinessItIsAbout(t *testing.T) {
	ordinary := convert(t, decision(nil))
	attributed := convert(t, decision(func(event *contract.TriggerEventV1) { event.AttributedBusinessID = "11" }))
	if got := string(attributed["labels"]); got != `{"strategy_id":123,"strategy_version":7,"bk_biz_id":11}` {
		t.Fatalf("labels = %s, want the attributed business 11", got)
	}
	for field, value := range ordinary {
		if field == "labels" {
			continue
		}
		if !bytes.Equal(attributed[field], value) {
			t.Fatalf("%s = %s with the attribution, %s without: only the label may move", field, attributed[field], value)
		}
	}
	if len(attributed) != len(ordinary) {
		t.Fatalf("fields %d with the attribution, %d without", len(attributed), len(ordinary))
	}
}

// An attribution that is the Plan's own business - a strategy aggregated
// across businesses - writes the message an ordinary Plan writes.
func TestAnEventAttributedToItsOwnBusinessWritesTheSameMessage(t *testing.T) {
	ordinary := convertRaw(t, decision(nil))
	own := convertRaw(t, decision(func(event *contract.TriggerEventV1) { event.AttributedBusinessID = event.BusinessID }))
	if !bytes.Equal(own.Payload, ordinary.Payload) {
		t.Fatalf("message\n%s\nwant\n%s", own.Payload, ordinary.Payload)
	}
}
