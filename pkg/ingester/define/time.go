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
	"strconv"
	"time"
)

type TimeFormatter func(layout string, t time.Time) string

// timeLayouts : Payload factory mappings
var timeLayouts = make(map[string]string)

// RegisterTimeLayout : register Payload to factory
var RegisterTimeLayout = func(name, layout string) {
	if name == "" {
		panic(errors.New("name can not be empty"))
	}
	timeLayouts[name] = layout
}

// UnregisterTimeLayout
var UnregisterTimeLayout = func(name string) (string, bool) {
	layout, ok := timeLayouts[name]
	if !ok {
		return "", false
	}
	delete(timeLayouts, name)
	return layout, true
}

// GetTimeLayout
var GetTimeLayout = func(name string) (string, bool) {
	layout, ok := timeLayouts[name]
	if !ok {
		return "", false
	}
	return layout, true
}

func timestampFormatByDuration(d time.Duration) TimeFormatter {
	duration := int64(d / time.Nanosecond)
	return func(layout string, t time.Time) string {
		return strconv.FormatInt(t.UnixNano()/duration, 10)
	}
}

func defaultTimeFormatter(layout string, t time.Time) string {
	return t.Format(layout)
}

var layoutFormatters = map[string]TimeFormatter{
	"epoch_nanos":  timestampFormatByDuration(time.Nanosecond),
	"epoch_micros": timestampFormatByDuration(time.Microsecond),
	"epoch_millis": timestampFormatByDuration(time.Millisecond),
	"epoch_second": timestampFormatByDuration(time.Second),
	"epoch_minute": timestampFormatByDuration(time.Minute),
}

func FormatTimeByLayout(layout string, t time.Time) string {
	if formatter, ok := layoutFormatters[layout]; ok {
		return formatter(layout, t)
	}
	return defaultTimeFormatter(layout, t)
}

func init() {
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
}
