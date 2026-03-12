// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redis

import (
	"time"
)

const (
	ModeConfigPath     = "redis.mode"
	HostConfigPath     = "redis.host"
	PortConfigPath     = "redis.port"
	PasswordConfigPath = "redis.password"

	MasterNameConfigPath       = "redis.master_name"
	SentinelAddressConfigPath  = "redis.sentinel_address"
	SentinelPasswordConfigPath = "redis.sentinel_password"
	DataBaseConfigPath         = "redis.database"

	DialTimeoutConfigPath = "redis.dial_timeout"
	ReadTimeoutConfigPath = "redis.read_timeout"
	ServiceNameConfigPath = "redis.service_name"
	KVBasePathConfigPath  = "redis.kv_base_path"
)

var (
	Mode     string
	Host     string
	Port     int
	Password string

	MasterName       string
	SentinelAddress  []string
	SentinelPassword string
	DataBase         int

	ServiceName string
	KVBasePath  string

	DialTimeout time.Duration
	ReadTimeout time.Duration
)
