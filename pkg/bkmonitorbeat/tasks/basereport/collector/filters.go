// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package collector

type SocketFilter interface {
	// return true when you want the msg
	Filter(socket SocketInfo) bool
}

// NoneSocketFilter will return all sockets
type NoneSocketFilter struct{}

func (f NoneSocketFilter) Filter(socket SocketInfo) bool {
	return true
}

// TcpSocketListenFilter only return listen status tcp sockets
type TcpSocketListenFilter struct{}

func (f TcpSocketListenFilter) Filter(socket SocketInfo) bool {
	if socket.Stat == TCP_LISTEN {
		return true
	} else {
		return false
	}
}

// TcpSocketListenPortFilter filters
type TcpSocketListenPortFilter struct {
	ListenPorts map[uint16]bool
}

func (f TcpSocketListenPortFilter) Filter(socket SocketInfo) bool {
	if socket.Stat == TCP_LISTEN {
		// has socket ?
		_, exist := f.ListenPorts[socket.SrcPort]
		if exist {
			return true
		}
	}
	return false
}

// UdpSocketListenPortFilter filters
type UdpSocketListenPortFilter struct {
	ListenPorts map[uint16]bool
}

func (f UdpSocketListenPortFilter) Filter(socket SocketInfo) bool {
	// udp are stateless
	// has socket ?
	_, exist := f.ListenPorts[socket.SrcPort]
	// 加强异常校验，理论上不应该找到已链接的udp socket
	// 因为udp socket本身无状态，所以用ip和端口为0来判断
	if exist && socket.DstIp == 0 && socket.DstPort == 0 {
		return true
	} else {
		return false
	}
}
