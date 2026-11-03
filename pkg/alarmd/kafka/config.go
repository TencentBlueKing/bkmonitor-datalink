// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kafka

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

func validateBroker(broker string) error {
	if broker == "" || strings.TrimSpace(broker) != broker {
		return fmt.Errorf("kafka: broker %q must be canonical host:port", broker)
	}
	host, port, err := net.SplitHostPort(broker)
	if err != nil {
		return fmt.Errorf("kafka: broker %q: %w", broker, err)
	}
	if host == "" {
		return fmt.Errorf("kafka: broker %q has empty host", broker)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber <= 0 || portNumber > 65535 {
		return fmt.Errorf("kafka: broker %q has invalid port", broker)
	}
	return nil
}
