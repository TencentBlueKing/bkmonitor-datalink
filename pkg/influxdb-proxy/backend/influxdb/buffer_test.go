// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb_test

import (
	"context"
	"io/ioutil"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/backend"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/backend/influxdb"
)

// 测试周期上报
func TestBufferTimeout(t *testing.T) {
	rd := backend.NewPointsReader([]byte("012345678"), 2)
	rd.AppendIndex(0, 2)  // 0123
	rd.AppendIndex(2, 4)  // 23
	rd.AppendIndex(4, 6)  // 45
	rd.AppendIndex(6, 10) // 678

	rd2 := backend.NewPointsReader([]byte("876543210"), 2)
	rd2.AppendIndex(0, 2)  // 8765
	rd2.AppendIndex(2, 4)  // 23
	rd2.AppendIndex(4, 6)  // 45
	rd2.AppendIndex(6, 10) // 678
	notify := make(chan string)
	buffer := influxdb.NewBuffer(context.Background(), "basic", nil, nil, 10*time.Second, notify)
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-notify
		data, err := ioutil.ReadAll(buffer)
		if err != nil {
			t.Error(err)
			return
		}
		if string(data) != "012345678876543210" {
			t.Error("wrong result")
		}
	}()
	buffer.AddReader(1, rd)
	buffer.AddReader(2, rd2)
	wg.Wait()
}

// 测试达到阈值上报场景
func TestBufferMax(t *testing.T) {
	// 第一个长度没有超过阈值
	rd0 := backend.NewPointsReader([]byte("012345678"), 2)
	rd0.AppendIndex(0, 2) // 01

	// 第二个超过了
	rd := backend.NewPointsReader([]byte("012345678"), 2)
	rd.AppendIndex(0, 2)  // 0123
	rd.AppendIndex(2, 4)  // 23
	rd.AppendIndex(4, 6)  // 45
	rd.AppendIndex(6, 10) // 678

	// 第三个也超过了
	rd2 := backend.NewPointsReader([]byte("876543210"), 2)
	rd2.AppendIndex(0, 2)  // 8765
	rd2.AppendIndex(2, 4)  // 23
	rd2.AppendIndex(4, 6)  // 45
	rd2.AppendIndex(6, 10) // 678
	notify := make(chan string)
	buffer := influxdb.NewBuffer(context.Background(), "basic", nil, nil, 10*time.Second, notify)
	expected := []string{"01012345678876543210"}
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for _, result := range expected {
			<-notify
			data, err := ioutil.ReadAll(buffer)
			if err != nil {
				t.Error(err)
				return
			}

			if string(data) != result {
				t.Error("wrong result")
			}
			buffer.SeekZero()
			// 顺便测试一下重新读取
			data, err = ioutil.ReadAll(buffer)
			if err != nil {
				t.Error(err)
				return
			}
			if string(data) != result {
				t.Error("wrong result")
			}

		}
	}()
	buffer.AddReader(0, rd0)
	buffer.AddReader(1, rd)
	buffer.AddReader(2, rd2)
	wg.Wait()
}
