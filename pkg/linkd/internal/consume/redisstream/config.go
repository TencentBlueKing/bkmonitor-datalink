// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redisstream

import (
	"fmt"
	"strings"
	"time"

	"linkd/internal/consume"
)

// Config 描述一个 Redis Streams Consumer Group Session。
type Config struct {
	Address        string
	Username       string
	Password       string
	DB             int
	UseTLS         bool
	Stream         string
	Group          string
	Consumer       string
	CreateGroup    bool
	ReadBlock      time.Duration
	ClaimMinIdle   time.Duration
	BodyField      string
	MessageIDField string
	TenantIDField  string
	OrderKeyField  string
}

// WithDefaults 返回补齐适配器默认值的副本。
func (c Config) WithDefaults() Config {
	if c.ReadBlock == 0 {
		c.ReadBlock = time.Second
	}
	if c.ClaimMinIdle == 0 {
		c.ClaimMinIdle = 5 * time.Minute
	}
	if c.BodyField == "" {
		c.BodyField = "payload"
	}
	if c.MessageIDField == "" {
		c.MessageIDField = "message_id"
	}
	if c.TenantIDField == "" {
		c.TenantIDField = "bk_tenant_id"
	}
	if c.OrderKeyField == "" {
		c.OrderKeyField = "order_key"
	}
	return c
}

// Validate 校验 Redis Streams Session 配置。
func (c Config) Validate() error {
	if strings.TrimSpace(c.Address) == "" {
		return fmt.Errorf("address must not be empty")
	}
	if strings.TrimSpace(c.Stream) == "" {
		return fmt.Errorf("stream must not be empty")
	}
	if strings.TrimSpace(c.Group) == "" {
		return fmt.Errorf("group must not be empty")
	}
	if strings.TrimSpace(c.Consumer) == "" {
		return fmt.Errorf("consumer must not be empty")
	}
	if c.DB < 0 {
		return fmt.Errorf("db must not be negative: %d", c.DB)
	}
	if c.ReadBlock <= 0 {
		return fmt.Errorf("read_block must be positive: %s", c.ReadBlock)
	}
	if c.ClaimMinIdle <= 0 {
		return fmt.Errorf("claim_min_idle must be positive: %s", c.ClaimMinIdle)
	}
	return nil
}

func (c Config) validateRuntime(runtimeConfig consume.Config) error {
	minimum := runtimeConfig.ProcessTimeout + runtimeConfig.RetryMaxElapsed
	if c.ClaimMinIdle <= minimum {
		return fmt.Errorf(
			"claim_min_idle must exceed process_timeout + retry_max_elapsed (%s): %s",
			minimum,
			c.ClaimMinIdle,
		)
	}
	return nil
}
