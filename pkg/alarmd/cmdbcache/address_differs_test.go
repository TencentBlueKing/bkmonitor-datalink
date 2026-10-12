// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

import "testing"

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
	for _, c := range []struct {
		name   string
		record string
		marked bool
		placed bool
	}{
		{"by id, the host's address", `{"bk_host_id":"720002","bk_target_ip":"192.0.2.172"}`, false, true},
		{"by id, another address", `{"bk_host_id":"720002","bk_target_ip":"198.51.100.9"}`, true, true},
		{"by id, an empty address", `{"bk_host_id":"720002","bk_target_ip":""}`, true, true},
		{"by id, no address", `{"bk_host_id":"720002"}`, true, true},
		// Not a string at all: Python writes CMDB's string over it.
		{"by id, an address that is not a string", `{"bk_host_id":"720002","bk_target_ip":192}`, true, true},
		{"by agent, another address", `{"bk_agent_id":"agent-live","bk_target_ip":"198.51.100.9"}`, true, true},
		{"by agent, the host's address", `{"bk_agent_id":"agent-live","bk_target_ip":"192.0.2.172"}`, false, true},
		// Escaped in the JSON, the host's address once decoded: the same
		// string Python's dedupe sees, so not marked.
		{"by id, the host's address escaped", `{"bk_host_id":"720002","bk_target_ip":"192.0.2.\u0031\u0037\u0032"}`, false, true},
		{"by address", `{"bk_target_ip":"192.0.2.172","bk_target_cloud_id":0}`, false, false},
		{"an id CMDB does not know", `{"bk_host_id":"720999","bk_target_ip":"198.51.100.9"}`, false, false},
	} {
		facts := chain.Enrich(jsonDims(t, c.record))
		if facts.ReportedAddressDiffers != c.marked {
			t.Errorf("%s: marked %t, want %t", c.name, facts.ReportedAddressDiffers, c.marked)
		}
		if facts.PlacedByHostID != c.placed {
			t.Errorf("%s: placed by id or agent %t, want %t", c.name, facts.PlacedByHostID, c.placed)
		}
	}
}

// The comparison runs for every series placed by id or agent, on every
// query: an address written without escapes is compared as it is, with
// nothing allocated.
func TestComparingAnUnescapedAddressAllocatesNothing(t *testing.T) {
	host := &HostFacts{IP: "192.0.2.172"}
	dimensions := jsonDims(t, `{"bk_host_id":"720002","bk_target_ip":"198.51.100.9"}`)
	if allocations := testing.AllocsPerRun(100, func() { reportedAddressDiffers(dimensions, host) }); allocations != 0 {
		t.Fatalf("%v allocations a comparison, want none", allocations)
	}
}
