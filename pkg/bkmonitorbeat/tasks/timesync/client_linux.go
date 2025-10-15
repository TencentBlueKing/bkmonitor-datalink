// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build linux

package timesync

import (
	"bufio"
	"bytes"
	"math"
	"net"
	"os"
	"strings"
	"time"

	"github.com/beevik/ntp"
	"github.com/facebook/time/ntp/chrony"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
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
	if c.opt.ChronyAddr == "" && c.opt.NtpdPath == "" {
		return nil, errors.New("no source found")
	}

	var stat *Stat
	var err error
	if c.opt.ChronyAddr != "" {
		stat, err = c.queryChrony()
		if err == nil {
			return stat, nil
		}
		// 默认配置里 chrony / ntpd 都会存在
		logger.Warnf("failed to query chrony: %v", err)
	}
	if c.opt.NtpdPath != "" {
		stat, err = c.queryNtpd()
		if err == nil {
			return stat, nil
		}
		logger.Warnf("failed to query ntpd: %v", err)
	}

	return nil, errors.New("no source available")
}

func (c *Client) queryNtpd() (*Stat, error) {
	b, err := os.ReadFile(c.opt.NtpdPath)
	if err != nil {
		return nil, err
	}

	stat := &Stat{
		Source: "ntpd",
		Min:    math.MaxInt64,
	}
	scanner := bufio.NewScanner(bytes.NewBuffer(b))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "server") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}
		rsp, err := ntp.QueryWithOptions(parts[1], ntp.QueryOptions{Timeout: c.opt.Timeout})
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

func (c *Client) queryChrony() (*Stat, error) {
	conn, err := net.DialTimeout("udp", c.opt.ChronyAddr, c.opt.Timeout)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	client := chrony.Client{
		Sequence:   1,
		Connection: conn,
	}
	packet, err := client.Communicate(chrony.NewSourcesPacket())
	if err != nil {
		return nil, err
	}
	sources, ok := packet.(*chrony.ReplySources)
	if !ok {
		return nil, errors.Errorf("want *chrony.ReplySources type, but got %T", packet)
	}

	stat := &Stat{
		Source: "chrony",
		Min:    math.MaxInt64,
	}
	for i := 0; i < sources.NSources; i++ {
		packet, err = client.Communicate(chrony.NewSourceDataPacket(int32(i)))
		if err != nil {
			logger.Warnf("client communicate error: %v", err)
			stat.Err++
			continue
		}
		sourceData, ok := packet.(*chrony.ReplySourceData)
		if !ok {
			stat.Err++
			logger.Warnf("want *chrony.ReplySourceData type, but got %T", packet)
			continue
		}

		// 本地时钟 忽略
		if sourceData.LatestMeas == 0 {
			continue
		}
		// 异常节点忽略
		if sourceData.State == chrony.SourceStateUnreach {
			stat.Err++
			continue
		}

		stat.Count++
		stat.Sum += sourceData.LatestMeas
		if stat.Max < sourceData.LatestMeas {
			stat.Max = sourceData.LatestMeas
		}
		if stat.Min > sourceData.LatestMeas {
			stat.Min = sourceData.LatestMeas
		}
	}
	return stat, nil
}
