// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package rabbitmq

import (
	"fmt"
	"strings"
)

// Config 描述一个 RabbitMQ queue consumer Session。
type Config struct {
	URL             string
	Queue           string
	ConsumerTag     string
	Prefetch        int
	MessageIDHeader string
	TenantIDHeader  string
	OrderKeyHeader  string
}

// WithDefaults 返回补齐适配器默认值的副本。
func (c Config) WithDefaults() Config {
	if c.Prefetch == 0 {
		c.Prefetch = 128
	}
	if c.MessageIDHeader == "" {
		c.MessageIDHeader = "message_id"
	}
	if c.TenantIDHeader == "" {
		c.TenantIDHeader = "bk_tenant_id"
	}
	if c.OrderKeyHeader == "" {
		c.OrderKeyHeader = "order_key"
	}
	return c
}

// Validate 校验 RabbitMQ Session 配置。
func (c Config) Validate() error {
	if strings.TrimSpace(c.URL) == "" {
		return fmt.Errorf("url must not be empty")
	}
	if strings.TrimSpace(c.Queue) == "" {
		return fmt.Errorf("queue must not be empty")
	}
	if c.Prefetch <= 0 {
		return fmt.Errorf("prefetch must be positive: %d", c.Prefetch)
	}
	return nil
}
