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
	"github.com/shirou/gopsutil/v3/net"
)

type SocketInfo struct {
	BaseSocketInfo
	Inode uint64
	Type  uint32 // syscall.SOCK_STREAM or syscall.SOCK_DGR
}

// GetTcp4SocketStatusCount get sockets status
func GetTcp4SocketStatusCount() (SocketStatusCount, error) {
	count := SocketStatusCount{}
	connections, err := net.Connections("tcp4")
	if err != nil {
		return count, err
	}
	for _, connection := range connections {
		switch connection.Status {
		case "ESTABLISHED":
			count.Established++
		case "SYN_SENT":
			count.SyncSent++
		case "SYN_RECV":
			count.SynRecv++
		case "FIN_WAIT1":
			count.FinWait1++
		case "FIN_WAIT2":
			count.FinWait2++
		case "TIME_WAIT":
			count.TimeWait++
		case "CLOSE":
			count.Close++
		case "CLOSE_WAIT":
			count.CloseWait++
		case "LAST_ACK":
			count.LastAck++
		case "LISTEN":
			count.Listen++
		case "CLOSING":
			count.Closing++
		}
	}
	return count, nil
}
