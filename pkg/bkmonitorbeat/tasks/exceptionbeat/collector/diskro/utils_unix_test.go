// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package diskro

import (
	"os"
	"testing"

	"github.com/shirou/gopsutil/v3/disk"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/storage"
)

var testDiskInfo = disk.PartitionStat{
	Device:     "/dev/sda",
	Mountpoint: "/data",
	Fstype:     "ext4",
	Opts:       []string{"rw"},
}

func initStorage() string {
	f, err := os.CreateTemp("", "")
	if err != nil {
		panic(err)
	}
	err = storage.Init(f.Name(), nil)
	if err != nil {
		panic(err)
	}
	return f.Name()
}

func cleanStorage(t *testing.T, mp *MountPointInfo) {
	assert.Nil(t, storage.Del(mp.genUniqueKey()), "clean data")
}

func TestMain(m *testing.M) {
	// 初始化动作
	// utils.Setup()

	code := m.Run()

	// 清理动作
	// utils.Teardown()
	os.Exit(code)
}

func TestGenUniqueKey(t *testing.T) {
	mpList := NewBatchMountPointInfo([]disk.PartitionStat{testDiskInfo})

	assert.Equal(t, 1, len(mpList), "test batch mp create count")

	uniqueKey := mpList[0].genUniqueKey()
	assert.Equal(t, "/dev/sda-ext4-/data", uniqueKey, "test mount point unique")
}

func TestSetHistoryInfoNotSaveRO(t *testing.T) {
	f := initStorage()
	defer os.Remove(f)
	mp := NewBatchMountPointInfo([]disk.PartitionStat{testDiskInfo})[0]
	mp.Options = append(mp.Options, "ro")

	cleanStorage(t, mp)

	err := mp.SaveStatus()
	assert.Equal(t, nil, err, "test save must success with no error")

	history := mp.getHistoryInfo()
	assert.Nil(t, history, "test ro info must not load from db")
	cleanStorage(t, mp)
}

func TestSetHistoryInfoSave(t *testing.T) {
	mp := NewBatchMountPointInfo([]disk.PartitionStat{testDiskInfo})[0]

	err := mp.SaveStatus()
	assert.Equal(t, nil, err, "test save must success with no error")

	history := mp.getHistoryInfo()
	assert.NotNil(t, history, "test normal info must load from db")

	assert.Equal(t, mp.Device, history.Device, "test restore data as old -- device")
	assert.Equal(t, mp.FileSystem, history.FileSystem, "test restore data as old -- fs")
	assert.Equal(t, mp.Options, history.Options, "test restore data as old -- option")
	assert.Equal(t, mp.MountPoint, history.MountPoint, "test restore data as old -- mount point")
	cleanStorage(t, mp)
}

func TestIsReadOnlyStatusChange(t *testing.T) {
	mp := NewBatchMountPointInfo([]disk.PartitionStat{testDiskInfo})[0]

	assert.False(t, mp.IsReadOnlyStatusChange(), "test init status, no status is detect.")
	err := mp.SaveStatus()
	assert.Nil(t, err, "test init data save must success")

	newMp := NewBatchMountPointInfo([]disk.PartitionStat{
		{
			Device:     testDiskInfo.Device,
			Mountpoint: testDiskInfo.Mountpoint,
			Fstype:     testDiskInfo.Fstype,
			Opts:       []string{"ro"},
		},
	})[0]
	assert.True(t, newMp.IsReadOnlyStatusChange(), "test detect ro status change")
	cleanStorage(t, mp)
}

func TestInitReadOnlyStatusNoChange(t *testing.T) {
	newMp := NewBatchMountPointInfo([]disk.PartitionStat{
		{
			Device:     testDiskInfo.Device,
			Mountpoint: testDiskInfo.Mountpoint,
			Fstype:     testDiskInfo.Fstype,
			Opts:       []string{"ro"},
		},
	})[0]
	assert.Nil(t, newMp.SaveStatus(), "save readonly init status success")

	mp := NewBatchMountPointInfo([]disk.PartitionStat{testDiskInfo})[0]
	assert.False(t, mp.IsReadOnlyStatusChange(), "test init status, no status is detect.")
	assert.Nil(t, mp.SaveStatus(), "test init data save must success")

	assert.False(t, mp.IsReadOnlyStatusChange(), "not detect ro status change")
	cleanStorage(t, mp)
}

func TestInitReadOnlyStatusNoChangeV2(t *testing.T) {
	newMp := NewBatchMountPointInfo([]disk.PartitionStat{
		{
			Device:     testDiskInfo.Device,
			Mountpoint: testDiskInfo.Mountpoint,
			Fstype:     testDiskInfo.Fstype,
			Opts:       []string{"ro"},
		},
	})[0]
	assert.Nil(t, newMp.SaveStatus(), "save readonly init status success")

	mp := NewBatchMountPointInfo([]disk.PartitionStat{
		{
			Device:     testDiskInfo.Device,
			Mountpoint: testDiskInfo.Mountpoint,
			Fstype:     testDiskInfo.Fstype,
			Opts:       []string{"ro"},
		},
	})[0]
	assert.False(t, mp.IsReadOnlyStatusChange(), "not detect ro status change")
	cleanStorage(t, mp)
}

func TestRuleMatch(t *testing.T) {
	testData := []struct {
		filePath string
		ruleList []string
		result   bool
	}{
		{
			"/data/file/path",
			[]string{"file"},
			true,
		},
		{
			"/data/file/path",
			[]string{"haha"},
			false,
		},
		{
			"/data/file/path",
			[]string{"f"},
			true,
		},
	}

	for _, data := range testData {
		mp := NewMountPointInfo(disk.PartitionStat{
			Mountpoint: data.filePath,
		})

		assert.Equal(t, data.result, mp.IsMatchRule(data.ruleList), "test mount point match rule")
	}
}
