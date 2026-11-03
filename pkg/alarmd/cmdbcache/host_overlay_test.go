// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

import (
	"encoding/json"
	"testing"
)

// A record whose host CMDB placed by bk_host_id or bk_agent_id carries the
// dimensions Python's fuller writes (fullers.py:57-74) - the host's address,
// its cloud ("0" for none) and, when the record has no bk_host_id key, its id
// as a string - exactly when its own address or cloud is not the host's; a
// record that already names its host's address carries nothing, so the
// series an ordinary deployment sends cost nothing.
func TestARecordPlacedByIDCarriesTheHostsAddressOnlyWhereItDiffers(t *testing.T) {
	store := agentStore(map[string]string{"agent-live": "720002"})
	chain := instanceChain(t, store)
	byID := map[string]string{"bk_target_ip": `"192.0.2.172"`, "bk_target_cloud_id": `"0"`}
	byAgent := map[string]string{"bk_target_ip": `"192.0.2.172"`, "bk_target_cloud_id": `"0"`, "bk_host_id": `"720002"`}
	for _, c := range []struct {
		name    string
		record  string
		written map[string]string
	}{
		{"by id, the host's address", `{"bk_host_id":"720002","bk_target_ip":"192.0.2.172","bk_target_cloud_id":"0"}`, nil},
		{"by id, the host's address and cloud as a number", `{"bk_host_id":"720002","bk_target_ip":"192.0.2.172","bk_target_cloud_id":0}`, nil},
		{"by id, the host's address and no cloud", `{"bk_host_id":"720002","bk_target_ip":"192.0.2.172"}`, nil},
		{"by id, the host's address escaped", `{"bk_host_id":"720002","bk_target_ip":"192.0.2.172"}`, nil},
		// The record names its id: Python leaves bk_host_id as it is.
		{"by id, another address", `{"bk_host_id":"720002","bk_target_ip":"198.51.100.9","bk_target_cloud_id":"0"}`, byID},
		{"by id, no address", `{"bk_host_id":"720002"}`, byID},
		{"by id, the host's address in another cloud", `{"bk_host_id":"720002","bk_target_ip":"192.0.2.172","bk_target_cloud_id":"3"}`, byID},
		{"by agent, another address", `{"bk_agent_id":"agent-live","bk_target_ip":"198.51.100.9"}`, byAgent},
		{"by agent, no address", `{"bk_agent_id":"agent-live"}`, byAgent},
		// An empty bk_host_id is no id, so the agent places the record; the
		// key is there, so Python does not write it.
		{"by agent, an empty id key", `{"bk_agent_id":"agent-live","bk_host_id":"","bk_target_ip":"198.51.100.9"}`, byID},
		{"by agent, the host's address", `{"bk_agent_id":"agent-live","bk_target_ip":"192.0.2.172"}`, nil},
		// Placed by its address: Python's address branch does not write
		// bk_target_ip, so neither is it overlaid here.
		{"by address", `{"bk_target_ip":"192.0.2.172","bk_target_cloud_id":0}`, nil},
		{"an id CMDB does not know", `{"bk_host_id":"720999","bk_target_ip":"198.51.100.9"}`, nil},
	} {
		checkOverlay(t, c.name, chain.Enrich(jsonDims(t, c.record)).HostOverlay, c.written)
	}
}

// A host whose facts hold no cloud is written with cloud "0", the cloud the
// platform looked it up in. The index reads a record without bk_cloud_id as
// cloud 0 already; the facts are emptied here so the overlay's own default
// is what is tested.
func TestAHostWithoutACloudIsOverlaidWithCloudZero(t *testing.T) {
	cloudless := `{"bk_host_id":720005,"bk_host_innerip":"192.0.2.175","bk_biz_id":999,"bk_agent_id":"agent-cloudless",` +
		`"bk_state":"运营中[需告警]","display_name":"c","topo_link":{"module|91":[{"bk_obj_id":"module","bk_inst_id":91}]}}`
	store := storeWith([]string{"720005", cloudless, "192.0.2.175|0", cloudless}, nil)
	store.index.byAgent = map[string]string{"agent-cloudless": "720005"}
	host, found := store.index.Lookup("720005")
	if !found {
		t.Fatal("the fixture host is not indexed")
	}
	host.CloudID = ""
	if again, _ := store.index.Lookup("720005"); again.CloudID != "" {
		t.Fatalf("the fixture host %+v still holds a cloud", again)
	}
	chain := instanceChain(t, store)
	checkOverlay(t, "by agent, another address", chain.Enrich(jsonDims(t, `{"bk_agent_id":"agent-cloudless","bk_target_ip":"198.51.100.9"}`)).HostOverlay,
		map[string]string{"bk_target_ip": `"192.0.2.175"`, "bk_target_cloud_id": `"0"`, "bk_host_id": `"720005"`})
	checkOverlay(t, "by agent, the host's address", chain.Enrich(jsonDims(t, `{"bk_agent_id":"agent-cloudless","bk_target_ip":"192.0.2.175"}`)).HostOverlay, nil)
}

func checkOverlay(t *testing.T, name string, overlay map[string]json.RawMessage, written map[string]string) {
	t.Helper()
	if (overlay != nil) != (written != nil) {
		t.Errorf("%s: overlay %v, want %v", name, overlay, written)
		return
	}
	if len(overlay) != len(written) {
		t.Errorf("%s: overlay %v, want %v", name, overlay, written)
	}
	for key, want := range written {
		if got := string(overlay[key]); got != want {
			t.Errorf("%s: %s = %s, want %s", name, key, got, want)
		}
	}
	var decoded map[string]any
	encoded, _ := json.Marshal(overlay)
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Errorf("%s: overlay does not encode: %v", name, err)
	}
}
