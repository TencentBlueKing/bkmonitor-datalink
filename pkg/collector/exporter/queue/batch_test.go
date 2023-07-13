// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package queue

import (
	"sync"
	"testing"
	"time"

	"github.com/elastic/beats/libbeat/common"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/exporter/sizeobserver"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type testEvent struct {
	define.CommonEvent
}

func (t testEvent) RecordType() define.RecordType {
	return define.RecordPushGateway
}

func TestQueueOut(t *testing.T) {
	conf := Config{
		MetricsBatchSize: 100,
		LogsBatchSize:    2000,
		TracesBatchSize:  100,
		FlushInterval:    time.Second,
	}
	queue := NewBatchQueue(conf, nil)

	dataIDs := []int32{1001, 1002}
	wg := sync.WaitGroup{}
	for _, id := range dataIDs {
		cloned := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 10000; i++ {
				queue.Put(&testEvent{define.NewCommonEvent(cloned, common.MapStr{"count": i})})
			}
		}()
	}

	var total int
	for {
		ms := <-queue.Pop()
		v, _ := ms.GetValue("data")
		data := v.([]common.MapStr)
		total += len(data)
		if total == 20000 {
			break
		}
	}
	wg.Wait()
	queue.Close()
}

func TestQueueOutWithDelta(t *testing.T) {
	conf := Config{
		MetricsBatchSize: 100,
		LogsBatchSize:    100,
		TracesBatchSize:  100,
		FlushInterval:    time.Second,
	}
	queue := NewBatchQueue(conf, nil)

	dataIDs := []int32{1001, 1002}
	wg := sync.WaitGroup{}
	for _, id := range dataIDs {
		cloned := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				time.Sleep(time.Millisecond)
				queue.Put(&testEvent{define.NewCommonEvent(cloned, common.MapStr{"count": i})})
			}
		}()
	}

	var total int
	for {
		ms := <-queue.Pop()
		v, _ := ms.GetValue("data")
		data := v.([]common.MapStr)
		total += len(data)
		if total == 2000 {
			break
		}
	}
	wg.Wait()
	queue.Close()
}

func TestQueueFull(t *testing.T) {
	conf := Config{
		MetricsBatchSize: 100,
		LogsBatchSize:    1,
		TracesBatchSize:  100,
		FlushInterval:    2 * time.Second,
	}
	queue := NewBatchQueue(conf, nil)
	wg := sync.WaitGroup{}
	wg.Add(1)

	cases := map[int32]int32{
		1001: 1,
		1002: 2,
		1003: 3,
		1004: 4,
	}
	done := make(chan struct{})
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			for k, v := range cases {
				evt := &testEvent{define.NewCommonEvent(k, common.MapStr{"count": v})}
				queue.Put(evt)
			}
		}
		done <- struct{}{}
	}()

	for {
		select {
		case e := <-queue.Pop():
			data, err := e.GetValue("data")
			assert.NoError(t, err)

			dataID, err := e.GetValue("dataid")
			assert.NoError(t, err)

			actual := data.([]common.MapStr)[0]["count"]
			expected := cases[dataID.(int32)]
			assert.Equal(t, expected, actual)

		case <-done:
			return
		}
	}
}

func TestQueueFullBatch(t *testing.T) {
	conf := Config{
		MetricsBatchSize: 100,
		LogsBatchSize:    1,
		TracesBatchSize:  100,
		FlushInterval:    2 * time.Second,
	}
	queue := NewBatchQueue(conf, nil)
	wg := sync.WaitGroup{}
	wg.Add(1)

	cases := map[int32]int32{
		1001: 1,
		1002: 2,
		1003: 3,
		1004: 4,
	}

	done := make(chan struct{})
	go func() {
		defer wg.Done()
		for k, v := range cases {
			events := make([]define.Event, 0)
			for i := 0; i < 100; i++ {
				evt := &testEvent{define.NewCommonEvent(k, common.MapStr{"count": v})}
				events = append(events, evt)
			}
			queue.Put(events...)
		}
		done <- struct{}{}
	}()

	for {
		select {
		case e := <-queue.Pop():
			data, err := e.GetValue("data")
			assert.NoError(t, err)

			dataID, err := e.GetValue("dataid")
			assert.NoError(t, err)

			actual := data.([]common.MapStr)[0]["count"]
			expected := cases[dataID.(int32)]
			assert.Equal(t, expected, actual)

		case <-done:
			return
		}
	}
}

func TestQueueTick(t *testing.T) {
	conf := Config{
		MetricsBatchSize: 100,
		LogsBatchSize:    101,
		TracesBatchSize:  100,
		FlushInterval:    2 * time.Second,
	}
	queue := NewBatchQueue(conf, nil)

	cases := map[int32]int32{
		1001: 1,
		1002: 2,
		1003: 3,
		1004: 4,
	}

	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			for k, v := range cases {
				evt := &testEvent{define.NewCommonEvent(k, common.MapStr{"count": v})}
				queue.Put(evt)
			}
		}
	}()

	n := 0
	for {
		select {
		case e := <-queue.Pop():
			data, err := e.GetValue("data")
			assert.NoError(t, err)

			dataID, err := e.GetValue("dataid")
			assert.NoError(t, err)

			actual := data.([]common.MapStr)[0]["count"]
			expected := cases[dataID.(int32)]
			assert.Equal(t, expected, actual)

			n++
			if n == 4 {
				return
			}
		}
	}
}

func TestQueueResize(t *testing.T) {
	conf := Config{
		MetricsBatchSize: 2,
		FlushInterval:    time.Minute,
	}
	queue := NewBatchQueue(conf, sizeobserver.New())
	queue.resizeInterval = time.Millisecond * 100
	wg := sync.WaitGroup{}
	wg.Add(1)

	logger.SetLoggerLevel(logger.DebugLevelDesc)
	cases := map[int32]int32{
		1001: 1,
		1002: 2,
		1003: 3,
		1004: 4,
	}

	go func() {
		defer wg.Done()
		for k, v := range cases {
			events := make([]define.Event, 0)
			for i := 0; i < 100; i++ {
				evt := &testEvent{define.NewCommonEvent(k, common.MapStr{"count": v})}
				events = append(events, evt)
			}
			time.Sleep(time.Millisecond * 500)
			queue.Put(events...)
		}
	}()

	go func() {
		n := 0
		for range time.Tick(time.Millisecond * 100) {
			n++
			if n > 20 {
				break
			}

			KB := 1024
			for k := range cases {
				queue.so.ObserveSize(k, n*KB)
			}
		}
	}()

	timer := time.NewTicker(time.Second * 3)
	defer timer.Stop()
	for {
		select {
		case <-queue.Pop():
		case <-timer.C:
			return
		}
	}
}
