// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build !linux

package timesync

import (
	"math"
	"strings"
	"time"

	"github.com/beevik/ntp"
	"golang.org/x/sys/windows/registry"
)

type Client struct {
	opt *Option
}

func NewClient(opt *Option) *Client {
	if opt.Timeout == 0 {
		opt.Timeout = time.Second * 5
	}
	return &Client{
		opt: opt,
	}
}

func (c *Client) Query() (*Stat, error) {
	return c.queryNtpd()
}

func (c *Client) getTimeServer() ([]string, error) {
	const (
		// 暂不支持修改
		keyPath = `SYSTEM\CurrentControlSet\Services\W32Time\Parameters`
	)
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, keyPath, registry.QUERY_VALUE)
	if err != nil {
		return nil, err
	}
	defer key.Close()

	value, _, err := key.GetStringValue("NtpServer")
	if err != nil {
		return nil, err
	}

	var lst []string
	fields := strings.Fields(value)
	for _, field := range fields {
		parts := strings.Split(field, ",")
		if len(parts) != 2 {
			continue
		}

		lst = append(lst, parts[0])
	}
	return lst, nil
}

func (c *Client) queryNtpd() (*Stat, error) {
	stat := &Stat{
		Source: "ntpd",
		Min:    math.MaxInt64,
	}

	lst, err := c.getTimeServer()
	if err != nil {
		return nil, err
	}

	for _, server := range lst {
		rsp, err := ntp.QueryWithOptions(server, ntp.QueryOptions{Timeout: c.opt.Timeout})
		if err != nil {
			stat.Err++
			continue
		}

		delay := rsp.ClockOffset.Seconds()
		if delay == 0 {
			continue
		}

		stat.Count++
		stat.Sum += delay
		if stat.Max < delay {
			stat.Max = delay
		}
		if stat.Min > delay {
			stat.Min = delay
		}
	}
	return stat, nil
}
