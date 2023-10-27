// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package backend

import (
	"net/http"
	"time"
)

// Backend :
type Backend interface {
	// String : 返回唯一字符描述标识
	String() string
	// Write : 写入数据
	Write(flow uint64, urlParams *WriteParams, reader CopyReader, header http.Header) (*Response, error)
	// Query : 读取数据
	Query(flow uint64, urlParams *QueryParams, header http.Header) (*Response, error)

	// RawQuery 透传请求
	RawQuery(flow uint64, request *http.Request) (*http.Response, error)
	// CreateDatabase : 创建数据库，传入q而非DB名，是为了防止语句有复杂的配置需要解析
	CreateDatabase(flow uint64, urlParams *QueryParams, header http.Header) (*Response, error)
	// GetVersion : 获取influxDB版本号
	GetVersion() string
	// Close : 关闭 backend
	Close() error
	// Wait : 等待 backend 资源清理完毕，在调用Close方法后调用
	Wait()
	// Name: 返回backend的配置名字
	Name() string
	Ping(time.Duration) (time.Duration, string, error)
	// Reset 重置参数
	Reset(config *BasicConfig) error
	// 是否可读
	Readable() bool
	// 是否禁用
	Disabled() bool
}

// CopyReader 可复制Reader
type CopyReader interface {
	Copy() CopyReader
	Read(b []byte) (int, error)
	AppendIndex(start, end int)
	SeekZero()
	PointCount() int
}
