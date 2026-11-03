// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package trigger_test

import (
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/legacyoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/trigger"
)

// The two computations of a no-data event's fingerprint agree: the trigger
// hashes with the no-data view's identity, the platform-compatible output
// with the record's own keys. Two groups differing only in a no-data field
// outside the item's dimensions are two fingerprints on both sides, and the
// same two.
func TestTheTriggerAndTheOutputFingerprintANoDataEventAlike(t *testing.T) {
	plan := trigger.IdentityPlanForTest(t, "tenant-a")
	identity := plan.NoDataOutputIdentity()
	if identity == nil {
		t.Fatal("fixture: the plan detects no no-data")
	}
	seen := map[string]bool{}
	for _, zone := range []string{"zone-a", "zone-b"} {
		dimensions := map[string]json.RawMessage{"host": json.RawMessage(`"192.0.2.40"`), "zone": json.RawMessage(`"` + zone + `"`),
			contract.NoDataDimensionTag: json.RawMessage(`true`)}
		fromTrigger, err := contract.MonitorDedupeMD5("1001", "2", dimensions, *identity)
		if err != nil {
			t.Fatal(err)
		}
		event := &contract.TriggerEventV1{BusinessID: "2"}
		event.PlanRef.StrategyID = "1001"
		event.RecordRef.Dimensions = dimensions
		fromOutput, err := legacyoutput.NoDataDedupeMD5(event)
		if err != nil {
			t.Fatal(err)
		}
		if fromTrigger != fromOutput {
			t.Fatalf("%s: trigger fingerprint %s, output %s", zone, fromTrigger, fromOutput)
		}
		seen[fromTrigger] = true
	}
	if len(seen) != 2 {
		t.Fatal("the two groups share one fingerprint")
	}
}
