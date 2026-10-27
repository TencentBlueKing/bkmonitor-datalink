// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"sync/atomic"
	"time"

	elastic "github.com/olivere/elastic/v7"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// indexMetadataDecoder 保持 elastic 默认的 JSON 解码语义，只统计解码耗时，不记录响应内容。
// 解码耗时已包含在 index-get/get-mapping 耗时内，不能作为独立阶段重复累加。
type indexMetadataDecoder struct {
	duration atomic.Int64
	bytes    atomic.Int64
	calls    atomic.Int64
}

func (d *indexMetadataDecoder) Decode(data []byte, target any) error {
	startedAt := time.Now()
	defer func() {
		d.duration.Add(int64(time.Since(startedAt)))
		d.bytes.Add(int64(len(data)))
		d.calls.Add(1)
	}()
	return (&elastic.DefaultDecoder{}).Decode(data, target)
}

func (d *indexMetadataDecoder) record(span *trace.Span) {
	span.Set("mapping-decode-duration-ms", float64(d.duration.Load())/float64(time.Millisecond))
	// 只统计传给解码器的响应体大小，不含 HTTP 错误响应体、响应头和压缩开销，
	// 因此不能作为实际网络传输字节数。
	span.Set("mapping-response-bytes", d.bytes.Load())
	span.Set("mapping-decode-count", d.calls.Load())
}
