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

// indexMetadataDecoder preserves elastic's default JSON semantics and measures
// only decoding, without recording response contents. Its duration is included
// in index-get/get-mapping duration, not an additional sequential phase.
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
	// Counts only payloads passed to the decoder, excluding HTTP error bodies,
	// headers and compression overhead; this is not a wire-byte measurement.
	span.Set("mapping-response-bytes", d.bytes.Load())
	span.Set("mapping-decode-count", d.calls.Load())
}
