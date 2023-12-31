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

	"github.com/stretchr/testify/assert"
)

func TestGetDiskSpeed(t *testing.T) {
	last := make(map[string]DiskStats)
	current := make(map[string]DiskStats)
	last["disk1"] = DiskStats{
		ReadCount:    10,
		ReadBytes:    1000,
		WriteCount:   20,
		WriteBytes:   2000,
		IoTime:       100,
		ReadTime:     50,
		WriteTime:    50,
		ReadSectors:  500,
		WriteSectors: 1000,
		WeightedIO:   2000,
	}

	last["disk2"] = DiskStats{
		ReadCount:    5,
		ReadBytes:    500,
		WriteCount:   15,
		WriteBytes:   1500,
		IoTime:       75,
		ReadTime:     30,
		WriteTime:    45,
		ReadSectors:  300,
		WriteSectors: 750,
		WeightedIO:   1000,
	}

	current["disk1"] = DiskStats{
		ReadCount:    15,
		ReadBytes:    1500,
		WriteCount:   30,
		WriteBytes:   3000,
		IoTime:       150,
		ReadTime:     75,
		WriteTime:    75,
		ReadSectors:  750,
		WriteSectors: 1500,
		WeightedIO:   3000,
	}

	current["disk2"] = DiskStats{
		ReadCount:    7,
		ReadBytes:    700,
		WriteCount:   17,
		WriteBytes:   1700,
		IoTime:       90,
		ReadTime:     35,
		WriteTime:    55,
		ReadSectors:  350,
		WriteSectors: 850,
		WeightedIO:   1400,
	}

	GetDiskSpeed(last, current)
	assert.True(t, reflect.DeepEqual(DiskStats{
		ReadCount:      15,
		ReadBytes:      1500,
		WriteCount:     30,
		WriteBytes:     3000,
		IoTime:         150,
		ReadTime:       75,
		WriteTime:      75,
		ReadSectors:    750,
		WriteSectors:   1500,
		WeightedIO:     3000,
		SpeedIORead:    5.0,
		SpeedByteRead:  500.0,
		SpeedIOWrite:   10.0,
		SpeedByteWrite: 1000.0,
		Svctm:          5.0,
		Await:          1.25,
		AvgrqSz:        62.5,
		AvgquSz:        0.08333333333333333,
		Util:           0.15,
	}, current["disk1"]))

	assert.True(t, reflect.DeepEqual(DiskStats{
		ReadCount:      7,
		ReadBytes:      700,
		WriteCount:     17,
		WriteBytes:     1700,
		IoTime:         90,
		ReadTime:       35,
		WriteTime:      55,
		ReadSectors:    350,
		WriteSectors:   850,
		WeightedIO:     1400,
		SpeedIORead:    2.0,
		SpeedByteRead:  200.0,
		SpeedIOWrite:   20.0,
		SpeedByteWrite: 200.0,
		Svctm:          7.5,
		Await:          1.2857142857142858,
		AvgrqSz:        85.71428571428571,
		AvgquSz:        0.016666666666666666,
		Util:           0.15,
	}, current["disk2"]))
}
