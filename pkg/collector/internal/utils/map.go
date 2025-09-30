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

	"go.opentelemetry.io/collector/pdata/pcommon"
)

// 标准字段的映射关系 使用缓存可提升性能 参见 benchmark
var dotToUnderline = map[string]string{
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
	"net.peer.name":              "net_peer_name",
	"net.peer.ip":                "net_peer_ip",
	"net.peer.port":              "net_peer_port",
	"http.scheme":                "http_scheme",
	"http.method":                "http_method",
	"http.flavor":                "http_favor",
	"http.status_code":           "http_status_code",
	"http.server_name":           "http_server_name",
	"rpc.method":                 "rpc_method",
	"rpc.service":                "rpc_service",
	"rpc.system":                 "rpc_system",
	"rpc.grpc.status_code":       "rpc_grpc_status_code",
	"peer.service":               "peer_service",
	"messaging.system":           "messaging_system",
	"messaging.destination":      "messaging_destination",
	"messaging.destination_kind": "messaging_destination_kind",
	"celery.action":              "celery_action",
	"celery.state":               "celery_state",
	"celery.task_name":           "celery_task_name",
}

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

// MergeMapWith 拷贝 map 并设置指定的键值对
//
// 优先考虑使用 MergeMapWith 而不是 MergeMaps 前者性能更优 参见 benchmark
// 所有的 Map 拷贝都应遵循 COW 原则（Copy On Write）
func MergeMapWith(m map[string]string, kv ...string) map[string]string {
	dst := MergeMaps(m)
	for i := 0; i < len(kv); i += 2 {
		if i+1 >= len(kv) {
			break
		}
		dst[kv[i]] = kv[i+1]
	}

	return dst
}

func MergeReplaceAttributeMaps(attrs ...pcommon.Map) map[string]string {
	dst := make(map[string]string)
	for _, attr := range attrs {
		attr.Range(func(k string, v pcommon.Value) bool {
			newKey, ok := dotToUnderline[k]
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
