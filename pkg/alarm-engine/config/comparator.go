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

type ComparatorKafkaConfig struct {
	Brokers                  []string `yaml:"brokers"`
	TriggerInputTopic        string   `yaml:"trigger_input_topic"`
	GoDecisionTopic          string   `yaml:"go_decision_topic"`
	PythonDecisionTopic      string   `yaml:"python_decision_topic"`
	AuditOutputTopic         string   `yaml:"audit_output_topic"`
	AllowedAuditOutputTopics []string `yaml:"allowed_audit_output_topics"`
	GroupID                  string   `yaml:"group_id"`
	ClientID                 string   `yaml:"client_id"`
	BrokerVersion            string   `yaml:"broker_version"`
	MaxEntries               int      `yaml:"max_entries"`
	CoverageTimeout          Duration `yaml:"coverage_timeout"`
	BarrierInterval          Duration `yaml:"barrier_interval"`
}

func (c ComparatorKafkaConfig) ServiceCoordinates() enginekafka.ComparatorServiceConfig {
	return enginekafka.ComparatorServiceConfig{
		Brokers:             append([]string(nil), c.Brokers...),
		TriggerInputTopic:   c.TriggerInputTopic,
		GoDecisionTopic:     c.GoDecisionTopic,
		PythonDecisionTopic: c.PythonDecisionTopic,
		GroupID:             c.GroupID,
		ClientID:            c.ClientID,
		BrokerVersion:       c.BrokerVersion,
		MaxEntries:          c.MaxEntries,
		CoverageTimeout:     c.CoverageTimeout.Duration(),
		BarrierInterval:     c.BarrierInterval.Duration(),
	}
}

func (c ComparatorKafkaConfig) AuditSinkCoordinates() enginekafka.ComparisonAuditSinkConfig {
	return enginekafka.ComparisonAuditSinkConfig{
		Brokers:             append([]string(nil), c.Brokers...),
		InputTopics:         c.ServiceCoordinates().Topics(),
		OutputTopic:         c.AuditOutputTopic,
		AllowedOutputTopics: append([]string(nil), c.AllowedAuditOutputTopics...),
		ClientID:            c.ClientID,
		BrokerVersion:       c.BrokerVersion,
	}
}

type ComparatorConfig struct {
	Mode            string                `yaml:"mode"`
	HTTP            HTTPConfig            `yaml:"http"`
	Kafka           ComparatorKafkaConfig `yaml:"kafka"`
	ShutdownTimeout Duration              `yaml:"shutdown_timeout"`
}

func DefaultComparator() ComparatorConfig {
	return ComparatorConfig{
		Mode:            ModeShadow,
		HTTP:            HTTPConfig{Listen: "127.0.0.1:8081"},
		ShutdownTimeout: Duration(10 * time.Second),
	}
}

func LoadComparator(path string) (ComparatorConfig, error) {
	config := DefaultComparator()
	if path == "" {
		return config, config.Validate()
	}
	file, err := os.Open(path)
	if err != nil {
		return ComparatorConfig{}, fmt.Errorf("open comparator config: %w", err)
	}
	defer file.Close()
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		return ComparatorConfig{}, fmt.Errorf("decode comparator config: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return ComparatorConfig{}, errors.New("decode comparator config: multiple YAML documents are not allowed")
		}
		return ComparatorConfig{}, fmt.Errorf("decode comparator config: %w", err)
	}
	if err := config.Validate(); err != nil {
		return ComparatorConfig{}, err
	}
	return config, nil
}

func (c ComparatorConfig) Validate() error {
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
	if err := c.Kafka.ServiceCoordinates().Validate(); err != nil {
		return fmt.Errorf("comparator consumer configuration: %w", err)
	}
	if err := c.Kafka.AuditSinkCoordinates().Validate(); err != nil {
		return fmt.Errorf("comparison audit sink configuration: %w", err)
	}
	return nil
}
