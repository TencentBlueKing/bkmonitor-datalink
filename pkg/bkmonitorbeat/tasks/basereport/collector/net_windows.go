// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build windows

package collector

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/shirou/gopsutil/v3/net"
	"github.com/yusufpapurcu/wmi"
	"golang.org/x/sys/windows"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/common"
)

// borrowed from net/interface_windows.go
func adapterAddresses() ([]*windows.IpAdapterAddresses, error) {
	var b []byte
	l := uint32(15000) // recommended initial size
	for {
		b = make([]byte, l)
		err := windows.GetAdaptersAddresses(syscall.AF_UNSPEC, windows.GAA_FLAG_INCLUDE_PREFIX, 0, (*windows.IpAdapterAddresses)(unsafe.Pointer(&b[0])), &l)
		if err == nil {
			if l == 0 {
				return nil, nil
			}
			break
		}
		if err.(syscall.Errno) != syscall.ERROR_BUFFER_OVERFLOW {
			return nil, os.NewSyscallError("getadaptersaddresses", err)
		}
		if l <= uint32(len(b)) {
			return nil, os.NewSyscallError("getadaptersaddresses", err)
		}
	}
	var aas []*windows.IpAdapterAddresses
	for aa := (*windows.IpAdapterAddresses)(unsafe.Pointer(&b[0])); aa != nil; aa = aa.Next {
		aas = append(aas, aa)
	}
	return aas, nil
}

// borrowed from net/interface_windows.go
func utf16PtrToString(p *uint16, max int) string {
	if p == nil {
		return ""
	}
	// Find NUL terminator.
	end := unsafe.Pointer(p)
	n := 0
	for *(*uint16)(end) != 0 && n < max {
		end = unsafe.Pointer(uintptr(end) + unsafe.Sizeof(*p))
		n++
	}
	s := (*[(1 << 30) - 1]uint16)(unsafe.Pointer(p))[:n:n]
	return string(utf16.Decode(s))
}

func InitVirtualInterfaceSet() error {
	interfaceSet := common.NewSet()
	aas, err := adapterAddresses()
	if err != nil {
		return err
	}
	for _, aa := range aas {
		// windows 目前只屏蔽本地回环
		if aa.IfType == windows.IF_TYPE_SOFTWARE_LOOPBACK {
			friendlyName := utf16PtrToString(aa.FriendlyName, 1000)
			interfaceSet.Insert(friendlyName)
		}
	}
	virtualInterfaceSet = interfaceSet
	return nil
}

func ProtoCounters(protocols []string) ([]net.ProtoCountersStat, error) {
	stats := make([]net.ProtoCountersStat, 0, len(protocols))
	for _, v := range protocols {
		var data map[string]int64
		var err error
		switch v {
		case "udp":
			data, err = getUDPProtoCounters()
		case "ip":
			data, err = getIPProtoCounters()
		case "tcp":
			data, err = getTCPProtoCounters()
		case "icmp":
			data, err = getICMPProtoCounters()
		default:
			return nil, errors.New("protocol not support")
		}
		if err != nil {
			return nil, err
		}

		stat := net.ProtoCountersStat{
			Protocol: v,
			Stats:    make(map[string]int64),
		}
		for k, m := range data {
			stat.Stats[k] = m
		}
		stats = append(stats, stat)
	}
	return stats, nil
}

// getUDPProtoCounters 我也不知道为什么只有 udp 使用 cmd 获取数据 ┓(´∀`)┏
// only God knows
func getUDPProtoCounters() (map[string]int64, error) {
	var buf bytes.Buffer
	cmd := exec.Command("netsh", "interface", "ipv4", "show", "udpstats")
	cmd.Stdout = &buf
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	udpMap := make(map[string]int64)
	regex := regexp.MustCompile("(?m)^\\s*-+\\s*$")
	parts := regex.Split(buf.String(), 2)
	if len(parts) == 2 {
		part := parts[1]
		re := regexp.MustCompile("(?m)^.*:\\s*(\\d+)")
		results := re.FindAllStringSubmatch(part, -1)
		if len(results) == 4 {
			udpMap["InDatagrams"], _ = strconv.ParseInt(results[0][1], 10, 64)
			udpMap["NoPorts"], _ = strconv.ParseInt(results[1][1], 10, 64)
			udpMap["InErrors"], _ = strconv.ParseInt(results[2][1], 10, 64)
			udpMap["OutDatagrams"], _ = strconv.ParseInt(results[3][1], 10, 64)
		}
	}
	return udpMap, nil
}

func getIPProtoCounters() (map[string]int64, error) {
	type Win32_PerfFormattedData_Tcpip_IPv4 struct {
		DatagramsReceivedDeliveredPersec int64
		DatagramsReceivedAddressErrors   int64
		DatagramsForwardedPersec         int64
		FragmentsCreatedPersec           int64
		FragmentationFailures            int64
		FragmentsReceivedPersec          int64
		DatagramsReceivedDiscarded       int64
		DatagramsReceivedHeaderErrors    int64
		DatagramsReceivedPersec          int64
		DatagramsReceivedUnknownProtocol int64
		DatagramsOutboundDiscarded       int64
		DatagramsOutboundNoRoute         int64
		DatagramsSentPersec              int64
		FragmentReassemblyFailures       int64
		FragmentsReassembledPersec       int64
	}

	ipMap := make(map[string]int64)
	var dst []Win32_PerfFormattedData_Tcpip_IPv4
	q := wmi.CreateQuery(&dst, "")

	done := make(chan error)
	go func() { done <- wmi.Query(q, &dst) }()

	timeout := time.After(30 * time.Second)
	select {
	case <-timeout:
		return ipMap, errors.New("time out")
	case err := <-done:
		if err != nil {
			return ipMap, err
		}

		ipMap["ForwDatagrams"] = dst[0].DatagramsForwardedPersec
		ipMap["FragCreates"] = dst[0].FragmentsCreatedPersec
		ipMap["FragFails"] = dst[0].FragmentationFailures
		ipMap["FragOKs"] = dst[0].FragmentsReceivedPersec
		ipMap["InAddrErrors"] = dst[0].DatagramsReceivedAddressErrors
		ipMap["InDelivers"] = dst[0].DatagramsReceivedDeliveredPersec
		ipMap["InDiscards"] = dst[0].DatagramsReceivedDiscarded
		ipMap["InHdrErrors"] = dst[0].DatagramsReceivedHeaderErrors
		ipMap["InReceives"] = dst[0].DatagramsReceivedPersec
		ipMap["InUnknownProtos"] = dst[0].DatagramsReceivedUnknownProtocol
		ipMap["OutDiscards"] = dst[0].DatagramsOutboundDiscarded
		ipMap["OutNoRoutes"] = dst[0].DatagramsOutboundNoRoute
		ipMap["OutRequests"] = dst[0].DatagramsSentPersec
		ipMap["ReasmFails"] = dst[0].FragmentReassemblyFailures
		ipMap["ReasmOKs"] = dst[0].FragmentsReassembledPersec
		return ipMap, nil
	}
}

func getTCPProtoCounters() (map[string]int64, error) {
	type Win32_PerfFormattedData_Tcpip_TCPv4 struct {
		SegmentsRetransmittedPersec int64
		SegmentsReceivedPersec      int64
		SegmentsSentPersec          int64
		SegmentsPersec              int64
		ConnectionFailures          int64
		ConnectionsActive           int64
		ConnectionsEstablished      int64
		ConnectionsPassive          int64
		ConnectionsReset            int64
	}

	tcpMap := make(map[string]int64)
	var dst []Win32_PerfFormattedData_Tcpip_TCPv4
	q := wmi.CreateQuery(&dst, "")

	done := make(chan error)
	go func() { done <- wmi.Query(q, &dst) }()

	timeout := time.After(30 * time.Second)
	select {
	case <-timeout:
		return tcpMap, errors.New("time out")
	case err := <-done:
		if err != nil {
			return tcpMap, err
		}

		tcpMap["ActiveOpens"] = dst[0].ConnectionsActive
		tcpMap["AttemptFails"] = dst[0].ConnectionFailures
		tcpMap["CurrEstab"] = dst[0].ConnectionsEstablished
		tcpMap["EstabResets"] = dst[0].ConnectionsReset
		tcpMap["InSegs"] = dst[0].SegmentsReceivedPersec
		tcpMap["OutSegs"] = dst[0].SegmentsSentPersec
		tcpMap["PassiveOpens"] = dst[0].ConnectionsPassive
		tcpMap["RetransSegs"] = dst[0].SegmentsRetransmittedPersec
		return tcpMap, nil
	}
}

func getICMPProtoCounters() (map[string]int64, error) {
	type Win32_PerfFormattedData_Tcpip_ICMP struct {
		ReceivedAddressMaskReply     int64
		ReceivedAddressMask          int64
		ReceivedDestUnreachable      int64
		ReceivedEchoReplyPersec      int64
		ReceivedEchoPersec           int64
		MessagesReceivedErrors       int64
		MessagesReceivedPersec       int64
		ReceivedParameterProblem     int64
		ReceivedRedirectPersec       int64
		ReceivedSourceQuench         int64
		ReceivedTimeExceeded         int64
		ReceivedTimestampReplyPersec int64
		ReceivedTimestampPersec      int64
		SentAddressMaskReply         int64
		SentAddressMask              int64
		SentDestinationUnreachable   int64
		SentEchoReplyPersec          int64
		SentEchoPersec               int64
		MessagesOutboundErrors       int64
		MessagesSentPersec           int64
		SentParameterProblem         int64
		SentRedirectPersec           int64
		SentSourceQuench             int64
		SentTimeExceeded             int64
		SentTimestampReplyPersec     int64
		SentTimestampPersec          int64
	}

	icmpMap := make(map[string]int64)
	var dst []Win32_PerfFormattedData_Tcpip_ICMP
	q := wmi.CreateQuery(&dst, "")

	done := make(chan error)
	go func() { done <- wmi.Query(q, &dst) }()

	timeout := time.After(30 * time.Second)
	select {
	case <-timeout:
		return icmpMap, errors.New("time out")
	case err := <-done:
		if err != nil {
			return icmpMap, err
		}

		icmpMap["InAddrMaskReps"] = dst[0].ReceivedAddressMaskReply
		icmpMap["InAddrMasks"] = dst[0].ReceivedAddressMask
		icmpMap["InCsumErrors"] = 0
		icmpMap["InDestUnreachs"] = dst[0].ReceivedDestUnreachable
		icmpMap["InEchoReps"] = dst[0].ReceivedEchoReplyPersec
		icmpMap["InEchos"] = dst[0].ReceivedEchoPersec
		icmpMap["InErrors"] = dst[0].MessagesReceivedErrors
		icmpMap["InMsgs"] = dst[0].MessagesReceivedPersec
		icmpMap["InParmProbs"] = dst[0].ReceivedParameterProblem
		icmpMap["InRedirects"] = dst[0].ReceivedRedirectPersec
		icmpMap["InSrcQuenchs"] = dst[0].ReceivedSourceQuench
		icmpMap["InTimeExcds"] = dst[0].ReceivedTimeExceeded
		icmpMap["InTimestampReps"] = dst[0].ReceivedTimestampReplyPersec
		icmpMap["InTimestamps"] = dst[0].ReceivedTimestampPersec
		icmpMap["OutAddrMaskReps"] = dst[0].SentAddressMaskReply
		icmpMap["OutAddrMasks"] = dst[0].SentAddressMask
		icmpMap["OutDestUnreachs"] = dst[0].SentDestinationUnreachable
		icmpMap["OutEchoReps"] = dst[0].SentEchoReplyPersec
		icmpMap["OutEchos"] = dst[0].SentEchoPersec
		icmpMap["OutErrors"] = dst[0].MessagesOutboundErrors
		icmpMap["OutMsgs"] = dst[0].MessagesSentPersec
		icmpMap["OutParmProbs"] = dst[0].SentParameterProblem
		icmpMap["OutRedirects"] = dst[0].SentRedirectPersec
		icmpMap["OutSrcQuenchs"] = dst[0].SentSourceQuench
		icmpMap["OutTimeExcds"] = dst[0].SentTimeExceeded
		icmpMap["OutTimestampReps"] = dst[0].SentTimestampReplyPersec
		icmpMap["OutTimestamps"] = dst[0].SentTimestampPersec
		return icmpMap, nil
	}
}
