// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package throttle

import (
	"fmt"
	"sync/atomic"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

var (
	httpRecordTypes atomic.Value // stores map[string]define.RecordType
	grpcRecordTypes atomic.Value // stores map[string]define.RecordType
)

// RegisterHTTPRecordType 注册参与限流的 HTTP 入站路径。receiver 应在 init 中用自己的路由常量登记。
func RegisterHTTPRecordType(path string, recordType define.RecordType) {
	registerRecordType(&httpRecordTypes, "http", path, recordType)
}

// RegisterGRPCRecordType 注册参与限流的 gRPC 全方法名。receiver 应在 init 中靠近服务注册处登记。
func RegisterGRPCRecordType(method string, recordType define.RecordType) {
	registerRecordType(&grpcRecordTypes, "grpc", method, recordType)
}

// ClassifyHTTP 按请求路径归类。表外的端点返回 RecordUndefined，由中间件放行、不限流。
func ClassifyHTTP(path string) define.RecordType {
	if rt, ok := loadRecordTypes(&httpRecordTypes)[path]; ok {
		return rt
	}
	return define.RecordUndefined
}

// ClassifyGRPC 按 gRPC 全方法名归类，未注册同样返回 RecordUndefined。
func ClassifyGRPC(method string) define.RecordType {
	if rt, ok := loadRecordTypes(&grpcRecordTypes)[method]; ok {
		return rt
	}
	return define.RecordUndefined
}

func registerRecordType(table *atomic.Value, protocol string, key string, recordType define.RecordType) {
	if key == "" {
		panic(fmt.Sprintf("throttle %s record type key is empty", protocol))
	}

	current := loadRecordTypes(table)
	if existing, ok := current[key]; ok {
		if existing == recordType {
			return
		}
		panic(fmt.Sprintf("throttle %s record type conflict: %s => %s/%s", protocol, key, existing.S(), recordType.S()))
	}

	next := make(map[string]define.RecordType, len(current)+1)
	for k, v := range current {
		next[k] = v
	}
	next[key] = recordType
	table.Store(next)
}

func loadRecordTypes(table *atomic.Value) map[string]define.RecordType {
	value := table.Load()
	if value == nil {
		return nil
	}
	return value.(map[string]define.RecordType)
}
