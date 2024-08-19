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
	"reflect"
	"testing"

	"github.com/shirou/gopsutil/v3/disk"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
)

func TestFilterDiskIoStats(t *testing.T) {
	diskStats := map[string]DiskStats{
		"/dev/sda": {
			ReadCount:  100,
			WriteCount: 200,
			ReadBytes:  300,
			WriteBytes: 400,
		},
		"/dev/sdb": {
			ReadCount:  500,
			WriteCount: 600,
			ReadBytes:  700,
			WriteBytes: 800,
		},
	}

	config := configs.DiskConfig{
		FSTypeWhiteListPattern: []string{"ext4"},
		FSTypeBlackListPattern: []string{"ntfs"},
		PartitionWhiteListPattern: []string{
			"/dev/sda",
			"/dev/sdb",
		},
		PartitionBlackListPattern: []string{"/dev/sdb"},
	}

	resultDiskStats := FilterDiskIoStats(diskStats, config)

	expectedDiskStats := map[string]DiskStats{
		"/dev/sda": {
			ReadCount:  100,
			WriteCount: 200,
			ReadBytes:  300,
			WriteBytes: 400,
		},
	}

	assert.True(t, reflect.DeepEqual(resultDiskStats, expectedDiskStats), "Expected  %v,  but  got  %v", expectedDiskStats, resultDiskStats)
}

func TestFilterPartitions(t *testing.T) {
	partitionStats := []disk.PartitionStat{
		{
			Device:     "/dev/sda1",
			Mountpoint: "/",
			Fstype:     "ext4",
		},
		{
			Device:     "/dev/sda2",
			Mountpoint: "/data",
			Fstype:     "ext4",
		},
	}
	config := configs.DiskConfig{
		PartitionWhiteListPattern: []string{"/dev/sda1", "/dev/sda2"},
		PartitionBlackListPattern: []string{"/dev/sda3"},
		FSTypeWhiteListPattern:    []string{"ext4"},
		FSTypeBlackListPattern:    []string{"tmpfs"},
	}

	expected := []disk.PartitionStat{
		{
			Device:     "/dev/sda1",
			Mountpoint: "/",
			Fstype:     "ext4",
		},
		{
			Device:     "/dev/sda2",
			Mountpoint: "/data",
			Fstype:     "ext4",
		},
	}

	resultPartitionStats := FilterPartitions(partitionStats, config)
	assert.Equal(t, len(resultPartitionStats), len(expected))

	for i := 0; i < len(resultPartitionStats); i++ {
		assert.Equal(t, resultPartitionStats[i], expected[i])
	}
}
