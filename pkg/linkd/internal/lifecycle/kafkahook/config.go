// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kafkahook

import (
	"fmt"
	"math"

	"linkd/internal/kafkaclient"
)

const defaultMaxMessageBytes = 1 << 20

// Config 描述 Alert FinalHook 的 Kafka producer 和目标 topic。
type Config struct {
	Brokers         []string
	Topic           string
	ClientID        string
	MaxMessageBytes int
	Security        kafkaclient.SecurityConfig
}

// WithDefaults 返回补齐非敏感默认值且不共享 Brokers slice 的副本。
func (c Config) WithDefaults() Config {
	c.Brokers = append([]string(nil), c.Brokers...)
	if c.MaxMessageBytes == 0 {
		c.MaxMessageBytes = defaultMaxMessageBytes
	}
	c.Security = c.Security.WithDefaults()
	return c
}

// Validate 校验 broker、topic、消息上限和安全配置。
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
	if err := kafkaclient.ValidateClientID(c.ClientID); err != nil {
		return err
	}
	if c.MaxMessageBytes <= 0 || c.MaxMessageBytes > math.MaxInt32 {
		return fmt.Errorf("max_message_bytes must be between 1 and %d", math.MaxInt32)
	}
	return c.Security.Validate()
}
