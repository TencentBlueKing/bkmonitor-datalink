// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package kachook

import (
	"fmt"
	"math"

	"linkd/internal/kafkaclient"
)

const defaultMaxMessageBytes = 1 << 20

// Config 描述 KAC Alarm producer 和目标 Kafka topic。
type Config struct {
	Brokers         []string
	Topic           string
	ClientID        string
	MaxMessageBytes int
	Security        kafkaclient.SecurityConfig
}

// WithDefaults 返回补齐默认值且不共享动态字段的副本。
func (c Config) WithDefaults() Config {
	c.Brokers = append([]string(nil), c.Brokers...)
	if c.MaxMessageBytes == 0 {
		c.MaxMessageBytes = defaultMaxMessageBytes
	}
	c.Security = c.Security.WithDefaults()
	return c
}

// Validate 校验 Kafka 连接、目标和消息大小。
func (c Config) Validate() error {
	c = c.WithDefaults()
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
