// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kafkaclient

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// NormalizeBrokers 校验 broker 清单并返回 host 与 port 规范化后的副本。
func NormalizeBrokers(brokers []string) ([]string, error) {
	if len(brokers) == 0 {
		return nil, fmt.Errorf("brokers must contain at least one broker")
	}
	normalized := make([]string, len(brokers))
	seen := make(map[string]int, len(brokers))
	for index, broker := range brokers {
		canonical, err := CanonicalBroker(broker)
		if err != nil {
			return nil, fmt.Errorf("brokers[%d] %w", index, err)
		}
		if previous, exists := seen[canonical]; exists {
			return nil, fmt.Errorf("brokers[%d] duplicates brokers[%d]: %q", index, previous, broker)
		}
		seen[canonical] = index
		normalized[index] = canonical
	}
	return normalized, nil
}

// CanonicalBroker 校验单个 host:port 并规范化主机大小写、IP 和端口。
func CanonicalBroker(broker string) (string, error) {
	if err := validateText("must be a non-empty host:port", broker); err != nil {
		return "", err
	}
	host, portText, err := net.SplitHostPort(broker)
	if err != nil || host == "" {
		return "", fmt.Errorf("must be a valid host:port: %q", broker)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", fmt.Errorf("must contain a port between 1 and 65535: %q", broker)
	}
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	} else {
		host = strings.ToLower(host)
	}
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}

// ValidateTopic 校验 Kafka topic 名称。
func ValidateTopic(topic string) error {
	if topic == "" {
		return fmt.Errorf("topic is required")
	}
	if len(topic) > 249 || topic == "." || topic == ".." {
		return fmt.Errorf("topic must contain 1 to 249 valid characters")
	}
	for _, character := range topic {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '.' || character == '_' || character == '-' {
			continue
		}
		return fmt.Errorf("topic contains unsupported character %q", character)
	}
	return nil
}

// ValidateClientID 校验可选 Kafka client ID；空值表示使用客户端默认值。
func ValidateClientID(clientID string) error {
	if clientID == "" {
		return nil
	}
	return validateText("client_id", clientID)
}
