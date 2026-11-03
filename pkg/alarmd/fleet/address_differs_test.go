// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"encoding/json"
	"fmt"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/racedetector"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The sample keeps the latest records whose address differs from their
// host's, one per strategy and host: a host seen again moves to the front
// with what it reported this time, and past the bound the oldest goes.
func TestAddressDiffersSamplesKeepTheLatestPerStrategyAndHost(t *testing.T) {
	at := time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)
	samples := &AddressDiffersSamples{}
	record := func(minute int, strategy, host, reported string) {
		samples.Record(AddressDiffersSample{At: at.Add(time.Duration(minute) * time.Minute), Cause: "differs",
			StrategyID: strategy, Business: "2", QueryGroup: "qg-" + strategy, HostID: host, Reported: reported, CMDBAddress: "192.0.2.1"})
	}
	record(0, "101", "11", "198.51.100.1")
	record(1, "102", "11", "198.51.100.2")
	record(2, "101", "11", "198.51.100.3")
	got := samples.Latest()
	if len(got) != 2 || got[0].StrategyID != "101" || got[0].Reported != "198.51.100.3" || got[1].StrategyID != "102" {
		t.Fatalf("samples %+v, want 101's latest first, then 102", got)
	}
	for minute := 3; minute < 3+MaxAddressDiffersSamples+4; minute++ {
		record(minute, strconv.Itoa(200+minute), "12", "198.51.100.9")
	}
	got = samples.Latest()
	if len(got) != MaxAddressDiffersSamples {
		t.Fatalf("%d samples, want the bound %d", len(got), MaxAddressDiffersSamples)
	}
	for index := 1; index < len(got); index++ {
		if got[index].At.After(got[index-1].At) {
			t.Fatalf("samples not newest first: %+v", got)
		}
	}
	for _, sample := range got {
		if sample.StrategyID == "101" || sample.StrategyID == "102" {
			t.Fatalf("an old sample outlived the bound: %+v", got)
		}
	}
	var none *AddressDiffersSamples
	none.Record(AddressDiffersSample{StrategyID: "1"})
	if none.Latest() != nil {
		t.Fatal("a nil sample answered")
	}
}

// Once the bound is reached a record allocates nothing: a strategy whose
// every record differs takes the lock and no garbage.
func TestARecordIntoAFullSampleAllocatesNothing(t *testing.T) {
	samples := &AddressDiffersSamples{}
	for index := 0; index < MaxAddressDiffersSamples; index++ {
		samples.Record(AddressDiffersSample{StrategyID: strconv.Itoa(index), HostID: "1"})
	}
	next := AddressDiffersSample{StrategyID: "new", HostID: "1"}
	again := AddressDiffersSample{StrategyID: "3", HostID: "1"}
	if allocations := testing.AllocsPerRun(100, func() { samples.Record(next); samples.Record(again) }); !racedetector.Enabled && allocations != 0 {
		t.Fatalf("%v allocations a record, want none", allocations)
	}
}

// Records are sampled from every query running at once.
func TestAddressDiffersSamplesTakeConcurrentRecords(t *testing.T) {
	samples := &AddressDiffersSamples{}
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for index := 0; index < 200; index++ {
				samples.Record(AddressDiffersSample{At: time.Now(), StrategyID: strconv.Itoa(worker), HostID: strconv.Itoa(index % 5)})
				_ = samples.Latest()
			}
		}(worker)
	}
	wg.Wait()
	if got := samples.Latest(); len(got) != MaxAddressDiffersSamples {
		t.Fatalf("%d samples, want %d", len(got), MaxAddressDiffersSamples)
	}
}

// fleet.get carries the replicas' samples together, newest first, each
// naming its replica, within the same bound; a replica with none puts
// nothing on the wire.
func TestTheDeploymentReadsEveryReplicasAddressDiffersSamples(t *testing.T) {
	at := time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)
	sample := func(minute int, strategy string) AddressDiffersSample {
		return AddressDiffersSample{At: at.Add(time.Duration(minute) * time.Second), Cause: "differs", StrategyID: strategy, HostID: "11"}
	}
	var many []AddressDiffersSample
	for index := 0; index < MaxAddressDiffersSamples; index++ {
		many = append(many, sample(100-index, fmt.Sprint(300+index)))
	}
	view := Aggregate(Expectation{QueryGroups: 20, Known: true}, []Snapshot{
		{Replica: "pod-a", TakenAt: at, Owned: 10, Determined: 10, AddressDiffers: many},
		{Replica: "pod-b", TakenAt: at, Owned: 10, Determined: 10, AddressDiffers: []AddressDiffersSample{sample(200, "101")}},
		{Replica: "pod-c", TakenAt: at, Owned: 0, Determined: 0},
	}, []string{"pod-a", "pod-b", "pod-c"}, at, time.Minute)
	got := view.AddressDiffers
	if len(got) != MaxAddressDiffersSamples || got[0].StrategyID != "101" || got[0].Replica != "pod-b" || got[1].Replica != "pod-a" {
		t.Fatalf("deployment samples %+v, want pod-b's newest first, then pod-a's, bounded", got)
	}
	health, _ := json.Marshal(healthOf(&view, ReplicaPart{}, at))
	if !strings.Contains(string(health), `"address_differs_samples"`) {
		t.Fatalf("fleet.get carries no samples: %s", health)
	}
	encoded, _ := json.Marshal(Snapshot{Replica: "pod-c", TakenAt: at})
	if strings.Contains(string(encoded), "address_differs") {
		t.Fatalf("a replica with no samples published the key: %s", encoded)
	}
}
