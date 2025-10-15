// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define

import (
	"time"
)

type ProcStat struct {
	Pid      int32
	PPid     int32
	Name     string
	Cwd      string
	Exe      string
	Cmd      string
	CmdSlice []string
	Status   string
	Username string
	Created  int64
	Mem      *MemStat
	CPU      *CPUStat
	IO       *IOStat
	Fd       *FdStat
}

type IOStat struct {
	Ts         time.Time
	ReadBytes  uint64
	WriteBytes uint64
	ReadSpeed  float64
	WriteSpeed float64
}

type ProcTime struct {
	StartTime uint64
	User      uint64
	Sys       uint64
	Total     uint64
}

type CPUStat struct {
	Ts            time.Time
	StartTime     uint64
	User          uint64
	Sys           uint64
	Total         uint64
	Percent       float64
	NormalPercent float64
}

type MemStat struct {
	Size     uint64
	Resident uint64
	Share    uint64
	Percent  float64
}

type FdStat struct {
	Open      uint64
	SoftLimit uint64
	HardLimit uint64
}
