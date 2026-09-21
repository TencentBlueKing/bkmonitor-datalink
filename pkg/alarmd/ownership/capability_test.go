// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package ownership

import (
	"encoding/json"
	"testing"
)

// The content contract starts for a fleet, not for nobody, and not for a
// fleet with one member that does not take part; a registration written by
// a binary from before the field declares nothing, which is the answer a
// rollout needs.
func TestAllDeclareIsTrueOnlyForAWholeFleetThatTakesPart(t *testing.T) {
	declaring := WorkerRegistration{WorkerID: "a", Capabilities: []string{CapabilityContentScope}}
	silent := WorkerRegistration{WorkerID: "b"}
	other := WorkerRegistration{WorkerID: "c", Capabilities: []string{"something-else.v1"}}
	if AllDeclare(nil, CapabilityContentScope) {
		t.Fatal("an empty fleet declared the contract")
	}
	if !AllDeclare([]WorkerRegistration{declaring, declaring}, CapabilityContentScope) {
		t.Fatal("a fleet that declares throughout was not read as declaring")
	}
	if AllDeclare([]WorkerRegistration{declaring, silent}, CapabilityContentScope) ||
		AllDeclare([]WorkerRegistration{declaring, other}, CapabilityContentScope) {
		t.Fatal("a fleet with one member that does not declare was read as declaring")
	}
	// The wire shape a binary from before the field wrote decodes to no
	// capabilities, and one with the field keeps it.
	var old WorkerRegistration
	if err := json.Unmarshal([]byte(`{"worker_id":"old","assignment_readiness":"READY"}`), &old); err != nil || old.Declares(CapabilityContentScope) {
		t.Fatalf("old registration = %+v, %v; want no capability declared", old, err)
	}
	encoded, err := json.Marshal(declaring)
	if err != nil {
		t.Fatal(err)
	}
	var decoded WorkerRegistration
	if err := json.Unmarshal(encoded, &decoded); err != nil || !decoded.Declares(CapabilityContentScope) {
		t.Fatalf("round-tripped registration = %+v, %v; want the capability kept", decoded, err)
	}
}
