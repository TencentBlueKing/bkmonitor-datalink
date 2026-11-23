// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package metricmetadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	consulStore "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/consul"
)

// ConsulKafkaConfigKey is the fixed path below store.consul.pathPrefix.
const ConsulKafkaConfigKey = "metricmetadata/kafka"

// ErrConsulConfigNotFound indicates that the fixed Consul key has no value.
var ErrConsulConfigNotFound = errors.New("metric metadata Consul Kafka config not found")

// ConsulConfig contains the DataID and Kafka connection values stored in Consul.
type ConsulConfig struct {
	DataID   uint     `json:"data_id"`
	Brokers  []string `json:"brokers"`
	Topic    string   `json:"topic"`
	Username string   `json:"username"`
	Password string   `json:"password"`
}

// LoadConsulConfig reads the fixed Kafka config key below the configured Consul path prefix.
// 独立命令和 daemon 共用该入口，确保消费身份与 Kafka 连接来自同一配置。
func LoadConsulConfig(ctx context.Context) (ConsulConfig, error) {
	if ctx == nil {
		return ConsulConfig{}, errors.New("metric metadata context is required")
	}
	if err := ctx.Err(); err != nil {
		return ConsulConfig{}, err
	}

	client, err := consulStore.GetInstance()
	if err != nil {
		return ConsulConfig{}, fmt.Errorf("get metric metadata Consul client: %w", err)
	}
	key := path.Join(strings.Trim(config.StorageConsulPathPrefix, "/"), ConsulKafkaConfigKey)
	_, value, err := client.Get(key)
	if err != nil {
		return ConsulConfig{}, fmt.Errorf("get metric metadata Kafka config from Consul key %q: %w", key, err)
	}
	if len(value) == 0 {
		return ConsulConfig{}, fmt.Errorf("%w at key %q", ErrConsulConfigNotFound, key)
	}

	var consulConfig ConsulConfig
	if err := json.Unmarshal(value, &consulConfig); err != nil {
		return ConsulConfig{}, fmt.Errorf("decode metric metadata Kafka config from Consul key %q: %w", key, err)
	}
	if consulConfig.DataID == 0 {
		return ConsulConfig{}, fmt.Errorf("metric metadata Kafka config at key %q requires a positive data_id", key)
	}
	if len(consulConfig.Brokers) == 0 || consulConfig.Topic == "" {
		return ConsulConfig{}, fmt.Errorf("metric metadata Kafka config at key %q requires brokers and topic", key)
	}
	for _, broker := range consulConfig.Brokers {
		if strings.TrimSpace(broker) == "" {
			return ConsulConfig{}, fmt.Errorf("metric metadata Kafka config at key %q contains an empty broker", key)
		}
	}
	if (consulConfig.Username == "") != (consulConfig.Password == "") {
		return ConsulConfig{}, fmt.Errorf("metric metadata Kafka config at key %q requires both username and password for SASL", key)
	}
	if err := ctx.Err(); err != nil {
		return ConsulConfig{}, err
	}
	return consulConfig, nil
}
