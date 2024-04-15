// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package types

import (
	"strconv"
	"time"
)

// TimeStamp :
type TimeStamp struct {
	time.Time
	unit string // 支持 s/ms/µs/ns（默认为 s）
}

// NewTimeStamp :
func NewTimeStamp(t time.Time) TimeStamp {
	return TimeStamp{
		Time: t,
	}
}

func (t *TimeStamp) SetUnit(u string) {
	t.unit = u
}

// String :
func (t TimeStamp) String() string {
	return strconv.FormatInt(t.Int64(), 10)
}

// Int64 :
func (t TimeStamp) Int64() int64 {
	var n int64
	switch t.unit {
	case "ms":
		n = t.UnixMilli()
	case "µs":
		n = t.UnixMicro()
	case "ns":
		n = t.UnixNano()
	default:
		n = t.Unix()
	}
	return n
}

// MarshalJSON :
func (t TimeStamp) MarshalJSON() ([]byte, error) {
	return []byte(t.String()), nil
}
