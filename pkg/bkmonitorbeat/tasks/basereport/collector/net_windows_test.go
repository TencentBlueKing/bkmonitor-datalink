// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package collector_test

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/basereport/collector"
)

func Test_UdpProtoCountersWin(t *testing.T) {
	data, err := collector.UdpProtoCounters()
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 7 {
		t.Fatal("get udp counters data is wrong")
	}
	for _, v := range data {
		if v < 0 {
			t.Fatal("data is wrong")
		}
	}
	t.Log(data)
}

func Test_ProtoCountersWin(t *testing.T) {
	protocols := []string{"udp", "tcp", "ip"}
	data, err := collector.ProtoCounters(protocols)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 3 {
		t.Fatal("not get all protocol (udp、tcp、ip)")
	}
	for _, v := range data {
		if v.Protocol != "udp" && v.Protocol != "tcp" && v.Protocol != "ip" {
			t.Fatal("get wrong protocol")
		}
		if len(v.Stats) == 0 {
			t.Fatal("protocol get wrong data")
		}
	}
	t.Log(data)
}

func Test_GetTTLWin(t *testing.T) {
	data, err := collector.GetTTL()
	if err != nil {
		t.Fatal(err)
	}
	if data < 0 || data > 255 {
		t.Fatal("get TTL data is wrong")
	}
	t.Log(data)
}

func Test_IpProtoCountersWin(t *testing.T) {
	data, err := collector.IpProtoCounters()
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 19 {
		t.Fatal("get ip counters data is wrong")
	}
	for _, v := range data {
		if v < 0 {
			t.Fatal("data is wrong")
		}
	}
	t.Log(data)
}

func Test_TcpProtoCountersWin(t *testing.T) {
	data, err := collector.TcpProtoCounters()
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 15 {
		t.Fatal("get tcp counters data is wrong")
	}
	for _, v := range data {
		if v < 0 {
			t.Fatal("data is wrong")
		}
	}
	t.Log(data)
}

func Test_IcmpProtoCountersWin(t *testing.T) {
	data, err := collector.IcmpProtoCounters()
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 27 {
		t.Fatal("get icmp counters data is wrong")
	}
	for _, v := range data {
		if v < 0 {
			t.Fatal("data is wrong")
		}
	}
	t.Log(data)
}
