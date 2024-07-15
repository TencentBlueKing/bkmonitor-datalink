// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build linux

package procbin

import (
	"os"
	"syscall"
	"time"

	"github.com/moby/sys/mountinfo"
)

func readStatInfo(pc pidCreated, path string, maxSize int64) *StatInfo {
	info, err := os.Stat(path)
	if err != nil {
		return &StatInfo{
			Path:      path,
			IsDeleted: true,
		}
	}

	var si StatInfo
	sys, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		si.Path = path
		si.Size = info.Size()
		si.Modify = info.ModTime()
	} else {
		si.Path = path
		si.Size = sys.Size
		si.Uid = sys.Uid
		si.Modify = time.Unix(0, sys.Mtim.Nano())
		si.Access = time.Unix(0, sys.Atim.Nano())
		si.Change = time.Unix(0, sys.Ctim.Nano())
	}

	if si.Size > maxSize {
		si.IsLargeBin = true
		return &si
	}

	si.MD5 = hashWithCached(pc, path)
	return &si
}

func readRootMountSource(pid int32) string {
	mounts, err := mountinfo.PidMountInfo(int(pid))
	if err != nil {
		return ""
	}

	for _, mount := range mounts {
		if mount.Mountpoint == "/" {
			return mount.Source
		}
	}
	return ""
}
