// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package timesync

import (
	"errors"
	"time"

	"github.com/beevik/ntp"
)

// SourceResult 表示一个本地 NTP 时间源的采集结果。
// OffsetSeconds 统一为目标机时间减去时间源时间，供新增指标使用。
type SourceResult struct {
	Address       string
	Target        string
	OffsetSeconds float64
	Available     bool
}

// BkResult 表示一次蓝鲸 NTP 查询的结果。
type BkResult struct {
	Address       string
	Target        string
	TBlue         time.Time
	OffsetSeconds float64
	Available     bool
}

// queryNTP 是 NTP 查询入口，单元测试可以替换它注入查询结果。
var queryNTP = ntp.QueryWithOptions

// ntpResponseOffsetSeconds 将 beevik/ntp 的 ClockOffset 转换为“目标机时间 - 时间源时间”。
func ntpResponseOffsetSeconds(rsp *ntp.Response) float64 {
	return -rsp.ClockOffset.Seconds()
}

// QueryBlueking 查询蓝鲸侧唯一的 NTP 地址，返回 T_blue 与 bk_offset。
// 查询失败或响应校验失败时返回错误，调用方据此只上报可用性指标。
func (c *Client) QueryBlueking() (*BkResult, error) {
	if c.opt.BkNTPSource == "" {
		return nil, errors.New("no blueking ntp source")
	}

	rsp, err := queryNTP(c.opt.BkNTPSource, ntp.QueryOptions{Timeout: c.opt.Timeout})
	if err != nil {
		return nil, err
	}
	if err := rsp.Validate(); err != nil {
		return nil, err
	}

	return &BkResult{
		Address:       c.opt.BkNTPSource,
		Target:        "ntpd",
		TBlue:         time.Now().Add(rsp.ClockOffset),
		OffsetSeconds: ntpResponseOffsetSeconds(rsp),
		Available:     true,
	}, nil
}
