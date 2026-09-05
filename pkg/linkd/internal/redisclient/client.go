// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redisclient

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	redis "github.com/redis/go-redis/v9"
)

// Options 描述 Redis 数据节点认证和可选的 Sentinel master 发现配置。
type Options struct {
	Address  string
	Username string
	Password string
	Database int
	Sentinel *SentinelOptions
}

// SentinelOptions 描述 Sentinel seed、master 名称和 Sentinel 自身认证。
// Username 和 Password 只用于 Sentinel；数据节点仍使用 Options 的认证字段。
type SentinelOptions struct {
	MasterName string
	Addresses  []string
	Username   string
	Password   string
}

// Validate 校验 standalone 或 Sentinel 连接所需的完整地址边界。
func (o Options) Validate() error {
	if o.Database < 0 {
		return fmt.Errorf("database must not be negative")
	}
	if o.Sentinel == nil {
		return validateHostPort("address", o.Address)
	}
	if o.Address != "" {
		return fmt.Errorf("address must be empty when sentinel is configured")
	}
	if strings.TrimSpace(o.Sentinel.MasterName) != o.Sentinel.MasterName || o.Sentinel.MasterName == "" {
		return fmt.Errorf("sentinel.master_name must not be empty or contain surrounding whitespace")
	}
	if len(o.Sentinel.Addresses) == 0 {
		return fmt.Errorf("sentinel.addresses must not be empty")
	}
	seen := make(map[string]struct{}, len(o.Sentinel.Addresses))
	for index, address := range o.Sentinel.Addresses {
		if err := validateHostPort(fmt.Sprintf("sentinel.addresses[%d]", index), address); err != nil {
			return err
		}
		if _, exists := seen[address]; exists {
			return fmt.Errorf("sentinel.addresses[%d] duplicates %q", index, address)
		}
		seen[address] = struct{}{}
	}
	return nil
}

// New 校验配置并创建直连单节点或通过 Sentinel 跟随 master 的 Redis client。
// Sentinel 模式不把只读命令路由到 replica，避免 Mailbox、lease 与缓存出现读写不一致。
func New(options Options) (*redis.Client, error) {
	if err := options.Validate(); err != nil {
		return nil, fmt.Errorf("create redis client: %w", err)
	}
	if options.Sentinel == nil {
		return redis.NewClient(&redis.Options{
			Addr:     options.Address,
			Username: options.Username,
			Password: options.Password,
			DB:       options.Database,
		}), nil
	}
	return redis.NewFailoverClient(newFailoverOptions(options)), nil
}

func newFailoverOptions(options Options) *redis.FailoverOptions {
	return &redis.FailoverOptions{
		MasterName:       options.Sentinel.MasterName,
		SentinelAddrs:    append([]string(nil), options.Sentinel.Addresses...),
		SentinelUsername: options.Sentinel.Username,
		SentinelPassword: options.Sentinel.Password,
		Username:         options.Username,
		Password:         options.Password,
		DB:               options.Database,
	}
}

func validateHostPort(name, address string) error {
	if strings.TrimSpace(address) != address || address == "" {
		return fmt.Errorf("%s must be host:port", name)
	}
	host, portText, err := net.SplitHostPort(address)
	if err != nil || host == "" {
		return fmt.Errorf("%s must be host:port", name)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("%s port must be between 1 and 65535", name)
	}
	return nil
}
