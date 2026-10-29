// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package fleet

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// A replica's query permits carry the last minute's peak and where its
// memory size came from, beside the instant and the limit: the page writes
// "峰值 X / 上限 N（按内存）" from them, and "按默认内存" when the size is the
// fallback rather than the container's.
func TestTheOverviewPermitsCarryThePeakAndTheMemorySource(t *testing.T) {
	health := overviewHealth()
	capacity := *health.PerReplica[0].Capacity
	capacity.PermitsPeak, capacity.MemorySource = 7, "fallback_default"
	health.PerReplica[0].Capacity = &capacity
	permits := overviewOf(health, nil, false, time.Now()).Resources.Replicas[0].QueryPermits
	encoded, err := json.Marshal(permits)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"held":1,"limit":16,"peak":7,"memory_source":"fallback_default"}` {
		t.Fatalf("query_permits = %s, want held, limit, the peak and the memory source", encoded)
	}
}

// A replica that reported no capacity says nothing about a peak, rather
// than a peak of zero.
func TestAReplicaWithoutCapacityHasNoPeak(t *testing.T) {
	health := overviewHealth()
	health.PerReplica[0].Capacity = nil
	encoded, err := json.Marshal(overviewOf(health, nil, false, time.Now()).Resources.Replicas[0].QueryPermits)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "peak") || strings.Contains(string(encoded), "memory_source") {
		t.Fatalf("query_permits = %s, want no peak and no memory source without capacity", encoded)
	}
}
