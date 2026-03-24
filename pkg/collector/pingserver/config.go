// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pingserver

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	confTypePing = "ping"
)

type Config struct {
	Main *MainConfig
	Sub  *SubConfig
}

type MainConfig struct {
	Disabled   bool     `config:"disabled"`
	Patterns   []string `config:"patterns"`
	AutoReload bool     `config:"auto_reload"`
}

type SubConfig struct {
	Type         string        `config:"type"`
	DataId       int64         `config:"dataid"`
	Ping         PingConfig    `config:"ping"`
	Period       time.Duration `config:"period"`
	Times        int           `config:"total_num"`
	MaxBatchSize int           `config:"max_batch_size"`
	Server       ServerConfig  `config:"server"`
	Targets      []Target      `config:"config_list"`

	addrToBizId map[string]string
}

func (sc *SubConfig) setup() {
	if sc.addrToBizId == nil {
		sc.addrToBizId = make(map[string]string)
	}

	if sc.Period <= 0 {
		sc.Period = time.Minute
	}

	for _, target := range sc.Targets {
		addr := target.String()
		if target.BizId == "" {
			sc.addrToBizId[addr] = "0"
			continue
		}
		sc.addrToBizId[addr] = target.BizId
	}
}

func (sc *SubConfig) GetBizId(s string) (string, bool) {
	if sc.addrToBizId == nil {
		return "", false
	}
	v, ok := sc.addrToBizId[s]
	return v, ok
}

func (sc *SubConfig) Addrs() []*net.IPAddr {
	addrs := make([]*net.IPAddr, 0, len(sc.Targets))
	for _, target := range sc.Targets {
		ip := net.ParseIP(target.Ip)
		if ip == nil {
			logger.Warnf("failed to parse ip '%s'", target.Ip)
			continue
		}
		addrs = append(addrs, &net.IPAddr{IP: ip})
	}
	return addrs
}

type PingConfig struct {
	Timeout time.Duration `config:"timeout"` // 最大往返时间
}

type ServerConfig struct {
	Ip      string `config:"ip"`
	CloudID string `config:"cloud_id"`
	HostID  string `config:"bk_host_id"`
}

type Target struct {
	BizId   string `config:"target_biz_id"`
	Ip      string `config:"target_ip"`
	CloudID string `config:"target_cloud_id"`
}

func (t *Target) String() string {
	return FormatTarget(t.Ip, t.CloudID)
}

func FormatTarget(ip, cloudID string) string {
	return fmt.Sprintf("%s|%s", ip, cloudID)
}

func DefaultSubConfig() *SubConfig {
	return &SubConfig{
		Period: time.Minute,
		Times:  3,
		Server: ServerConfig{
			Ip:      "127.0.0.1",
			CloudID: "0",
		},
		Ping: PingConfig{
			Timeout: 3 * time.Second,
		},
	}
}

func LoadConfig(conf *confengine.Config) ([]string, *Config, error) {
	mc := &MainConfig{}
	if err := conf.UnpackChild(define.ConfigFieldPingserver, mc); err != nil {
		return nil, nil, err
	}

	if len(mc.Patterns) == 0 {
		return nil, &Config{Main: mc, Sub: &SubConfig{Period: time.Minute}}, nil
	}

	var patterns []string
	for _, p := range mc.Patterns {
		patterns = append(patterns, p)
		// TODO(remove): 临时兼容操作 后续 saas 将子配置文件下发至 'bk-collector' 文件夹下后可删除
		patterns = append(patterns, strings.ReplaceAll(p, "/bk-collector/", "/bkmonitorproxy/"))
	}

	unpackCfgs := make([]*confengine.Config, 0)
	for _, p := range patterns {
		cfgs, err := confengine.LoadConfigPattern(p)
		if err != nil {
			logger.Errorf("pingserver: failed to load pattern %s, err: %v", p, err)
			continue
		}
		unpackCfgs = append(unpackCfgs, cfgs...)
	}

	merged := &SubConfig{}
	union := make(map[Target]struct{})
	var finalSubConfig *SubConfig

	for _, cfg := range unpackCfgs {
		sc := DefaultSubConfig()
		if err := cfg.Unpack(&sc); err != nil {
			logger.Errorf("failed to unpack pingserver subconfig: %v", err)
			continue
		}

		// 只处理 ping 类型配置
		if sc.Type != confTypePing {
			logger.Debugf("pingserver skip type %s subconfig", sc.Type)
			continue
		}

		// 记录默认第一个 subConfig 内容
		if finalSubConfig == nil {
			finalSubConfig = sc
		}

		// 合并 target 配置
		for _, target := range sc.Targets {
			if _, ok := union[target]; !ok {
				union[target] = struct{}{}
				merged.Targets = append(merged.Targets, target)
			}
		}
	}

	// 没有子配置文件就置空
	if finalSubConfig == nil {
		finalSubConfig = DefaultSubConfig()
	}

	finalSubConfig.Targets = merged.Targets
	finalSubConfig.setup()
	return patterns, &Config{Main: mc, Sub: finalSubConfig}, nil
}
