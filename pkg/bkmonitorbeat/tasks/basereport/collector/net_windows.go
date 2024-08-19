// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package collector

import (
	"bytes"
	"errors"
	"math"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/shirou/gopsutil/v3/net"
	"github.com/yusufpapurcu/wmi"
	"golang.org/x/sys/windows"

	bkcommon "github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/common"
)

// NetProtoCounters returns network statistics for the entire system
// If protocols is empty then all protocols are returned, otherwise
// just the protocols in the list are returned.
var netProtocols = []string{"udp", "ip", "tcp", "icmp"}

func ProtoCounters(protocols []string) ([]net.ProtoCountersStat, error) {
	if len(protocols) == 0 {
		protocols = netProtocols
	}

	stats := make([]net.ProtoCountersStat, 0, len(protocols))
	for _, v := range protocols {
		oneStat := net.ProtoCountersStat{
			Protocol: v,
			Stats:    make(map[string]int64),
		}
		var data map[string]int64
		var err error
		if v == "udp" {
			data, err = UdpProtoCounters()
		} else if v == "ip" {
			data, err = IpProtoCounters()
		} else if v == "tcp" {
			data, err = TcpProtoCounters()
		} else if v == "icmp" {
			data, err = IcmpProtoCounters()
		} else {
			return nil, errors.New("protocol not support")
		}
		if err != nil {
			return nil, err
		}
		for k, m := range data {
			oneStat.Stats[k] = m
		}
		stats = append(stats, oneStat)
	}
	return stats, nil
}

// get udp protocol counter
func UdpProtoCounters() (map[string]int64, error) {
	udpMap := make(map[string]int64)
	udpMap["InCsumErrors"] = -1
	udpMap["InDatagrams"] = -1
	udpMap["InErrors"] = -1
	udpMap["NoPorts"] = -1
	udpMap["OutDatagrams"] = -1
	udpMap["RcvbufErrors"] = -1
	udpMap["SndbufErrors"] = -1

	cmd := exec.Command("netsh", "interfac", "ipv4", "show", "udpstats")
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Start()
	cmd.Wait()

	outStr := buf.String()
	parts := regexp.MustCompile("(?m)^\\s*-+\\s*$").Split(outStr, 2)

	if len(parts) == 2 {
		part := parts[1]
		results := regexp.MustCompile("(?m)^.*:\\s*(\\d+)").FindAllStringSubmatch(part, -1)
		if len(results) == 4 {
			udpMap["InDatagrams"], _ = strconv.ParseInt(results[0][1], 10, 64)
			udpMap["NoPorts"], _ = strconv.ParseInt(results[1][1], 10, 64)
			udpMap["InErrors"], _ = strconv.ParseInt(results[2][1], 10, 64)
			udpMap["OutDatagrams"], _ = strconv.ParseInt(results[3][1], 10, 64)
		}
	}
	return udpMap, nil
}

// get ip protocol counter
func IpProtoCounters() (map[string]int64, error) {
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
		ttlInt, err := GetTTL()
		if err != nil {
			ipMap["DefaultTTL"] = 0
		} else {
			ipMap["DefaultTTL"] = ttlInt
		}

		ipMap["ForwDatagrams"] = dst[0].DatagramsForwardedPersec
		ipMap["Forwarding"] = 0
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
		ipMap["ReasmReqds"] = 0
		ipMap["ReasmTimeout"] = 0
		return ipMap, nil
	}
}

// get windows DefaultTTL
func GetTTL() (int64, error) {
	// get default ttl cmd
	cmdStr := "REG QUERY HKEY_LOCAL_MACHINE\\SYSTEM\\CurrentControlSet\\services\\Tcpip\\Parameters /v DefaultTTL | findstr /i DefaultTTL"
	cmd := exec.Command("cmd", "/c", cmdStr)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Start()

	// use a channel to signal completion
	var outStr string
	done := make(chan error)
	go func() { done <- cmd.Wait() }()

	// start a timer
	timeout := time.After(10 * time.Second)
	select {
	case <-timeout:
		return 0, errors.New("time out")
	case err := <-done:
		if err != nil {
			return 0, err
		}
		outStr = buf.String()
	}
	lineStr := strings.Split(outStr, "\n")[0]
	arr1 := strings.Split(lineStr, " ")
	if len(arr1) == 0 {
		return 0, errors.New("can not get default TTL")
	}
	ttlStr := strings.Trim(arr1[len(arr1)-1], "\r")
	if len(ttlStr) == 0 {
		return 0, errors.New("default TTL is null")
	}
	ttlInt, err := strconv.ParseInt(strings.TrimLeft(ttlStr, "0x"), 16, 64)
	if err != nil {
		return 0, errors.New("TTL data convert to int error")
	}
	return ttlInt, nil
}

// get tcp protocol counter
func TcpProtoCounters() (map[string]int64, error) {
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
		tcpMap["InCsumErrors"] = 0
		tcpMap["InErrs"] = 0
		tcpMap["InSegs"] = dst[0].SegmentsReceivedPersec
		tcpMap["MaxConn"] = 0
		tcpMap["OutRsts"] = 0
		tcpMap["OutSegs"] = dst[0].SegmentsSentPersec
		tcpMap["PassiveOpens"] = dst[0].ConnectionsPassive
		tcpMap["RetransSegs"] = dst[0].SegmentsRetransmittedPersec
		tcpMap["RtoAlgorithm"] = 0
		tcpMap["RtoMax"] = 0
		tcpMap["RtoMin"] = 0
		return tcpMap, nil
	}
}

// get icmp protocol counter
func IcmpProtoCounters() (map[string]int64, error) {
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

const (
	NetCoutnerMaxSize = math.MaxUint32
)

// 从 net/interface_windows.go 中复制过来
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

func initVirtualInterfaceSet() error {
	interfaceSet := bkcommon.NewSet()
	aas, err := adapterAddresses()
	if err != nil {
		return err
	}
	for _, aa := range aas {
		// windows目前只屏蔽本地回环
		if aa.IfType == windows.IF_TYPE_SOFTWARE_LOOPBACK {
			friendlyName := UTF16PtrToString(aa.FriendlyName, 1000)
			interfaceSet.Insert(friendlyName)
		}
	}
	virtualInterfaceSet = interfaceSet
	return nil
}

// not implemented
func GetNetInfoFromDev() (map[string]NetInfo, error) {
	// return nil, fmt.Errorf("get netinfo from dev not implemented in windows")
	return nil, nil
}

// borrowed from net/interface_windows.go
func UTF16PtrToString(p *uint16, max int) string {
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
