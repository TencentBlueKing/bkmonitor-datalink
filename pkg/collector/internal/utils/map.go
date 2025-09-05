// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils

import (
	"strings"

	"github.com/spf13/cast"
	"go.opentelemetry.io/collector/pdata/pcommon"
)

func CloneMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}

	dst := make(map[string]string)
	for key, value := range m {
		dst[key] = value
	}
	return dst
}

func MergeMaps(ms ...map[string]string) map[string]string {
	dst := make(map[string]string)
	for _, m := range ms {
		for k, v := range m {
			dst[k] = v
		}
	}

	return dst
}

// 标准字段的映射关系 使用缓存可提升性能 参见 benchmark
var mappings = map[string]string{
	"service.name":               "service_name",
	"service.version":            "service_version",
	"status.code":                "status_code",
	"bk.instance.id":             "bk_instance_id",
	"telemetry.sdk.name":         "telemetry_sdk_name",
	"telemetry.sdk.version":      "telemetry_sdk_version",
	"telemetry.sdk.language":     "telemetry_sdk_language",
	"db.name":                    "db_name",
	"db.operation":               "db_operation",
	"db.system":                  "db_system",
	"net.host.port":              "net_host_port",
	"net.host.name":              "net_host_name",
	"http.scheme":                "http_scheme",
	"http.method":                "http_method",
	"http.flavor":                "http_favor",
	"http.status_code":           "http_status_code",
	"http.server_name":           "http_server_name",
	"rpc.method":                 "rpc_method",
	"rpc.service":                "rpc_service",
	"rpc.grpc.status_code":       "rpc_grpc_status_code",
	"peer.service":               "peer_service",
	"messaging.system":           "messaging_system",
	"messaging.destination":      "messaging_destination",
	"messaging.destination_kind": "messaging_destination_kind",
}

func MergeReplaceMaps(ms ...map[string]string) map[string]string {
	dst := make(map[string]string)
	for _, m := range ms {
		for k, v := range m {
			newKey := strings.ReplaceAll(k, ".", "_")
			dst[newKey] = v
		}
	}

	return dst
}

func MergeReplaceAttributeMaps(attrs ...pcommon.Map) map[string]string {
	dst := make(map[string]string)
	for _, attr := range attrs {
		attr.Range(func(k string, v pcommon.Value) bool {
			newKey, ok := mappings[k]
			if ok {
				dst[newKey] = v.AsString()
			} else {
				newKey = strings.ReplaceAll(k, ".", "_")
				dst[newKey] = v.AsString()
			}
			return true
		})
	}
	return dst
}

func NameOpts(s string) (string, string) {
	if s == "" {
		return "", ""
	}

	nameOpts := strings.Split(s, ";")
	if len(nameOpts) == 1 {
		return nameOpts[0], ""
	}
	return nameOpts[0], nameOpts[1]
}

type OptMap struct {
	m map[string]any // 不会有并发读写
}

func NewOptMap(s string) *OptMap {
	m := make(map[string]any)
	pairs := strings.Split(s, ",")
	for _, pair := range pairs {
		kv := strings.Split(strings.TrimSpace(pair), "=")
		if len(kv) != 2 {
			continue
		}
		m[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
	}
	return &OptMap{m: m}
}

func (om *OptMap) GetInt(k string) (int, bool) {
	v, ok := om.m[k]
	if !ok {
		return 0, false
	}

	i, err := cast.ToIntE(v)
	if err != nil {
		return 0, false
	}
	return i, true
}

func (om *OptMap) GetIntDefault(k string, defaultVal int) int {
	i, ok := om.GetInt(k)
	if ok {
		return i
	}
	return defaultVal
}
