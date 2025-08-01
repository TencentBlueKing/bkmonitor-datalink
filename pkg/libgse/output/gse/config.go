// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package gse

import (
	"strconv"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/gse"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/host"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type BKAddressingType string

const (
	BKAddressingTypeStatic  BKAddressingType = "static"
	BKAddressingTypeDynamic BKAddressingType = "dynamic"
)

// Unpack 必须实现该接口方法，否则现有配置解析库解析该类型字段会panic
func (a *BKAddressingType) Unpack(s string) error {
	*a = BKAddressingType(s)
	return nil
}

type Config struct {
	// gse client config
	RetryTimes     uint          `config:"retrytimes"`
	RetryInterval  time.Duration `config:"retryinterval"`
	Nonblock       bool          `config:"nonblock"`
	EventBufferMax int32         `config:"eventbuffermax"`
	MsgQueueSize   uint32        `config:"mqsize"`
	Endpoint       string        `config:"endpoint"`
	WriteTimeout   time.Duration `config:"writetimeout"` // unit: second
	FastMode       bool          `config:"fastmode"`     // 是否启用高性能模式（默认不启用）
	Concurrency    int           `config:"concurrency"`  // 并发数（仅在高性能模式下生效）
	FlowLimit      int           `config:"flowlimit"`    // unit: Bytes（仅在大于 0 时生效）

	BKAddressing BKAddressingType `config:"bk_addressing"`
	HostIP       string           `config:"hostip"`
	CloudId      *int32           `config:"cloudid"`
	HostId       int32            `config:"hostid"`

	// monitor config
	MonitorID  int32 `config:"monitorid"`  // <= 0 : disable bk monitor tag
	ResourceID int32 `config:"resourceid"` // <= 0 : disable resource report
}

var defaultConfig = Config{
	MonitorID: 295,
}

type AgentInfoFetcher struct {
	cli          *gse.GseClient
	bkAddressing BKAddressingType
	cloudid      *int32
	hostid       int32
	hostip       string
}

func NewAgentInfoFetcher(c Config, cll *gse.GseClient) *AgentInfoFetcher {
	aif := &AgentInfoFetcher{cli: cll}
	aif.bkAddressing = c.BKAddressing
	aif.cloudid = c.CloudId
	aif.hostip = c.HostIP
	aif.hostid = c.HostId
	ips := strings.Split(aif.hostip, ",")
	if len(ips) >= 2 {
		aif.hostip = ips[0]
	}

	return aif
}

var globalHostWatcher host.Watcher

func RegisterHostWatcher(w host.Watcher) {
	globalHostWatcher = w
}

func (aif *AgentInfoFetcher) Fetch() (gse.AgentInfo, error) {
	info, err := aif.cli.GetAgentInfo()
	if err != nil {
		return info, err
	}
	logger.Debugf("fetch agent info gse: %+v", info)

	// 优先以配置文件中 cloudid 和 hostip 为主
	if aif.cloudid != nil {
		info.Cloudid = *aif.cloudid
	}
	if aif.hostip != "" {
		info.IP = aif.hostip
	}
	if aif.hostid != 0 {
		info.HostID = aif.hostid
	}
	logger.Debugf("fetch agent info from config file: %+v", info)
	if globalHostWatcher != nil {
		w := globalHostWatcher
		if w.GetHostId() != 0 {
			info.HostID = w.GetHostId()
		}
		if w.GetBizId() != 0 {
			info.BKBizID = int32(w.GetBizId())
		}
		i, _ := strconv.Atoi(w.GetCloudId())
		if i != 0 {
			info.Cloudid = int32(i)
		}
		if info.IP == "" {
			// 如果 agent 中没有 IP 信息，则从主机身份文件中获取
			info.IP = w.GetHostInnerIp()
		}
		if w.GetTenantID() != "" {
			info.BKTenantID = w.GetTenantID()
		}
		if w.GetStaticDataID() != 0 {
			info.StaticDataID = w.GetStaticDataID()
		}
		logger.Debugf("fetch agent info from host watcher: %+v", info)
	}
	logger.Debugf("fetch agent info: %+v", info)
	if aif.bkAddressing == BKAddressingTypeDynamic {
		info.IP = ""
	}
	return info, nil
}
