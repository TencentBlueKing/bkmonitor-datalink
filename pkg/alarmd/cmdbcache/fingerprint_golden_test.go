// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

import (
	"bytes"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// fingerprintCase is one case of testdata/fingerprint/fingerprint_golden.json:
// a record's dimensions as Python's fuller leaves them, the strategy's
// aggregation, and the dedupe md5 and bk_target_cloud_id Python gives it.
type fingerprintCase struct {
	Name            string                     `json:"name"`
	StrategyID      int64                      `json:"strategy_id"`
	BusinessID      int64                      `json:"bk_biz_id"`
	AggDimension    []string                   `json:"agg_dimension"`
	Dimensions      map[string]json.RawMessage `json:"dimensions"`
	DedupeMD5       string                     `json:"dedupe_md5"`
	TargetCloudJSON string                     `json:"bk_target_cloud_id"`
}

// The fingerprint alarmd gives a record placed through its service instance -
// its dimensions with the instance's address and cloud written over them,
// the cloud a JSON number - is the one Python's own code gives the record
// its fuller rewrote, read from the deployed revision by the generator beside
// the golden; and the cloud the record carries is the same JSON value. A
// string cloud hashes as the number does, and a record placed by its agent,
// whose cloud the host branch writes as a string, matches too.
func TestTheOverlaidFingerprintIsPythons(t *testing.T) {
	raw, err := os.ReadFile("testdata/fingerprint/fingerprint_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []fingerprintCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("the golden is empty")
	}
	record := map[string]json.RawMessage{"bk_target_service_instance_id": json.RawMessage(`"7"`),
		"bk_agent_id": json.RawMessage(`"agent-live"`), "device": json.RawMessage(`"sda"`),
		"bk_target_ip": json.RawMessage(`"198.51.100.9"`), "bk_target_cloud_id": json.RawMessage(`"0"`)}
	instance := &ServiceInstanceFacts{ID: "7", HostID: "720002", IP: "192.0.2.172", CloudID: "0"}
	host := &HostFacts{HostID: "720002", IP: "192.0.2.172", CloudID: "0"}
	for _, c := range cases {
		identity := contract.MonitorOutputIdentity{DimensionFields: c.AggDimension}
		strategy, business := strconv.FormatInt(c.StrategyID, 10), strconv.FormatInt(c.BusinessID, 10)
		python, err := contract.MonitorDedupeMD5(strategy, business, c.Dimensions, identity)
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		if python != c.DedupeMD5 {
			t.Errorf("%s: alarmd fingerprints Python's dimensions as %s, Python as %s", c.Name, python, c.DedupeMD5)
		}
		if strings.Contains(c.Name, "as a string") {
			continue
		}
		var overlay map[string]json.RawMessage
		if strings.HasPrefix(c.Name, "instance placed") {
			overlay = instanceOverlay(record, instance)
		} else {
			overlay = hostOverlay(record, host, reportedAddress(record, host))
		}
		alerted := make(map[string]json.RawMessage, len(record)+len(overlay))
		for key, value := range record {
			alerted[key] = value
		}
		for key, value := range overlay {
			alerted[key] = value
		}
		got, err := contract.MonitorDedupeMD5(strategy, business, alerted, identity)
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		if got != c.DedupeMD5 {
			t.Errorf("%s: alarmd's overlaid record fingerprints as %s, Python's as %s", c.Name, got, c.DedupeMD5)
		}
		if cloud := string(alerted["bk_target_cloud_id"]); cloud != c.TargetCloudJSON {
			t.Errorf("%s: alarmd writes bk_target_cloud_id %s, Python %s", c.Name, cloud, c.TargetCloudJSON)
		}
		if len(alerted) != len(c.Dimensions) {
			t.Errorf("%s: alarmd's record has %d dimensions, Python's %d", c.Name, len(alerted), len(c.Dimensions))
		}
		for key, want := range c.Dimensions {
			if !jsonEqual(alerted[key], want) {
				t.Errorf("%s: %s = %s, Python %s", c.Name, key, alerted[key], want)
			}
		}
	}
}

func jsonEqual(left, right json.RawMessage) bool {
	var a, b any
	if json.Unmarshal(left, &a) != nil || json.Unmarshal(right, &b) != nil {
		return false
	}
	encodedA, _ := json.Marshal(a)
	encodedB, _ := json.Marshal(b)
	return bytes.Equal(encodedA, encodedB)
}
