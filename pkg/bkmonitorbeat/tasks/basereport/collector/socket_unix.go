// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build aix || dragonfly || linux || netbsd || openbsd || solaris || zos

package collector

import (
	"unsafe"

	"github.com/mdlayher/netlink"
	"github.com/mdlayher/netlink/nlenc"
	"golang.org/x/sys/unix"
)

// tcp status
// reference: source/include/net/tcp_states.h
/*
 enum {
	TCP_ESTABLISHED = 1,
	TCP_SYN_SENT,
	TCP_SYN_RECV,
	TCP_FIN_WAIT1,
	TCP_FIN_WAIT2,
	TCP_TIME_WAIT,
	TCP_CLOSE,
	TCP_CLOSE_WAIT,
	TCP_LAST_ACK,
	TCP_LISTEN,
	TCP_CLOSING,	* Now a valid state *
	TCP_NEW_SYN_RECV,   // kernel > 4.1

	TCP_MAX_STATES	* Leave at the end! *
};
*/
const (
	TCP_ESTABLISHED = iota + 1
	TCP_SYN_SENT
	TCP_SYN_RECV
	TCP_FIN_WAIT1
	TCP_FIN_WAIT2
	TCP_TIME_WAIT
	TCP_CLOSE
	TCP_CLOSE_WAIT
	TCP_LAST_ACK
	TCP_LISTEN  // 0x0A
	TCP_CLOSING // now a valid state
	TCP_MAX_STATES
)

/*
	Socket identity

	struct inet_diag_sockid {
	    __be16  idiag_sport;
	    __be16  idiag_dport;
	    __be32  idiag_src[4];
	    __be32  idiag_dst[4];
	    __u32   idiag_if;
	    __u32   idiag_cookie[2];

#define INET_DIAG_NOCOOKIE (~0U)
};

/* Request structure

	struct inet_diag_req {
	    __u8    idiag_family;       /* Family of addresses.
	    __u8    idiag_src_len;
	    __u8    idiag_dst_len;
	    __u8    idiag_ext;      /* Query extended information

	    struct inet_diag_sockid id;

	    __u32   idiag_states;
	    __u32   idiag_dbs;
	};

	struct inet_diag_msg {
	    __u8    idiag_family;
	    __u8    idiag_state;
	    __u8    idiag_timer;
	    __u8    idiag_retrans;

	    struct inet_diag_sockid id;

	    __u32   idiag_expires;
	    __u32   idiag_rqueue;
	    __u32   idiag_wqueue;
	    __u32   idiag_uid;
	    __u32   idiag_inode;
	};
*/
const TCPDIAG_GETSOCK = 18

// #define NLMSG_ALIGNTO   4U
const nlmsgAlignTo = 4

// #define NLMSG_ALIGN(len) ( ((len)+NLMSG_ALIGNTO-1) & ~(NLMSG_ALIGNTO-1) )
func nlmsgAlign(len int) int {
	return ((len) + nlmsgAlignTo - 1) & ^(nlmsgAlignTo - 1)
}

type InetDiagReq struct {
	Family uint8
	SrcLen uint8
	DstLen uint8
	Ext    uint8

	Sport  uint16
	Dport  uint16
	Src    [4]uint32
	Dst    [4]uint32
	If     uint32
	Cookie [2]uint32

	States uint32
	Dbs    uint32
}

func (m InetDiagReq) MarshalBinary() []byte {
	ml := nlmsgAlign(int(unsafe.Sizeof(m)))
	b := make([]byte, ml)
	nlenc.PutUint8(b[0:1], m.Family)
	nlenc.PutUint32(b[52:56], m.States)
	return b
}

type InetDiagMsg struct {
	Family  uint8 // 0,1
	State   uint8 // 1,2
	Timer   uint8 // 2,3
	Retrans uint8 // 3,4

	Sport  uint16 // 4,6
	Dport  uint16 // 6,8
	Src    [4]uint32
	Dst    [4]uint32
	If     uint32
	Cookie [2]uint32

	Expires uint32
	Rqueue  uint32
	Wqueue  uint32
	Uid     uint32
	Inode   uint32
}

func GetTcp4SocketStatusCount() (SocketStatusCount, error) {
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

	counts := make([]uint, TCP_MAX_STATES)
	for _, m := range msgs {
		req := *(**InetDiagMsg)(unsafe.Pointer(&m.Data))
		counts[int(req.State)]++
	}

	// transfer to SocketStatusCount
	var count SocketStatusCount
	count.Established = counts[TCP_ESTABLISHED]
	count.SyncSent = counts[TCP_SYN_SENT]
	count.SynRecv = counts[TCP_SYN_RECV]
	count.FinWait1 = counts[TCP_FIN_WAIT1]
	count.FinWait2 = counts[TCP_FIN_WAIT2]
	count.TimeWait = counts[TCP_TIME_WAIT]
	count.Close = counts[TCP_CLOSE]
	count.CloseWait = counts[TCP_CLOSE_WAIT]
	count.LastAck = counts[TCP_LAST_ACK]
	count.Listen = counts[TCP_LISTEN]
	count.Closing = counts[TCP_CLOSING]
	return count, nil
}
