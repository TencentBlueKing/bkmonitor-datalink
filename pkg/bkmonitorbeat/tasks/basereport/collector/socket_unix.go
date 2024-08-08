// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build aix || darwin || dragonfly || linux || netbsd || openbsd || solaris || zos
// +build aix darwin dragonfly linux netbsd openbsd solaris zos

package collector

import (
	"unsafe"

	"github.com/mdlayher/netlink"
	"golang.org/x/sys/unix"
)

type FileSocketItem struct {
	Laddr  string
	Raddr  string
	Status string
	Inode  string
	Pid    int32
	Fd     uint32
}

type SocketInfo struct {
	BaseSocketInfo
	Inode uint64
	Type  uint32 // syscall.SOCK_STREAM or syscall.SOCK_DGR
}

// GetTcp4SocketStatusCountByNetlink get sockets status by netlink
func GetTcp4SocketStatusCountByNetlink() (SocketStatusCount, error) {
	c, err := netlink.Dial(unix.NETLINK_INET_DIAG, nil)
	if err != nil {
		return SocketStatusCount{}, err
	}
	defer c.Close()

	r := InetDiagReq{
		Family: unix.AF_INET,
		States: 1<<TCP_MAX_STATES - 1, // all status
	}

	req := netlink.Message{
		Header: netlink.Header{
			Flags: netlink.Root | netlink.Match | netlink.Request | netlink.Acknowledge,
			Type:  TCPDIAG_GETSOCK,
		},
		Data: r.MarshalBinary(),
	}

	// Perform a request, receive replies, and validate the replies
	msgs, err := c.Execute(req)
	if err != nil {
		return SocketStatusCount{}, err
	}

	rawcount := make([]uint, TCP_MAX_STATES)
	for _, m := range msgs {
		req := *(**InetDiagMsg)(unsafe.Pointer(&m.Data))
		rawcount[int(req.State)]++
	}

	// transfer to SocketStatusCount
	var count SocketStatusCount
	count.Established = rawcount[TCP_ESTABLISHED]
	count.SyncSent = rawcount[TCP_SYN_SENT]
	count.SynRecv = rawcount[TCP_SYN_RECV]
	count.FinWait1 = rawcount[TCP_FIN_WAIT1]
	count.FinWait2 = rawcount[TCP_FIN_WAIT2]
	count.TimeWait = rawcount[TCP_TIME_WAIT]
	count.Close = rawcount[TCP_CLOSE]
	count.CloseWait = rawcount[TCP_CLOSE_WAIT]
	count.LastAck = rawcount[TCP_LAST_ACK]
	count.Listen = rawcount[TCP_LISTEN]
	count.Closing = rawcount[TCP_CLOSING]
	return count, nil
}

// GetTcp4SocketStatusCount get sockets status
func GetTcp4SocketStatusCount() (SocketStatusCount, error) {
	return GetTcp4SocketStatusCountByNetlink()
}
