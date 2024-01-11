// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || zos

package collector

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shirou/gopsutil/v3/mem"
)

func TestGetIEGMemInfo(t *testing.T) {
	tempDir := os.TempDir()
	virtMemoryFilePath := filepath.Join(tempDir, "iegDockervmMeminfo")
	f, err := os.Create(virtMemoryFilePath)
	if err != nil {
		t.Errorf("Error creating iegDockervmMeminfo file: %s", err)
	}
	defer f.Close()

	// 创建虚拟内存结构体
	var virtMemoryStat mem.VirtualMemoryStat

	// 写入四行数据
	lines := []string{"MemTotal: 1024", "MemFree: 256", "MemUsed: 512", "MemApp: 256"}
	for _, line := range lines {
		_, err := f.WriteString(line + "\n")
		if err != nil {
			t.Errorf("Error writing to iegDockervmMeminfo file: %s", err)
		}
	}

	// 调用被测函数
	getIEGMemInfo(&virtMemoryStat)

	// 检查虚拟内存结构体是否被正确更新
	if virtMemoryStat.Total != 1024*1024 {
		t.Errorf("Error: Expected Total to be %d, but got %d", 1024*1024, virtMemoryStat.Total)
	}
	if virtMemoryStat.Free != 256*1024 {
		t.Errorf("Error: Expected Free to be %d, but got %d", 256*1024, virtMemoryStat.Free)
	}
	if virtMemoryStat.Available != 256*1024+256*1024 {
		t.Errorf("Error: Expected Available to be %d, but got %d", 256*1024+256*1024, virtMemoryStat.Available)
	}
	if virtMemoryStat.Used != 1024*1024-(256*1024+256*1024) {
		t.Errorf("Error: Expected Used to be %d, but got %d", 1024*1024-(256*1024+256*1024), virtMemoryStat.Used)
	}
	expectedUsedPercent := float64(1024*1024-(256*1024+256*1024)) / float64(1024*1024) * 100.0
	if virtMemoryStat.UsedPercent != expectedUsedPercent {
		t.Errorf("Error: Expected UsedPercent to be %f, but got %f", expectedUsedPercent, virtMemoryStat.UsedPercent)
	}

	// 删除虚拟内存文件
	if err := os.RemoveAll(virtMemoryFilePath); err != nil {
		t.Errorf("Error removing virtMemoryDirPath file: %s", err)
	}
}
