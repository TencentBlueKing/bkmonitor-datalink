// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package networkflow

import (
	"fmt"
	"net"
	"net/netip"

	flowpb "github.com/netsampler/goflow2/v2/pb"
	"github.com/netsampler/goflow2/v2/producer"
	protoproducer "github.com/netsampler/goflow2/v2/producer/proto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

type flowRecordProducer struct {
	dataID  int32
	publish RecordPublisher
	inner   producer.ProducerInterface
}

type noopProtoProducerConfig struct{}

func (noopProtoProducerConfig) GetFormatter() protoproducer.FormatterMapper { return nil }

func (noopProtoProducerConfig) GetIPFIXMapper() protoproducer.TemplateMapper { return nil }

func (noopProtoProducerConfig) GetNetFlowMapper() protoproducer.TemplateMapper { return nil }

func (noopProtoProducerConfig) GetPacketMapper() protoproducer.PacketMapper { return nil }

func newFlowRecordProducer(dataID int32, publish RecordPublisher) (*flowRecordProducer, error) {
	inner, err := protoproducer.CreateProtoProducer(noopProtoProducerConfig{}, protoproducer.CreateSamplingSystem)
	if err != nil {
		return nil, err
	}
	return &flowRecordProducer{dataID: dataID, publish: publish, inner: inner}, nil
}

func (p *flowRecordProducer) Produce(msg interface{}, args *producer.ProduceArgs) ([]producer.ProducerMessage, error) {
	flowMessages, err := p.inner.Produce(msg, args)
	if err != nil {
		return flowMessages, err
	}

	for _, item := range flowMessages {
		flowMessage, ok := item.(*protoproducer.ProtoProducerMessage)
		if !ok {
			return flowMessages, fmt.Errorf("unexpected producer message type %T", item)
		}
		flowData := mapFlowData(p.dataID, flowMessage)
		if p.publish != nil {
			p.publish(&define.Record{
				RecordType:    define.RecordNetworkFlow,
				RequestType:   define.RequestUDP,
				RequestClient: define.RequestClient{IP: flowData.SamplerAddress},
				Data:          flowData,
			})
		}
	}
	return flowMessages, nil
}

func (p *flowRecordProducer) Commit(msgs []producer.ProducerMessage) {
	p.inner.Commit(msgs)
}

func (p *flowRecordProducer) Close() {
	p.inner.Close()
}

func mapFlowData(dataID int32, msg *protoproducer.ProtoProducerMessage) *Data {
	data := &Data{DataID: dataID}
	fillCoreFields(data, msg)
	fillAddressFields(data, msg)
	fillInterfaceAndLinkFields(data, msg)
	fillNetworkExtensionFields(data, msg)
	return data
}

func fillCoreFields(data *Data, msg *protoproducer.ProtoProducerMessage) {
	data.Type = flowTypeName(msg.Type)
	data.TimeReceivedNs = int64(msg.TimeReceivedNs)
	data.SequenceNum = msg.SequenceNum
	data.SamplingRate = msg.SamplingRate
	data.SamplerAddress = bytesToAddrString(msg.SamplerAddress)
	data.TimeFlowStartNs = int64(msg.TimeFlowStartNs)
	data.TimeFlowEndNs = int64(msg.TimeFlowEndNs)
	data.Bytes = msg.Bytes
	data.Packets = msg.Packets
	data.Etype = etherTypeName(msg.Etype)
	data.Proto = protoName(msg.Proto)
}

func fillAddressFields(data *Data, msg *protoproducer.ProtoProducerMessage) {
	data.SrcAddr = bytesToAddrString(msg.SrcAddr)
	data.DstAddr = bytesToAddrString(msg.DstAddr)
	data.SrcPort = msg.SrcPort
	data.DstPort = msg.DstPort
	data.NextHop = bytesToAddrString(msg.NextHop)
	data.NextHopAs = msg.NextHopAs
	data.SrcNet = networkString(msg.SrcAddr, msg.SrcNet)
	data.DstNet = networkString(msg.DstAddr, msg.DstNet)
	data.BgpNextHop = bytesToAddrString(msg.BgpNextHop)
}

func fillInterfaceAndLinkFields(data *Data, msg *protoproducer.ProtoProducerMessage) {
	data.InIf = msg.InIf
	data.OutIf = msg.OutIf
	data.SrcMac = macAddressString(msg.SrcMac)
	data.DstMac = macAddressString(msg.DstMac)
	data.SrcVlan = msg.SrcVlan
	data.DstVlan = msg.DstVlan
	data.VlanID = msg.VlanId
	data.IpTos = msg.IpTos
	data.ForwardingStatus = msg.ForwardingStatus
	data.IpTtl = msg.IpTtl
	data.IpFlags = msg.IpFlags
	data.TcpFlags = msg.TcpFlags
	data.IcmpType = msg.IcmpType
	data.IcmpCode = msg.IcmpCode
	data.Ipv6FlowLabel = msg.Ipv6FlowLabel
	data.FragmentID = msg.FragmentId
	data.FragmentOffset = msg.FragmentOffset
}

func fillNetworkExtensionFields(data *Data, msg *protoproducer.ProtoProducerMessage) {
	data.SrcAs = msg.SrcAs
	data.DstAs = msg.DstAs
	data.BgpCommunities = cloneUint32Slice(msg.BgpCommunities)
	data.AsPath = cloneUint32Slice(msg.AsPath)
	data.MplsTtl = cloneUint32Slice(msg.MplsTtl)
	data.MplsLabel = cloneUint32Slice(msg.MplsLabel)
	data.MplsIP = bytesSliceToAddrStrings(msg.MplsIp)
	data.ObservationDomainID = msg.ObservationDomainId
	data.ObservationPointID = msg.ObservationPointId
	data.LayerStack = layerStackNames(msg.LayerStack)
	data.LayerSize = cloneUint32Slice(msg.LayerSize)
	data.Ipv6RoutingHeaderAddresses = bytesSliceToAddrStrings(msg.Ipv6RoutingHeaderAddresses)
	data.Ipv6RoutingHeaderSegLeft = msg.Ipv6RoutingHeaderSegLeft
}

func cloneUint32Slice(items []uint32) []uint32 {
	if len(items) == 0 {
		return nil
	}
	return append([]uint32(nil), items...)
}

func flowTypeName(flowType flowpb.FlowMessage_FlowType) string {
	if flowType == flowpb.FlowMessage_FLOWUNKNOWN {
		return ""
	}
	return flowType.String()
}

func protoName(proto uint32) string {
	return protoproducer.ProtoName(proto)
}

func etherTypeName(etherType uint32) string {
	switch etherType {
	case 0x0806:
		return "ARP"
	case 0x0800:
		return "IPv4"
	case 0x86dd:
		return "IPv6"
	default:
		return "unknown"
	}
}

func macAddressString(raw uint64) string {
	mac := net.HardwareAddr{
		byte(raw >> 40),
		byte(raw >> 32),
		byte(raw >> 24),
		byte(raw >> 16),
		byte(raw >> 8),
		byte(raw),
	}
	return mac.String()
}

func networkString(raw []byte, prefixBits uint32) string {
	if len(raw) == 0 {
		return ""
	}
	addr, ok := netip.AddrFromSlice(raw)
	if !ok {
		return ""
	}
	prefix, err := normalizeAddr(addr).Prefix(int(prefixBits))
	if err != nil {
		return ""
	}
	return prefix.String()
}

func bytesSliceToAddrStrings(items [][]byte) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, bytesToAddrString(item))
	}
	return out
}

func layerStackNames(items []flowpb.FlowMessage_LayerStack) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.String())
	}
	return out
}

func bytesToAddrString(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	addr, ok := netip.AddrFromSlice(raw)
	if !ok {
		return ""
	}
	return normalizeAddr(addr).String()
}
