// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package contract

import (
	"bytes"
	"os"
	"testing"
)

func goldenTriggerEvent(t *testing.T) ([]byte, *TriggerEventV1) {
	t.Helper()
	payload, err := os.ReadFile("testdata/go-v2/trigger_event_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	event, err := DecodeTriggerEventV1(payload)
	if err != nil {
		t.Fatal(err)
	}
	return payload, event
}

// The attributed business rides beside the event's identity: it round-trips
// through the strict reader in every schema minor, and it is not part of
// the event id or the semantic digest, so attributing an event does not
// make it another event. An event without it encodes the bytes it always
// did.
func TestAnAttributedBusinessRoundTripsWithoutMovingTheEventIdentity(t *testing.T) {
	_, event := goldenTriggerEvent(t)
	ordinary, err := EncodeTriggerEventV1(event)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ordinary, []byte("attributed_business_id")) {
		t.Fatalf("an event without an attribution names the field: %s", ordinary)
	}
	event.AttributedBusinessID = "11"
	encoded, err := EncodeTriggerEventV1(event)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeTriggerEventV1(encoded)
	if err != nil {
		t.Fatalf("the reader refused an attributed event: %v", err)
	}
	if decoded.AttributedBusinessID != "11" || decoded.EventID != event.EventID || decoded.EventSemanticDigest != event.EventSemanticDigest {
		t.Fatalf("decoded = %+v", decoded)
	}
}

// The attribution is spelled as the business id is, and never zero, never
// null: a reader that took either would file an alert under no business.
func TestAnAttributedBusinessIsACanonicalNonZeroDecimal(t *testing.T) {
	for _, value := range []string{"0", "011", "biz", "1.5"} {
		_, event := goldenTriggerEvent(t)
		event.AttributedBusinessID = value
		if err := ValidateTriggerEventV1(event); err == nil {
			t.Fatalf("attributed_business_id=%q validated", value)
		}
	}
	payload, _ := goldenTriggerEvent(t)
	withNull := bytes.Replace(payload, []byte(`"business_id":`), []byte(`"attributed_business_id":null,"business_id":`), 1)
	if bytes.Equal(withNull, payload) {
		t.Fatal("fixture has no business_id to place the field beside")
	}
	if _, err := DecodeTriggerEventV1(withNull); err == nil {
		t.Fatal("the reader took a null attributed_business_id")
	}
}
