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
	"math/big"
	sysnet "net"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// socket info
type SocketInfo struct {
	BaseSocketInfo
	Pid   uint64
	Inode uint64
	Type  uint32 // syscall.SOCK_STREAM or syscall.SOCK_DGR
}

// get all tcp socket info
// return map[pid]<socket list>
func GetAllTcp4Socket(filter SocketFilter) (map[uint64]ElementInfo, error) {
	// tcp status => int
	socketStat := make(map[string]uint8)
	socketStat["ESTABLISHED"] = 1
	socketStat["SYN_SENT"] = 2
	socketStat["SYN_RECV"] = 3
	socketStat["FIN_WAIT_1"] = 4
	socketStat["FIN_WAIT_2"] = 5
	socketStat["TIME_WAIT"] = 6
	socketStat["CLOSE"] = 7
	socketStat["CLOSE_WAIT"] = 8
	socketStat["LAST_ACK"] = 9
	socketStat["LISTENING"] = 10
	socketStat["CLOSING"] = 11

	res := make(map[uint64]ElementInfo)
	// get all tcp socket cmd
	cmdStr := "netstat -ano -p TCP |more +4"
	cmd := exec.Command("cmd", "/c", cmdStr)
	// use a bytes.Buffer to get the output
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Start()

	// use a channel to signal completion
	done := make(chan error)
	go func() { done <- cmd.Wait() }()

	// start a timer
	timeout := time.After(5 * time.Second)

	// select the command operation result
	var outStr string
	select {
	case <-timeout:
		cmd.Process.Kill()
		return res, errors.New("get tcp socket info time out")
	case err := <-done:
		if err != nil {
			return res, errors.New("get tcp socket info failed")
		}
		outStr = buf.String()
	}

	// format tcp socket data
	// outStr :   TCP    0.0.0.0:135            0.0.0.0:0              LISTENING       964
	//  TCP    10.0.0.1:51095       0.0.0.0:22       ESTABLISHED     10464
	//  TCP    10.0.0.1:51280       10.0.0.1:80        ESTABLISHED     1748
	//  TCP    10.0.0.1:51327       10.0.0.1:28812      ESTABLISHED     2964
	//  TCP    10.0.0.1:51440       10.0.0.1:8080      CLOSE_WAIT      4052
	//  TCP    10.0.0.1:51441       10.0.0.1:8080      CLOSE_WAIT      4052
	//  TCP    10.0.0.1:51442       10.0.0.1:8080      ESTABLISHED     4052
	// res : [964:{[{10 135 0 0 0 964}]} 10464:{[{1 51095 22 168177956 1998416475 10464}]} 1748:{[{1 51280 80 168177956 168707456 1748}]}
	// 2964:{[{1 51327 28812 168177956 168707598 2964}]} 4052:{[{8 51440 8080 168177956 168698982 4052} {8 51441 8080 168177956 168698982 4052} {1 51442 8080 168177956 168698982 4052}]}]
	outArr := strings.Split(outStr, "\n")
	for _, line := range outArr {
		var oneSocket SocketInfo
		oneLine := RemoveEmpty(strings.Split(line, " "))
		if len(oneLine) < 5 {
			break
		}
		statStr := strings.TrimSpace(oneLine[3])
		srcStr := strings.TrimSpace(oneLine[1])
		srcIpStr := strings.Split(srcStr, ":")[0]
		srcPortStr := strings.Split(srcStr, ":")[1]
		dstStr := strings.TrimSpace(oneLine[2])
		dstIpStr := strings.Split(dstStr, ":")[0]
		dstPortStr := strings.Split(dstStr, ":")[1]
		pidStr := strings.TrimSpace(oneLine[len(oneLine)-1])

		scrPort, _ := strconv.ParseInt(srcPortStr, 10, 32)
		dstPort, _ := strconv.ParseInt(dstPortStr, 10, 32)
		pid, _ := strconv.ParseInt(pidStr, 10, 64)
		stat := socketStat[statStr]

		oneSocket.Pid = uint64(pid)
		oneSocket.Stat = uint8(stat)
		oneSocket.SrcPort = uint16(scrPort)
		oneSocket.DstPort = uint16(dstPort)
		oneSocket.SrcIp = IpToInt(srcIpStr)
		oneSocket.DstIp = IpToInt(dstIpStr)
		if !filter.Filter(oneSocket) {
			continue
		}
		if _, ok := res[uint64(pid)]; ok {
			var element ElementInfo = res[uint64(pid)]
			element.Element = append(element.Element, oneSocket)
			res[uint64(pid)] = element
		} else {
			var element ElementInfo
			element.Element = append(element.Element, oneSocket)
			res[uint64(pid)] = element
		}
	}
	return res, nil
}

// ip convert to uint32
func IpToInt(ip string) uint32 {
	res := big.NewInt(0)
	res.SetBytes(sysnet.ParseIP(ip).To4())
	return uint32(res.Uint64())
}

// []string delete empty element
func RemoveEmpty(str []string) (res []string) {
	strLen := len(str)
	for i := 0; i < strLen; i++ {
		if len(str[i]) == 0 {
			continue
		} else {
			res = append(res, str[i])
		}
	}
	return
}

// get all udp socket info
func GetAllUdp4Socket(filter SocketFilter) (map[uint64]ElementInfo, error) {
	res := make(map[uint64]ElementInfo)
	// get all udp socket data
	cmdStr := "netstat -ano -p UDP |more +4"
	cmd := exec.Command("cmd", "/c", cmdStr)
	// use a bytes.Buffer to get the output
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Start()

	// use a channel to signal completion
	done := make(chan error)
	go func() { done <- cmd.Wait() }()

	// start a timer
	timeout := time.After(5 * time.Second)

	// select the command operation result
	var outStr string
	select {
	case <-timeout:
		cmd.Process.Kill()
		return res, errors.New("get udp socket info time out")
	case err := <-done:
		if err != nil {
			return res, errors.New("get udp socket info failed")
		}
		outStr = buf.String()
	}

	// format udp socket info
	// outStr : UDP    10.0.0.1:137         *:*                                    4
	// UDP    10.0.0.1:138         *:*                                    4
	// UDP    10.0.0.1:1900        *:*                                    6492
	// UDP    10.0.0.1:5353        *:*                                    5864
	// res : [6492:{[{7 1900 0 2130706433 0 6492}]} 5864:{[{7 5353 0 168177956 0 5864}]} 4:{[{7 137 0 168177956 0 4} {7 138 0 168177956 0 4}]}]
	outArr := strings.Split(outStr, "\n")
	for _, line := range outArr {
		var oneSocket SocketInfo
		oneLine := RemoveEmpty(strings.Split(line, " "))
		if len(oneLine) < 4 {
			break
		}
		srcStr := strings.TrimSpace(oneLine[1])
		srcIpStr := strings.Split(srcStr, ":")[0]
		srcPortStr := strings.Split(srcStr, ":")[1]
		pidStr := strings.TrimSpace(oneLine[len(oneLine)-1])
		scrPort, _ := strconv.ParseInt(srcPortStr, 10, 32)
		pid, _ := strconv.ParseInt(pidStr, 10, 64)

		oneSocket.Stat = 7
		oneSocket.Pid = uint64(pid)
		oneSocket.SrcPort = uint16(scrPort)
		oneSocket.DstPort = 0
		oneSocket.SrcIp = IpToInt(srcIpStr)
		oneSocket.DstIp = 0
		if !filter.Filter(oneSocket) {
			continue
		}
		if _, ok := res[uint64(pid)]; ok {
			var element ElementInfo = res[uint64(pid)]
			element.Element = append(element.Element, oneSocket)
			res[uint64(pid)] = element
		} else {
			var element ElementInfo
			element.Element = append(element.Element, oneSocket)
			res[uint64(pid)] = element
		}
	}
	return res, nil
}

// get current system counts of sockets status
func GetTcp4SocketStatusCount() (SocketStatusCount, error) {
	cmd := "netstat -ano -p TCP |more +4 && netstat -ano -p TCPv6 |more +4"
	out, err := exec.Command("cmd.exe", "/c", cmd).Output()
	var TcpCount SocketStatusCount
	if err != nil {
		logger.Errorf("get Tcp data fail %v", err)
		return TcpCount, err
	}
	TcpCount.Close = uint(strings.Count(string(out), " CLOSE "))
	TcpCount.CloseWait = uint(strings.Count(string(out), " CLOSE_WAIT "))
	TcpCount.Closing = uint(strings.Count(string(out), " CLOSING "))
	TcpCount.Established = uint(strings.Count(string(out), " ESTABLISHED "))
	TcpCount.FinWait1 = uint(strings.Count(string(out), " FIN_WAIT_1 "))
	TcpCount.FinWait2 = uint(strings.Count(string(out), " FIN_WAIT_2 "))
	TcpCount.LastAck = uint(strings.Count(string(out), " LAST_ACK "))
	TcpCount.Listen = uint(strings.Count(string(out), " LISTENING "))
	TcpCount.SyncSent = uint(strings.Count(string(out), " SYN_SENT "))
	TcpCount.SynRecv = uint(strings.Count(string(out), " SYN_RECV "))
	TcpCount.TimeWait = uint(strings.Count(string(out), " TIME_WAIT "))
	return TcpCount, nil
}
