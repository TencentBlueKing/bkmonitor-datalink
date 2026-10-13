// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/admission"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/racedetector"
)

// Python's fuller overwrites bk_target_ip with the CMDB host's address
// whenever it finds the host by bk_host_id or bk_agent_id (bk-monitor
// c0e828fa8c, fullers.py:57-74), and the alert's target is read from it
// afterwards. This side writes nothing back, so a record placed that way
// whose own bk_target_ip is not the host's - another address, empty, or
// missing - is one whose alert identity can differ from Python's. The fact
// is marked for exactly those records; a record placed by its address, or
// not placed at all, is not one Python rewrites.
func TestARecordPlacedByIDWhoseAddressIsNotTheHostsIsMarked(t *testing.T) {
	store := agentStore(map[string]string{"agent-live": "720002"})
	chain := instanceChain(t, store)
	// placed is the branch Python's fuller rewrites bk_target_ip on - the
	// host found by bk_host_id or bk_agent_id - whatever the address: the
	// count the marked ones are read against, so a zero of the marked can
	// tell "always the host's address" from "never placed that way".
	//
	// The cause says which way it differs - another address, none at all, or
	// a value that is not a string - and only another address carries the
	// value reported, beside CMDB's; the host the record was placed at is
	// named on every marked one.
	for _, c := range []struct {
		name     string
		record   string
		cause    string
		reported string
		placed   bool
	}{
		{"by id, the host's address", `{"bk_host_id":"720002","bk_target_ip":"192.0.2.172"}`, admission.AddressSame, "", true},
		{"by id, another address", `{"bk_host_id":"720002","bk_target_ip":"198.51.100.9"}`, admission.AddressDiffers, "198.51.100.9", true},
		{"by id, an empty address", `{"bk_host_id":"720002","bk_target_ip":""}`, admission.AddressMissing, "", true},
		{"by id, no address", `{"bk_host_id":"720002"}`, admission.AddressMissing, "", true},
		// Not a string at all: Python writes CMDB's string over it.
		{"by id, an address that is not a string", `{"bk_host_id":"720002","bk_target_ip":192}`, admission.AddressUndecodable, "", true},
		{"by agent, another address", `{"bk_agent_id":"agent-live","bk_target_ip":"198.51.100.9"}`, admission.AddressDiffers, "198.51.100.9", true},
		{"by agent, the host's address", `{"bk_agent_id":"agent-live","bk_target_ip":"192.0.2.172"}`, admission.AddressSame, "", true},
		// Escaped in the JSON, the host's address once decoded: the same
		// string Python's dedupe sees, so not marked.
		{"by id, the host's address escaped", `{"bk_host_id":"720002","bk_target_ip":"192.0.2.\u0031\u0037\u0032"}`, admission.AddressSame, "", true},
		{"by id, another address escaped", `{"bk_host_id":"720002","bk_target_ip":"198.51.100.\u0039"}`, admission.AddressDiffers, "198.51.100.9", true},
		{"by address", `{"bk_target_ip":"192.0.2.172","bk_target_cloud_id":0}`, "", "", false},
		{"an id CMDB does not know", `{"bk_host_id":"720999","bk_target_ip":"198.51.100.9"}`, "", "", false},
	} {
		facts := chain.Enrich(jsonDims(t, c.record))
		address := facts.ReportedAddress
		if address.Cause != c.cause || address.Reported != c.reported {
			t.Errorf("%s: cause %q reported %q, want %q %q", c.name, address.Cause, address.Reported, c.cause, c.reported)
		}
		marked := c.cause != "" && c.cause != admission.AddressSame
		if address.Differs() != marked {
			t.Errorf("%s: differs %t, want %t", c.name, address.Differs(), marked)
		}
		if marked && (address.CMDB != "192.0.2.172" || address.HostID != "720002") {
			t.Errorf("%s: CMDB address %q host %q, want 192.0.2.172 on host 720002", c.name, address.CMDB, address.HostID)
		}
		if !marked && (address.CMDB != "" || address.HostID != "") {
			t.Errorf("%s: an address that does not differ names %q on %q", c.name, address.CMDB, address.HostID)
		}
		if facts.PlacedByHostID != c.placed {
			t.Errorf("%s: placed by id or agent %t, want %t", c.name, facts.PlacedByHostID, c.placed)
		}
	}
}

// The comparison runs for every series placed by id or agent, on every
// query: an address written without escapes that is the host's is compared
// as it is, with nothing allocated. Only one that differs copies what it
// reported, which is the rare path.
func TestComparingAnUnescapedAddressAllocatesNothing(t *testing.T) {
	host := &HostFacts{IP: "192.0.2.172", HostID: "720002"}
	dimensions := jsonDims(t, `{"bk_host_id":"720002","bk_target_ip":"192.0.2.172"}`)
	var address admission.ReportedAddress
	if allocations := testing.AllocsPerRun(100, func() { address = reportedAddress(dimensions, host) }); !racedetector.Enabled && allocations != 0 {
		t.Fatalf("%v allocations a comparison, want none", allocations)
	}
	if address.Cause != admission.AddressSame {
		t.Fatalf("cause %q, want same", address.Cause)
	}
}
