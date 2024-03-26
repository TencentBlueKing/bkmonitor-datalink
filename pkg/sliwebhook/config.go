// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type HttpConfig struct {
	Listen string `yaml:"listen"`
}

type Config struct {
	Http            HttpConfig     `yaml:"http"`
	Logger          logger.Options `yaml:"logger"`
	RefreshInterval time.Duration  `yaml:"refresh_interval"`
}

func (c *Config) Validate() {
	cfg := DefaultConfig()
	if c.Http.Listen == "" {
		c.Http.Listen = cfg.Http.Listen
	}
	if c.RefreshInterval <= 0 {
		c.RefreshInterval = cfg.RefreshInterval
	}
}

func DefaultConfig() *Config {
	return &Config{
		Http: HttpConfig{
			Listen: "localhost:8081",
		},
		Logger: logger.Options{
			Stdout: true,
		},
		RefreshInterval: time.Minute,
	}
}
