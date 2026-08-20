// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"

	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/kafka"
)

const ModeShadow = "shadow"

type Duration time.Duration

func (d *Duration) UnmarshalText(text []byte) error {
	value, err := time.ParseDuration(string(text))
	if err != nil {
		return fmt.Errorf("parse duration: %w", err)
	}
	*d = Duration(value)
	return nil
}

func (d Duration) Duration() time.Duration {
	return time.Duration(d)
}

type HTTPConfig struct {
	Listen string `yaml:"listen"`
}

type KafkaConfig struct {
	Brokers             []string `yaml:"brokers"`
	InputTopic          string   `yaml:"input_topic"`
	OutputTopic         string   `yaml:"output_topic"`
	AllowedOutputTopics []string `yaml:"allowed_output_topics"`
	GroupID             string   `yaml:"group_id"`
	ClientID            string   `yaml:"client_id"`
	BrokerVersion       string   `yaml:"broker_version"`
}

func (c KafkaConfig) ConsumerCoordinates() enginekafka.Config {
	return enginekafka.Config{
		Brokers:       append([]string(nil), c.Brokers...),
		Topic:         c.InputTopic,
		GroupID:       c.GroupID,
		ClientID:      c.ClientID,
		BrokerVersion: c.BrokerVersion,
	}
}

func (c KafkaConfig) DecisionSinkCoordinates() enginekafka.DecisionSinkConfig {
	return enginekafka.DecisionSinkConfig{
		Brokers:             append([]string(nil), c.Brokers...),
		InputTopic:          c.InputTopic,
		OutputTopic:         c.OutputTopic,
		AllowedOutputTopics: append([]string(nil), c.AllowedOutputTopics...),
		ClientID:            c.ClientID,
		BrokerVersion:       c.BrokerVersion,
	}
}

type Config struct {
	Mode            string      `yaml:"mode"`
	HTTP            HTTPConfig  `yaml:"http"`
	Kafka           KafkaConfig `yaml:"kafka"`
	ShutdownTimeout Duration    `yaml:"shutdown_timeout"`
}

func Default() Config {
	return Config{
		Mode: ModeShadow,
		HTTP: HTTPConfig{
			Listen: "127.0.0.1:8080",
		},
		ShutdownTimeout: Duration(10 * time.Second),
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, cfg.Validate()
	}

	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open config: %w", err)
	}
	defer file.Close()

	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Config{}, errors.New("decode config: multiple YAML documents are not allowed")
		}
		return Config{}, fmt.Errorf("decode config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.Mode != ModeShadow {
		return fmt.Errorf("mode %q is not allowed before production ownership is implemented", c.Mode)
	}

	host, port, err := net.SplitHostPort(c.HTTP.Listen)
	if err != nil {
		return fmt.Errorf("http listen %q: %w", c.HTTP.Listen, err)
	}
	if host == "" {
		return fmt.Errorf("http listen %q has empty host", c.HTTP.Listen)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber <= 0 || portNumber > 65535 {
		return fmt.Errorf("http listen %q has invalid port", c.HTTP.Listen)
	}

	if c.ShutdownTimeout.Duration() <= 0 {
		return errors.New("shutdown_timeout must be positive")
	}
	if err := c.Kafka.ConsumerCoordinates().Validate(); err != nil {
		return fmt.Errorf("consumer configuration: %w", err)
	}
	if err := c.Kafka.DecisionSinkCoordinates().Validate(); err != nil {
		return fmt.Errorf("decision sink configuration: %w", err)
	}
	return nil
}
