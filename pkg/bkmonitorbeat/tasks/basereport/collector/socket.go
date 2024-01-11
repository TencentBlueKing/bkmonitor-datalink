// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package collector

type BaseSocketInfo struct {
	Stat    uint8
	SrcPort uint16
	DstPort uint16
	SrcIp   uint32
	DstIp   uint32
}

type SocketInfo struct {
	BaseSocketInfo
	Pid  uint64
	Type uint32 // syscall.SOCK_STREAM or syscall.SOCK_DGR
}

type SocketStatusCount struct {
	Established uint `json:"established"`
	SyncSent    uint `json:"syncSent"`
	SynRecv     uint `json:"synRecv"`
	FinWait1    uint `json:"finWait1"`
	FinWait2    uint `json:"finWait2"`
	TimeWait    uint `json:"timeWait"`
	Close       uint `json:"close"`
	CloseWait   uint `json:"closeWait"`
	LastAck     uint `json:"lastAck"`
	Listen      uint `json:"listen"`
	Closing     uint `json:"closing"`
}
