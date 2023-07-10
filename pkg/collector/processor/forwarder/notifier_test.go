// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package forwarder

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func TestEndpointNotifier(t *testing.T) {
	logger.SetLoggerLevel("debug")
	notifier := NewEventNotifier()

	events := []Event{
		{Type: EventTypeAdd, Endpoint: ":1001"},
		{Type: EventTypeAdd, Endpoint: ":1002"},
	}

	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		count := 0
		for ev := range notifier.Watch() {
			assert.Equal(t, events[count], ev)
			count++
			if count >= 2 {
				return
			}
		}
	}()

	notifier.Sync([]string{":1001", ":1002"})
	wg.Wait()

	notifier.Sync([]string{":1001"})
	ev := <-notifier.Watch()
	assert.Equal(t, Event{Type: EventTypeDelete, Endpoint: ":1002"}, ev)
	notifier.Stop()
}
