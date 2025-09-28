// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package alias

import "go.opentelemetry.io/collector/pdata/ptrace"

func serverKind(s string) KindKey {
	return KindKey{Kind: "SPAN_KIND_SERVER", Key: s} // 2
}

func clientKind(s string) KindKey {
	return KindKey{Kind: "SPAN_KIND_CLIENT", Key: s} // 3
}

var builtin = New()

var kfs = []KF{
	{K: serverKind("net.peer.name"), F: FuncContact("client.address", "client.port", ":")},
	{K: serverKind("net.peer.ip"), F: FuncOr("client.address")},
	{K: serverKind("net.peer.port"), F: FuncOr("client.port")},
	{K: clientKind("net.peer.name"), F: FuncContact("server.address", "server.port", ":")},
	{K: clientKind("net.peer.ip"), F: FuncOr("server.address", "network.peer.address")},
	{K: clientKind("net.peer.port"), F: FuncOr("server.port", "network.peer.port")},

	{K: clientKind("http.method"), F: FuncOr("http.request.method")},
	{K: clientKind("http.status_code"), F: FuncOr("http.response.status_code")},
	{K: clientKind("http.scheme"), F: FuncOr("url.scheme")},
	{K: clientKind("http.client_ip"), F: FuncOr("client.address")},

	{K: clientKind("db.system"), F: FuncOr("db.system.name")},
	{K: clientKind("db.name"), F: FuncOr("db.namespace")},
	{K: clientKind("db.statement"), F: FuncOr("db.query.next")},
	{K: clientKind("db.operation"), F: FuncOr("db.operation.name")},
	{K: clientKind("db.sql.table"), F: FuncOr("db.collection.name")},
}

func init() {
	for _, kf := range kfs {
		builtin.Register(kf)
	}
}

// GetAttributes 全局获取属性值
//
// 内置 builtin 转换规则
func GetAttributes(span ptrace.Span, key string) (string, bool) {
	return builtin.GetAttributes(span, key)
}
