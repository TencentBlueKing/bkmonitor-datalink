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
	"errors"
	"sync"
	"time"
)

// timeLayouts : Payload factory mappings
var timeLayouts = make(map[string]string)

var timeLayoutsMut sync.RWMutex

// RegisterTimeLayout 注册时间处理模板
var RegisterTimeLayout = func(name, layout string) {
	timeLayoutsMut.Lock() // write-lock
	defer timeLayoutsMut.Unlock()

	if name == "" {
		panic(errors.New("name can not be empty"))
	}

	// 不重复注册
	_, ok := timeLayouts[name]
	if ok {
		return
	}
	timeLayouts[name] = layout
}

// GetTimeLayout 获取时间处理模板
var GetTimeLayout = func(name string) (string, bool) {
	timeLayoutsMut.RLock() // read-lock
	defer timeLayoutsMut.RUnlock()

	layout, ok := timeLayouts[name]
	if !ok {
		return "", false
	}
	return layout, true
}

func init() {
	RegisterPlugin(&PluginInfo{
		Name: "TimeLayout",
		Registered: func() []string {
			keys := make([]string, 0, len(timeLayouts))
			for key := range timeLayouts {
				keys = append(keys, key)
			}
			return keys
		},
	})

	RegisterTimeLayout("default", "epoch_second")
	RegisterTimeLayout("timestamp", "epoch_second")
	RegisterTimeLayout("epoch_second", "epoch_second")
	RegisterTimeLayout("epoch_minute", "epoch_minute")
	RegisterTimeLayout("epoch_millis", "epoch_millis")
	RegisterTimeLayout("epoch_millisecond", "epoch_millis")
	RegisterTimeLayout("epoch_micros", "epoch_micros")
	RegisterTimeLayout("epoch_microsecond", "epoch_micros")
	RegisterTimeLayout("epoch_nanos", "epoch_nanos")
	RegisterTimeLayout("epoch_nanosecond", "epoch_nanos")
	RegisterTimeLayout("rfc822", time.RFC822)
	RegisterTimeLayout("rfc3339", time.RFC3339)
	RegisterTimeLayout("rfc3339_nano", time.RFC3339Nano)
	RegisterTimeLayout("date", "2006-01-02")
	RegisterTimeLayout("datetime", "2006-01-02 15:04:05")
	RegisterTimeLayout("ISO8601", "2006-01-02T15:04:05.000-0700")
}
