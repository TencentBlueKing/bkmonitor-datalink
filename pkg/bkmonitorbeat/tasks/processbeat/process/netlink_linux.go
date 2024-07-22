// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package process

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/pkg/errors"
	"golang.org/x/sys/unix"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	tcpInit uint8 = iota
	tcpEstablished
	tcpSynSent
	tcpSynRecv
	tcpFinWait1
	tcpFinWait2
	tcpTimeWait
	tcpClose
	tcpCloseWait
	tcpLastAck
	tcpListen
	tcpClosing
	tpcMax
)

const (
	//udpConn UDP state
	udpConn uint8 = iota + 7
)

// tcpStatesMap tcp state map
var tcpStatesMap = map[uint8]string{
	tcpEstablished: "ESTABLISHED",
	tcpSynSent:     "SYN_SENT",
	tcpSynRecv:     "SYN_RECV",
	tcpFinWait1:    "FIN_WAIT1",
	tcpFinWait2:    "FIN_WAIT2",
	tcpTimeWait:    "TIME_WAIT",
	tcpClose:       "CLOSE",
	tcpCloseWait:   "CLOSE_WAIT",
	tcpLastAck:     "LAST_ACK",
	tcpListen:      "LISTEN",
	tcpClosing:     "CLOSING",
	tpcMax:         "tpcMax",
}

// udpStatesMap upd state map
var udpStatesMap = map[uint8]string{
	udpConn: "UNCONN",
}

const (
	sizeOfInetDiagRequest = 72
	sockDiagByFamily      = 20 // sock_diag.h
)

type be16 [2]byte

type be32 [4]byte

// inetDiagSockID sock_diag
/* inet_diag.h
struct inet_diag_sockid {
	__be16  idiag_sport;
	__be16  idiag_dport;
	__be32  idiag_src[4];
	__be32  idiag_dst[4];
	__u32   idiag_if;
	__u32   idiag_cookie[2];
#define INET_DIAG_NOCOOKIE (~0U)
};
*/
type inetDiagSockID struct {
	IdiagSport  be16
	IdiagDport  be16
	IdiagSrc    [4]be32
	IdiagDst    [4]be32
	IdiagIF     uint32
	IdiagCookie [2]uint32
}

// inetDiagReqV2 sock_diag
/* inet_diag.h
struct inet_diag_req_v2 {
        __u8    sdiag_family;
        __u8    sdiag_protocol;
        __u8    idiag_ext;
        __u8    pad;
        __u32   idiag_states;
        struct inet_diag_sockid id;
};
*/
type inetDiagReqV2 struct {
	Family   uint8
	Protocol uint8
	Ext      uint8
	Pad      uint8
	States   uint32
	ID       inetDiagSockID
}

// inetDiagMsg receiv msg
/* inet_diag.h
Base info structure. It contains socket identity (addrs/ports/cookie) and, alas, the information shown by netstat.
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
type inetDiagMsg struct {
	IDiagFamily  uint8
	IDiagState   uint8
	IDiagTimer   uint8
	IDiagRetrans uint8
	ID           inetDiagSockID
	IDiagExpires uint32
	IDiagRqueue  uint32
	IDiagWqueue  uint32
	IDiagUid     uint32
	IDiagInode   uint32
}

// inetDiagRequest count tcp state
/* netlink.h
struct nlmsghdr {
        __u32           nlmsg_len;      // Length of message including header
        __u16           nlmsg_type;     // Message content
        __u16           nlmsg_flags;    // Additional flags
        __u32           nlmsg_seq;      // Sequence number
        __u32           nlmsg_pid;      // Sending process port ID
};

/* netlink protocol
https://tools.ietf.org/html/rfc3549#section-2.3.2
0                   1                   2                   3
0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                          Length                             |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|            Type              |           Flags              |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                      Sequence Number                        |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                      Process ID (PID)                       |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
*/

//inetDiagRequest see https://github.com/sivasankariit/iproute2/blob/1179ab033c31d2c67f406be5bcd5e4c0685855fe/misc/ss.c#L1509-L1512
/* go/src/syscall/ztypes_linux_amd64.go
type NlMsghdr struct {
        Len   uint32
        Type  uint16
        Flags uint16
        Seq   uint32
        Pid   uint32
}
*/
type inetDiagRequest struct {
	Nlh     syscall.NlMsghdr
	ReqDiag inetDiagReqV2
}

var nativeEndian binary.ByteOrder

// getNativeEndian gets native endianness for the system
func getNativeEndian() binary.ByteOrder {
	if nativeEndian == nil {
		var x uint32 = 0x01020304
		if *(*byte)(unsafe.Pointer(&x)) == 0x01 {
			nativeEndian = binary.BigEndian
		} else {
			nativeEndian = binary.LittleEndian
		}
	}
	return nativeEndian
}

// swap16 Byte swap a 16 bit value if we aren't big endian
func swap16(i uint16) uint16 {
	if getNativeEndian() == binary.BigEndian {
		return i
	}
	return (i&0xff00)>>8 | (i&0xff)<<8
}

// Int be16 to int
func (v be16) Int() int {
	v2 := *(*uint16)(unsafe.Pointer(&v))
	return int(swap16(v2))
}

func stateToFlag(num uint8) uint32 {
	if num == 0 {
		return 0xfff //4095
	} else if num > tcpClose {
		return 0
	}
	return 1 << (num)
}

// ipv6 be32 to string
func ipv6(ip [4]be32) string {
	IP := make(net.IP, net.IPv6len)
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			IP[4*i+j] = ip[i][j]
		}
	}
	return IP.String()
}

// ipv4 be32 to string
func ipv4(ip be32) string {
	IP := net.IPv4(ip[0], ip[1], ip[2], ip[3])
	return IP.String()
}

// ipHex2String ip hex to string
func ipHex2String(family uint8, ip [4]be32) (string, error) {
	switch family {
	case unix.AF_INET:
		return ipv4(ip[0]), nil
	case unix.AF_INET6:
		return ipv6(ip), nil
	default:
		return "", errors.New("family is not unix.AF_INET or unix.AF_INET6")
	}
}

// parse be16 to hex
// [32 109] -> 27DB
func (v be16) portHex() string {
	return hex.EncodeToString(v[0:])
}

// sockdiag_send see https://github.com/sivasankariit/iproute2/blob/1179ab033c31d2c67f406be5bcd5e4c0685855fe/misc/ss.c#L1575-L1640
func sockdiagSend(proto, seq, family uint8, exts uint8, states uint32) (skfd int, err error) {
	if skfd, err = unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW, unix.NETLINK_SOCK_DIAG); err != nil {
		return -1, err
	}

	var diagReq inetDiagRequest
	diagReq.Nlh.Type = sockDiagByFamily
	//man 7 netlink: NLM_F_DUMP Convenience macro; equivalent to (NLM_F_ROOT|NLM_F_MATCH).
	diagReq.Nlh.Flags = unix.NLM_F_DUMP | unix.NLM_F_REQUEST
	diagReq.Nlh.Seq = uint32(seq)
	diagReq.Nlh.Pid = 0
	diagReq.ReqDiag.Family = family
	diagReq.ReqDiag.Protocol = proto
	diagReq.ReqDiag.Ext = exts
	diagReq.ReqDiag.States = states
	diagReq.Nlh.Len = uint32(unsafe.Sizeof(diagReq))

	var inDiagRequestBuffer []byte

	inDiagRequestBuffer = make([]byte, sizeOfInetDiagRequest)
	*(*inetDiagRequest)(unsafe.Pointer(&inDiagRequestBuffer[0])) = diagReq

	sockAddrNl := unix.SockaddrNetlink{Family: syscall.AF_NETLINK}
	timeout := syscall.NsecToTimeval((200 * time.Millisecond).Nanoseconds())
	if err = syscall.SetsockoptTimeval(skfd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &timeout); err != nil {
		return 0, err
	}

	if err = unix.Sendmsg(skfd, inDiagRequestBuffer, nil, &sockAddrNl, 0); err != nil {
		return -1, err
	}
	return skfd, nil
}

// sockdiagRecv inode -> FileSocket
func sockdiagRecv(skfd int, proto uint8) (map[uint32]FileSocket, error) {
	var (
		err         error
		stateMap    = make(map[uint8]string)
		filesockets = make(map[uint32]FileSocket)
		buf         = make([]byte, os.Getpagesize())
	)

	switch proto {
	case syscall.IPPROTO_UDP:
		stateMap = udpStatesMap
	case syscall.IPPROTO_TCP:
		stateMap = tcpStatesMap
	}

	var (
		n int
	)

	// loop here, it will ensure that all messages have been read from the kernel
loop:
	for {
		for {
			if n, _, _, _, err = unix.Recvmsg(skfd, buf, nil, unix.MSG_PEEK); err != nil {
				return nil, errors.Wrap(err, "unix.Recvmsg")
			}
			if n < len(buf) {
				break
			}

			// double if not enough bytes
			buf = make([]byte, 2*len(buf))
		}

		if n, _, _, _, err = unix.Recvmsg(skfd, buf, nil, 0); err != nil {
			return nil, errors.Wrap(err, "unix.Recvmsg")
		}

		// no messages anymore
		if n == 0 {
			logger.Debugf("recvmsg done, fd=%v, proto=%x", skfd, proto)
			break loop
		}

		msgs, err := syscall.ParseNetlinkMessage(buf[:n])
		if err != nil {
			return nil, errors.Wrap(err, "syscall.ParseNetlinkMessage")
		}

		for idx, netlinkMessage := range msgs {
			if netlinkMessage.Header.Type == syscall.NLMSG_DONE {
				logger.Debugf("got done message from header, msg index=%d", idx)
				break loop
			}

			data := netlinkMessage.Data
			m := (*inetDiagMsg)(unsafe.Pointer(&data[0]))
			srcIPString, _ := ipHex2String(m.IDiagFamily, m.ID.IdiagSrc)
			dstIPString, _ := ipHex2String(m.IDiagFamily, m.ID.IdiagDst)
			filesocket := FileSocket{
				Status: stateMap[m.IDiagState],
				Inode:  m.IDiagInode,
				Family: uint32(m.IDiagFamily),
				Saddr:  srcIPString,
				Sport:  uint32(m.ID.IdiagSport.Int()),
				Daddr:  dstIPString,
				Dport:  uint32(m.ID.IdiagDport.Int()),
			}

			switch proto {
			case syscall.IPPROTO_UDP:
				filesocket.Type = syscall.SOCK_DGRAM
			case syscall.IPPROTO_TCP:
				filesocket.Type = syscall.SOCK_STREAM
			}

			filesockets[filesocket.Inode] = filesocket
		}
	}

	return filesockets, nil
}

// getProcInodes returns inodes of the specified pid
func getProcInodes(root string, pid int32) ([]uint64, error) {
	var inodefds []uint64
	dir := fmt.Sprintf("%s/%d/fd", root, pid)
	f, err := os.Open(dir)
	if err != nil {
		return inodefds, err
	}
	defer f.Close()
	files, err := f.Readdir(0)
	if err != nil {
		return inodefds, err
	}

	for _, fd := range files {
		inodePath := fmt.Sprintf("%s/%d/fd/%s", root, pid, fd.Name())
		inode, err := os.Readlink(inodePath)
		if err != nil {
			continue
		}
		// socket:[1070205860]
		if !strings.HasPrefix(inode, "socket:[") {
			continue
		}

		inodeInt, err := strconv.Atoi(inode[8 : len(inode)-1])
		if err != nil {
			continue
		}

		inodefds = append(inodefds, uint64(inodeInt))
	}
	return inodefds, nil
}

// getConcernPidInodes pid -> inodes
func getConcernPidInodes(pids []int32) map[int32][]uint64 {
	ret := make(map[int32][]uint64)
	for idx, pid := range pids {
		// resource limited
		if (idx+1)%socketPerformanceThreshold == 0 {
			time.Sleep(time.Millisecond * socketPerformanceSleep)
		}

		inodes, err := getProcInodes("/proc", pid)
		if err != nil {
			logger.Errorf("failed to get /proc info: %v", err)
			continue
		}
		ret[pid] = inodes
	}

	return ret
}

// getIntersection inode -> sockets
func getIntersection(conn map[uint32]FileSocket, inodes map[int32][]uint64, tcp bool, status []string) map[uint32]map[FileSocket]struct{} {
	matchStatus := func(s string, status []string) bool {
		for i := 0; i < len(status); i++ {
			if status[i] == s {
				return true
			}
		}
		return false
	}

	ret := make(map[uint32]map[FileSocket]struct{})
	for pid, items := range inodes {
		for _, inode := range items {
			if v, ok := conn[uint32(inode)]; ok {
				if tcp && !matchStatus(v.Status, status) {
					continue
				}
				v.Pid = pid

				if _, exist := ret[uint32(inode)]; !exist {
					ret[uint32(inode)] = map[FileSocket]struct{}{}
				}
				ret[uint32(inode)][v] = struct{}{}
			}
		}
	}

	return ret
}

// getPidSockets fills pid field with socket
func getPidSockets(sni socketNetInfo, pids []int32, status []string) PidSockets {
	inodes := getConcernPidInodes(pids) // pid -> inodes

	netTcp := getIntersection(sni.TCP, inodes, true, status)
	netUdp := getIntersection(sni.UDP, inodes, false, status)
	netTcp6 := getIntersection(sni.TCP6, inodes, true, status)
	netUdp6 := getIntersection(sni.UDP6, inodes, false, status)

	ret := PidSockets{
		TCP:  map[int32][]FileSocket{},
		UDP:  map[int32][]FileSocket{},
		TCP6: map[int32][]FileSocket{},
		UDP6: map[int32][]FileSocket{},
	}

	cloneFileSocket := func(fs FileSocket, protocol, ip string) FileSocket {
		cloned := fs
		cloned.Protocol = protocol
		cloned.Saddr = ip
		return cloned
	}

	for _, sockets := range netTcp {
		for v := range sockets {
			for _, listenIP := range tasks.GetListeningIPs(v.Saddr) {
				ret.TCP[v.Pid] = append(ret.TCP[v.Pid], cloneFileSocket(v, ProtocolTCP, listenIP))
			}
		}
	}

	for _, sockets := range netUdp {
		for v := range sockets {
			for _, listenIP := range tasks.GetListeningIPs(v.Saddr) {
				ret.UDP[v.Pid] = append(ret.UDP[v.Pid], cloneFileSocket(v, ProtocolUDP, listenIP))
			}
		}
	}

	for _, sockets := range netTcp6 {
		for v := range sockets {
			for _, listenIP := range tasks.GetListeningIPs(v.Saddr) {
				ret.TCP6[v.Pid] = append(ret.TCP6[v.Pid], cloneFileSocket(v, ProtocolTCP6, listenIP))
			}
		}
	}

	for _, sockets := range netUdp6 {
		for v := range sockets {
			for _, listenIP := range tasks.GetListeningIPs(v.Saddr) {
				ret.UDP6[v.Pid] = append(ret.UDP6[v.Pid], cloneFileSocket(v, ProtocolUDP6, listenIP))
			}
		}
	}

	return ret
}

type NetlinkDetector struct{}

var _ ConnDetector = NetlinkDetector{}

func (d NetlinkDetector) Type() string {
	return DetectorNetlink
}

func (d NetlinkDetector) Get(pids []int32) (PidSockets, error) {
	return d.GetState(pids, StateListen)
}

func (d NetlinkDetector) GetState(pids []int32, state StateType) (PidSockets, error) {
	type Task struct {
		proto      uint8
		family     uint8
		states     uint32
		fileSocket map[uint32]FileSocket
	}

	const (
		IPv4TCPIndex = iota
		IPv6TCPIndex
		IPv4UDPIndex
		IPv6UDPIndex
	)

	var status []string
	var tcpState uint32
	switch state {
	case StateListenEstab:
		tcpState = 1<<tcpInit | 1<<tcpListen | 1<<tcpEstablished
		status = []string{"LISTEN", "ESTABLISHED"}
	default:
		tcpState = 1<<tcpInit | 1<<tcpListen // 默认为 listen
		status = []string{"LISTEN"}
	}

	tasks := []*Task{
		{
			proto:  syscall.IPPROTO_TCP, // IPv4TCP
			family: syscall.AF_INET,
			states: tcpState,
		},
		{
			proto:  syscall.IPPROTO_TCP, // IPv6TCP
			family: syscall.AF_INET6,
			states: tcpState,
		},
		{
			proto:  syscall.IPPROTO_UDP, // IPv4UDP
			family: syscall.AF_INET,
			states: stateToFlag(udpConn),
		},
		{
			proto:  syscall.IPPROTO_UDP, // IPv6UDP
			family: syscall.AF_INET6,
			states: stateToFlag(udpConn),
		},
	}

	var (
		errs []error
		cni  = socketNetInfo{}
	)

	for _, t := range tasks {
		taskFd, err := sockdiagSend(t.proto, 0, t.family, 0, t.states)
		if err != nil {
			errs = append(errs, errors.Wrap(err, "sockdiag send failed"))
			continue
		}

		defer func(fd int) {
			_ = syscall.Close(fd)
		}(taskFd)

		if t.fileSocket, err = sockdiagRecv(taskFd, t.proto); err != nil {
			errs = append(errs, errors.Wrap(err, "sockdiag recv failed"))
			continue
		}
	}

	// 任何错误都将当做 netlink 失败
	if len(errs) > 0 {
		return PidSockets{}, errs[0]
	}

	cni.TCP = tasks[IPv4TCPIndex].fileSocket
	cni.TCP6 = tasks[IPv6TCPIndex].fileSocket
	cni.UDP = tasks[IPv4UDPIndex].fileSocket
	cni.UDP6 = tasks[IPv6UDPIndex].fileSocket

	logger.Debugf("netlink get sockets: %+v, pids=%+v", cni, pids)
	return getPidSockets(cni, pids, status), nil
}
