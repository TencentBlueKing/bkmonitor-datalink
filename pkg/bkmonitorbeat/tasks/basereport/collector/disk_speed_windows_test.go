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
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIOCountersWithContextIgnoreDriveError(t *testing.T) {
	drivemap, err := iOCountersWithContextIgnoreDriveError(context.Background(), "C")
	assert.NoError(t, err)

	if len(drivemap) <= 0 {
		t.Errorf("Unexpected length of drivemap.Expected 1, got %d ", len(drivemap))
	}
}

func TestIOSpeed(t *testing.T) {
	expected := map[string]BKDiskStats{}

	res, err := IOSpeed()
	if err != nil {
		t.Fatalf("unexpected error:  %v", err)
	}

	if len(res) != len(expected) {
		t.Fatalf("unexpected result length: expected %d, got %d", len(expected), len(res))
	}

	for k, v := range expected {
		r, ok := res[k]
		if !ok {
			t.Fatalf("result does not contain key %q", k)
		}

		if r.SpeedIORead != v.SpeedIORead {
			t.Errorf("result[%q].SpeedIORead = %f, expected %f", k, r.SpeedIORead, v.SpeedIORead)
		}
	}
}

func TestGetDiskSpeedWin(t *testing.T) {
	last := make(map[string]BKDiskStats)
	current := make(map[string]BKDiskStats)
	last["disk1"] = BKDiskStats{
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

	last["disk2"] = BKDiskStats{
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

	//  set  new  values  in  current  map
	current["disk1"] = BKDiskStats{
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

	current["disk2"] = BKDiskStats{
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
	assert.True(t, reflect.DeepEqual(BKDiskStats{
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

	assert.True(t, reflect.DeepEqual(BKDiskStats{
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
