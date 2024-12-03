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
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/basereport/collector"
)

func TestGetTcp4SocketStatusCount(t *testing.T) {
	out, err := collector.GetTcp4SocketStatusCount()
	if err != nil {
		t.Fatal(err)
	}
	v := reflect.ValueOf(out)
	count := v.NumField()
	if count != 11 {
		t.Fatal("the number of return data is wrong")
	}
	for i := 0; i < count; i++ {
		if v.Field(i).Uint() < 0 {
			t.Fatal("the return data is wrong")
		}
	}
	t.Log(out)
}

func TestGetAllTcp4Socket(t *testing.T) {
	map1 := make(map[uint16]bool)
	map1[135] = true
	map1[445] = true
	map1[49152] = true
	data1, err1 := collector.GetAllTcp4Socket(collector.NoneSocketFilter{})
	data2, err2 := collector.GetAllTcp4Socket(collector.TcpSocketListenFilter{})
	data3, err3 := collector.GetAllTcp4Socket(collector.TcpSocketListenPortFilter{ListenPorts: map1})
	if err1 != nil {
		t.Fatal(err1)
	}
	if err2 != nil {
		t.Fatal(err2)
	}
	if err3 != nil {
		t.Fatal(err3)
	}
	for k, v := range data1 {
		if k < 0 {
			t.Fatal("Pid data is wrong")
		}
		for _, e := range v.Element {
			if e.Stat < 1 || e.Stat > 11 {
				t.Fatal("Stat data is wrong")
			}
			if e.SrcPort < 0 || e.SrcPort > 65535 {
				t.Fatal("SrcPort data is wrong")
			}
			if e.DstPort < 0 || e.DstPort > 65535 {
				t.Fatal("DstPort data is wrong")
			}
			if e.SrcIp < 0 || e.SrcIp > 4294967295 {
				t.Fatal("SrcIp data is wrong")
			}
			if e.DstIp < 0 || e.DstIp > 4294967295 {
				t.Fatal("DstIp data is wrong")
			}
		}
	}
	for k, v := range data2 {
		if k < 0 {
			t.Fatal("Pid data is wrong")
		}
		for _, e := range v.Element {
			if e.Stat != collector.TCP_LISTEN {
				t.Fatal("filter tcp listening socket is wrong")
			}
		}
	}
	for k, v := range data3 {
		if k < 0 {
			t.Fatal("Pid data is wrong")
		}
		for _, e := range v.Element {
			if _, ok := map1[e.SrcPort]; !ok {
				t.Fatal("filter tcp listening ports is wrong")
			}
		}
	}
	t.Log(data1)
	t.Log(data2)
	t.Log(data3)
}

func TestGetAllUdp4Socket(t *testing.T) {
	map1 := make(map[uint16]bool)
	map1[123] = true
	map1[137] = true
	map1[138] = true
	data1, err1 := collector.GetAllUdp4Socket(collector.NoneSocketFilter{})
	data2, err2 := collector.GetAllUdp4Socket(collector.UdpSocketListenPortFilter{ListenPorts: map1})
	if err1 != nil {
		t.Fatal(err1)
	}
	if err2 != nil {
		t.Fatal(err2)
	}
	for k, v := range data1 {
		if k < 0 {
			t.Fatal("Pid data is wrong")
		}
		for _, e := range v.Element {
			if e.Stat != 7 {
				t.Fatal("Stat data is wrong")
			}
			if e.SrcPort < 0 || e.SrcPort > 65535 {
				t.Fatal("SrcPort data is wrong")
			}
			if e.DstPort < 0 || e.DstPort > 65535 {
				t.Fatal("DstPort data is wrong")
			}
			if e.SrcIp < 0 || e.SrcIp > 4294967295 {
				t.Fatal("SrcIp data is wrong")
			}
			if e.DstIp < 0 || e.DstIp > 4294967295 {
				t.Fatal("DstIp data is wrong")
			}
		}
	}
	for k, v := range data2 {
		if k < 0 {
			t.Fatal("Pid data is wrong")
		}
		for _, e := range v.Element {
			if _, ok := map1[e.SrcPort]; !ok {
				t.Fatal("filter udp socket ports is wrong")
			}
		}
	}
	t.Log(data1)
	t.Log(data2)
}

func Test_IpToInt(t *testing.T) {
	var ip string = "127.0.0.1"
	var ipInt uint32 = 3232238081
	data := collector.IpToInt(ip)
	if data != ipInt {
		t.Fatalf("test is not passed")
	}
	t.Log("test is passed")
}

func Test_RemoveEmpty(t *testing.T) {
	arr1 := []string{"name", "", "age", "", "", "class"}
	arr2 := []string{"name", "age", "class"}
	data := collector.RemoveEmpty(arr1)
	if len(data) != len(arr2) {
		t.Fatalf("test is not passed")
	}
	for i, v := range data {
		if v != arr2[i] {
			t.Fatalf("test is not passed")
		}
	}
	t.Log("test is passed")
}
