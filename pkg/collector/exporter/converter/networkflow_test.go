// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package converter

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/networkflow"
)

func TestNetworkflowConverterToDataID(t *testing.T) {
	record := define.Record{
		RecordType: define.RecordNetworkFlow,
		Data:       &networkflow.Data{DataID: 320001},
	}
	var conv networkflowConverter
	assert.Equal(t, int32(320001), conv.ToDataID(&record))
}

func TestNetworkflowConverterConvert(t *testing.T) {
	record := define.Record{
		RecordType: define.RecordNetworkFlow,
		Data: &networkflow.Data{
			DataID:         320001,
			Type:           "NETFLOW_V5",
			SamplerAddress: "198.51.100.7",
			SrcAddr:        "10.0.0.1",
			DstAddr:        "10.0.0.2",
			SrcPort:        12345,
			DstPort:        80,
			Proto:          "TCP",
			Bytes:          1024,
			Packets:        8,
			LayerStack:     []string{"IPv4", "TCP"},
		},
	}

	events := make([]define.Event, 0)
	gather := func(evts ...define.Event) {
		events = append(events, evts...)
	}

	var conv networkflowConverter
	conv.Convert(&record, gather)

	require.Len(t, events, 1)
	evt := events[0]
	assert.Equal(t, define.RecordNetworkFlow, evt.RecordType())
	assert.Equal(t, int32(320001), evt.DataId())
	assert.Equal(t, "198.51.100.7", evt.Data()["sampler_address"])
	assert.Equal(t, "10.0.0.1", evt.Data()["src_addr"])
	assert.Equal(t, "TCP", evt.Data()["proto"])
	assert.Equal(t, "NETFLOW_V5", evt.Data()["type"])
	assert.Equal(t, []string{"IPv4", "TCP"}, evt.Data()["layer_stack"])
}

func TestFlowConvertFields(t *testing.T) {
	data := &networkflow.Data{
		DataID:                     320001,
		Type:                       "IPFIX",
		TimeReceivedNs:             1710000007000000000,
		SequenceNum:                17,
		SamplingRate:               1000,
		SamplerAddress:             "198.51.100.7",
		TimeFlowStartNs:            1710000000000000000,
		TimeFlowEndNs:              1710000005000000000,
		Bytes:                      123456,
		Packets:                    789,
		SrcAddr:                    "10.10.0.1",
		DstAddr:                    "10.20.0.2",
		Etype:                      "IPv4",
		Proto:                      "TCP",
		SrcPort:                    44321,
		DstPort:                    80,
		InIf:                       10,
		OutIf:                      20,
		SrcMac:                     "00:00:00:00:00:01",
		DstMac:                     "00:00:00:00:00:02",
		SrcVlan:                    11,
		DstVlan:                    12,
		VlanID:                     13,
		IpTos:                      14,
		ForwardingStatus:           15,
		IpTtl:                      16,
		IpFlags:                    17,
		TcpFlags:                   18,
		IcmpType:                   19,
		IcmpCode:                   20,
		Ipv6FlowLabel:              21,
		FragmentID:                 22,
		FragmentOffset:             23,
		SrcAs:                      24,
		DstAs:                      25,
		NextHop:                    "192.0.2.1",
		NextHopAs:                  26,
		SrcNet:                     "10.10.0.0/24",
		DstNet:                     "10.20.0.0/24",
		BgpNextHop:                 "198.51.100.8",
		BgpCommunities:             []uint32{100, 200},
		AsPath:                     []uint32{65001, 65002},
		MplsTtl:                    []uint32{30},
		MplsLabel:                  []uint32{1000},
		MplsIP:                     []string{"203.0.113.1"},
		ObservationDomainID:        27,
		ObservationPointID:         28,
		LayerStack:                 []string{"IPv4", "TCP"},
		LayerSize:                  []uint32{20, 32},
		Ipv6RoutingHeaderAddresses: []string{"2001:db8::1"},
		Ipv6RoutingHeaderSegLeft:   29,
	}
	record := &define.Record{RecordType: define.RecordNetworkFlow, Data: data}

	var conv networkflowConverter
	events := make([]define.Event, 0)
	conv.Convert(record, func(evts ...define.Event) { events = append(events, evts...) })

	require.Len(t, events, 1)
	d := events[0].Data()

	assert.Equal(t, int32(320001), events[0].DataId())
	assert.Equal(t, int32(320001), d["dataid"])
	assert.Equal(t, "198.51.100.7", d["sampler_address"])
	assert.Equal(t, "TCP", d["proto"])
	assert.Equal(t, "IPFIX", d["type"])
	assert.Equal(t, "10.10.0.1", d["src_addr"])
	assert.Equal(t, "10.20.0.2", d["dst_addr"])
	assert.Equal(t, []uint32{100, 200}, d["bgp_communities"])
	assert.Equal(t, []string{"203.0.113.1"}, d["mpls_ip"])
	assert.Equal(t, []string{"IPv4", "TCP"}, d["layer_stack"])
	assert.Nil(t, d["peer_ip"])
	assert.Nil(t, d["effective_exporter_key"])
	assert.Nil(t, d["proto_name"])
	assert.Nil(t, d["direction"])
	assert.Nil(t, d["flow_protocol_family"])
	assert.Nil(t, d["extra"])
}

func TestFlowConvertUsesDataID(t *testing.T) {
	record := &define.Record{
		RecordType: define.RecordNetworkFlow,
		Token:      define.Token{TracesDataId: 999999},
		Data:       &networkflow.Data{DataID: 320001},
	}

	var conv networkflowConverter
	events := make([]define.Event, 0)
	conv.Convert(record, func(evts ...define.Event) { events = append(events, evts...) })

	require.Len(t, events, 1)
	assert.Equal(t, int32(320001), events[0].DataId())
}
