// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package networkflow

import "github.com/elastic/beats/libbeat/common"

// Data freezes the collector output contract for network flow records.
// It keeps dataid as the only collector-added field and follows goflow2 JSON keys for the rest.
type Data struct {
	DataID int32 `json:"dataid"`

	Type           string `json:"type"`
	TimeReceivedNs int64  `json:"time_received_ns"`
	SequenceNum    uint32 `json:"sequence_num"`
	SamplingRate   uint64 `json:"sampling_rate"`

	SamplerAddress string `json:"sampler_address"`

	TimeFlowStartNs int64 `json:"time_flow_start_ns"`
	TimeFlowEndNs   int64 `json:"time_flow_end_ns"`

	Bytes   uint64 `json:"bytes"`
	Packets uint64 `json:"packets"`

	SrcAddr string `json:"src_addr"`
	DstAddr string `json:"dst_addr"`

	Etype string `json:"etype"`
	Proto string `json:"proto"`

	SrcPort uint32 `json:"src_port"`
	DstPort uint32 `json:"dst_port"`
	InIf    uint32 `json:"in_if"`
	OutIf   uint32 `json:"out_if"`

	SrcMac string `json:"src_mac"`
	DstMac string `json:"dst_mac"`

	SrcVlan uint32 `json:"src_vlan"`
	DstVlan uint32 `json:"dst_vlan"`
	VlanID  uint32 `json:"vlan_id"`

	IpTos            uint32 `json:"ip_tos"`
	ForwardingStatus uint32 `json:"forwarding_status"`
	IpTtl            uint32 `json:"ip_ttl"`
	IpFlags          uint32 `json:"ip_flags"`
	TcpFlags         uint32 `json:"tcp_flags"`
	IcmpType         uint32 `json:"icmp_type"`
	IcmpCode         uint32 `json:"icmp_code"`
	Ipv6FlowLabel    uint32 `json:"ipv6_flow_label"`

	FragmentID     uint32 `json:"fragment_id"`
	FragmentOffset uint32 `json:"fragment_offset"`

	SrcAs     uint32 `json:"src_as"`
	DstAs     uint32 `json:"dst_as"`
	NextHop   string `json:"next_hop"`
	NextHopAs uint32 `json:"next_hop_as"`
	SrcNet    string `json:"src_net"`
	DstNet    string `json:"dst_net"`

	BgpNextHop     string   `json:"bgp_next_hop"`
	BgpCommunities []uint32 `json:"bgp_communities"`
	AsPath         []uint32 `json:"as_path"`

	MplsTtl   []uint32 `json:"mpls_ttl"`
	MplsLabel []uint32 `json:"mpls_label"`
	MplsIP    []string `json:"mpls_ip"`

	ObservationDomainID uint32 `json:"observation_domain_id"`
	ObservationPointID  uint32 `json:"observation_point_id"`

	LayerStack []string `json:"layer_stack"`
	LayerSize  []uint32 `json:"layer_size"`

	Ipv6RoutingHeaderAddresses []string `json:"ipv6_routing_header_addresses"`
	Ipv6RoutingHeaderSegLeft   uint32   `json:"ipv6_routing_header_seg_left"`
}

func (d *Data) ToMapStr() common.MapStr {
	return common.MapStr{
		"dataid":                        d.DataID,
		"type":                          d.Type,
		"time_received_ns":              d.TimeReceivedNs,
		"sequence_num":                  d.SequenceNum,
		"sampling_rate":                 d.SamplingRate,
		"sampler_address":               d.SamplerAddress,
		"time_flow_start_ns":            d.TimeFlowStartNs,
		"time_flow_end_ns":              d.TimeFlowEndNs,
		"bytes":                         d.Bytes,
		"packets":                       d.Packets,
		"src_addr":                      d.SrcAddr,
		"dst_addr":                      d.DstAddr,
		"etype":                         d.Etype,
		"proto":                         d.Proto,
		"src_port":                      d.SrcPort,
		"dst_port":                      d.DstPort,
		"in_if":                         d.InIf,
		"out_if":                        d.OutIf,
		"src_mac":                       d.SrcMac,
		"dst_mac":                       d.DstMac,
		"src_vlan":                      d.SrcVlan,
		"dst_vlan":                      d.DstVlan,
		"vlan_id":                       d.VlanID,
		"ip_tos":                        d.IpTos,
		"forwarding_status":             d.ForwardingStatus,
		"ip_ttl":                        d.IpTtl,
		"ip_flags":                      d.IpFlags,
		"tcp_flags":                     d.TcpFlags,
		"icmp_type":                     d.IcmpType,
		"icmp_code":                     d.IcmpCode,
		"ipv6_flow_label":               d.Ipv6FlowLabel,
		"fragment_id":                   d.FragmentID,
		"fragment_offset":               d.FragmentOffset,
		"src_as":                        d.SrcAs,
		"dst_as":                        d.DstAs,
		"next_hop":                      d.NextHop,
		"next_hop_as":                   d.NextHopAs,
		"src_net":                       d.SrcNet,
		"dst_net":                       d.DstNet,
		"bgp_next_hop":                  d.BgpNextHop,
		"bgp_communities":               d.BgpCommunities,
		"as_path":                       d.AsPath,
		"mpls_ttl":                      d.MplsTtl,
		"mpls_label":                    d.MplsLabel,
		"mpls_ip":                       d.MplsIP,
		"observation_domain_id":         d.ObservationDomainID,
		"observation_point_id":          d.ObservationPointID,
		"layer_stack":                   d.LayerStack,
		"layer_size":                    d.LayerSize,
		"ipv6_routing_header_addresses": d.Ipv6RoutingHeaderAddresses,
		"ipv6_routing_header_seg_left":  d.Ipv6RoutingHeaderSegLeft,
	}
}
