// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tenant

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// parseGseMessageEndpointPort extracts the local GSE message port used by the
// Windows agent-message SDK. The deployed config may be rendered as a bare port
// (for example, "26001") or as a local address (for example, "127.0.0.1:26001").
func parseGseMessageEndpointPort(endpoint string) (uint, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return 0, fmt.Errorf("gse_message_endpoint is empty")
	}

	port := endpoint
	if _, err := strconv.Atoi(endpoint); err != nil {
		host, splitPort, err := net.SplitHostPort(endpoint)
		if err != nil {
			return 0, fmt.Errorf("invalid gse_message_endpoint %q: %w", endpoint, err)
		}
		// The current Windows SDK always dials 127.0.0.1:<port>, so accepting a
		// non-local host here would make the config look supported but behave differently.
		if host != "" && host != "127.0.0.1" && host != "localhost" && host != "::1" {
			return 0, fmt.Errorf("windows gse_message_endpoint only supports local address, got %q", host)
		}
		port = splitPort
	}

	value, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return 0, fmt.Errorf("invalid gse_message_endpoint port %q: %w", port, err)
	}
	if value == 0 {
		return 0, fmt.Errorf("gse_message_endpoint port must be greater than 0")
	}
	return uint(value), nil
}
