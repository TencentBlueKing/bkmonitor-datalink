// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kafka

import (
	"fmt"
	"strings"

	"linkd/internal/kafkaclient"
)

// Config 描述一个 Kafka Consumer Group Session。
type Config struct {
	Brokers         []string
	Topic           string
	ConsumerGroup   string
	ClientID        string
	MaxFetchBytes   int32
	MessageIDHeader string
	TenantIDHeader  string
	OrderKeyHeader  string
	Security        kafkaclient.SecurityConfig
}

// WithDefaults 返回补齐非敏感适配器默认值的副本。
func (c Config) WithDefaults() Config {
	if c.MaxFetchBytes == 0 {
		c.MaxFetchBytes = 4 << 20
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
	c.Security = c.Security.WithDefaults()
	return c
}

// Validate 校验 Kafka Session 的静态配置。
func (c Config) Validate() error {
	if err := c.validateStatic(); err != nil {
		return err
	}
	_, err := c.Security.BuildTLSConfig()
	return err
}

func (c Config) validateStatic() error {
	if _, err := kafkaclient.NormalizeBrokers(c.Brokers); err != nil {
		return err
	}
	if err := kafkaclient.ValidateTopic(c.Topic); err != nil {
		return err
	}
	if strings.TrimSpace(c.ConsumerGroup) == "" {
		return fmt.Errorf("consumer_group must not be empty")
	}
	if c.MaxFetchBytes <= 0 {
		return fmt.Errorf("max_fetch_bytes must be positive: %d", c.MaxFetchBytes)
	}
	if err := kafkaclient.ValidateClientID(c.ClientID); err != nil {
		return err
	}
	return c.Security.Validate()
}
