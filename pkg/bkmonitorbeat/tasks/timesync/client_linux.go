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
	"net"
	"os"
	"strings"
	"time"

	"github.com/beevik/ntp"
	"github.com/facebook/time/ntp/chrony"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var newChronyConn = func(addr string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("udp", addr, timeout)
}

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
	stat, _, err := c.QueryAll()
	return stat, err
}

// QueryAll 查询本地 NTP 源，同时返回旧聚合统计和按地址明细。
// 旧聚合统计保持原有规则不变；按地址明细用于新增指标。
func (c *Client) QueryAll() (*Stat, []SourceResult, error) {
	if c.opt.ChronyAddr == "" && c.opt.NtpdPath == "" {
		return nil, nil, errors.New("no source found")
	}

	if c.opt.ChronyAddr != "" {
		stat, details, err := c.queryChronyDetailed()
		if err == nil {
			return stat, details, nil
		}
		// 默认配置里 chrony / ntpd 都会存在
		logger.Warnf("failed to query chrony: %v", err)
	}
	if c.opt.NtpdPath != "" {
		stat, details, err := c.queryNtpdDetailed()
		if err == nil {
			return stat, details, nil
		}
		logger.Warnf("failed to query ntpd: %v", err)
	}

	return nil, nil, errors.New("no source available")
}

func (c *Client) queryNtpd() (*Stat, error) {
	stat, _, err := c.queryNtpdDetailed()
	return stat, err
}

func (c *Client) queryNtpdDetailed() (*Stat, []SourceResult, error) {
	b, err := os.ReadFile(c.opt.NtpdPath)
	if err != nil {
		return nil, nil, err
	}

	stat := newStat("ntpd")
	details := make([]SourceResult, 0)
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
		address := parts[1]
		rsp, err := queryNTP(address, ntp.QueryOptions{Timeout: c.opt.Timeout})
		if err != nil {
			stat.Err++
			details = append(details, SourceResult{Address: address, Target: "ntpd", Available: false})
			continue
		}

		delay := rsp.ClockOffset.Seconds()
		// 旧聚合指标保持原行为：直接累加 ClockOffset，0 值跳过。
		// 新增指标统一为目标机时间 - 时间源时间，因此取 -ClockOffset。
		details = append(details, SourceResult{
			Address:       address,
			Target:        "ntpd",
			OffsetSeconds: ntpResponseOffsetSeconds(rsp),
			Available:     true,
		})
		if delay == 0 {
			continue
		}

		stat.Add(delay)
	}
	return stat, details, nil
}

func (c *Client) queryChrony() (*Stat, error) {
	stat, _, err := c.queryChronyDetailed()
	return stat, err
}

func (c *Client) queryChronyDetailed() (*Stat, []SourceResult, error) {
	conn, err := newChronyConn(c.opt.ChronyAddr, c.opt.Timeout)
	if err != nil {
		return nil, nil, err
	}
	defer conn.Close()

	client := chrony.Client{
		Sequence:   1,
		Connection: conn,
	}
	packet, err := c.communicateChrony(conn, &client, chrony.NewSourcesPacket())
	if err != nil {
		return nil, nil, err
	}
	sources, ok := packet.(*chrony.ReplySources)
	if !ok {
		return nil, nil, errors.Errorf("want *chrony.ReplySources type, but got %T", packet)
	}

	stat := newStat("chrony")
	details := make([]SourceResult, 0, sources.NSources)
	for i := 0; i < sources.NSources; i++ {
		packet, err = c.communicateChrony(conn, &client, chrony.NewSourceDataPacket(int32(i)))
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

		address := sourceData.IPAddr.String()
		// 异常节点：旧指标累加 Err，新指标上报 available=0。
		if sourceData.State == chrony.SourceStateUnreach {
			stat.Err++
			details = append(details, SourceResult{Address: address, Target: "chrony", Available: false})
			continue
		}

		stat.Add(sourceData.LatestMeas)
		// chrony LatestMeas 为本机时间 - 时间源时间，新指标直接使用。
		details = append(details, SourceResult{
			Address:       address,
			Target:        "chrony",
			OffsetSeconds: sourceData.LatestMeas,
			Available:     true,
		})
	}
	return stat, details, nil
}

func (c *Client) communicateChrony(conn net.Conn, client *chrony.Client, packet chrony.RequestPacket) (chrony.ResponsePacket, error) {
	if err := conn.SetDeadline(time.Now().Add(c.opt.Timeout)); err != nil {
		return nil, err
	}

	return client.Communicate(packet)
}
