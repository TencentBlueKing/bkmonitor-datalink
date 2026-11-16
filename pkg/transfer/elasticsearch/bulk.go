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
	"context"
	"io"
	"net/http"
	"time"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/bufferpool"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

func retryableBulkStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= http.StatusInternalServerError
}

// ES 按请求顺序返回条目。先校验完整响应，再按位置关联文档；
// 回执不完整时无法确定哪些文档成功，需要重试当前待确认的文档。
func readBulkResponse(response *Response, size int) (*ESWriteResult, bool, error) {
	if response == nil || response.Body == nil {
		return nil, true, errors.New("empty elasticsearch bulk response")
	}
	defer func() { logging.WarnIf("close response error", response.Body.Close()) }()
	buf := bufferpool.Get()
	defer bufferpool.Put(buf)
	if _, err := io.Copy(buf, response.Body); err != nil {
		return nil, true, errors.Wrap(err, "read elasticsearch bulk response")
	}
	if response.StatusCode < http.StatusOK || response.IsError() {
		return nil, retryableBulkStatus(response.StatusCode), errors.Errorf("elasticsearch bulk HTTP %d: %s", response.StatusCode, buf.String())
	}
	var result ESWriteResult
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		return nil, true, errors.Wrap(err, "decode elasticsearch bulk response")
	}
	if len(result.Items) != size {
		return nil, true, errors.Errorf("elasticsearch bulk acknowledged %d of %d items", len(result.Items), size)
	}
	for i, item := range result.Items {
		if item.Index.Status < http.StatusOK || item.Index.Status > 599 {
			return nil, true, errors.Errorf("elasticsearch bulk item %d has invalid status %d", i, item.Index.Status)
		}
	}
	return &result, false, nil
}

func (b *BulkHandler) flush(ctx context.Context, index string, records Records) (count int, err error) {
	errs := utils.NewMultiErrors()
	var interval time.Duration
	if b.flushRetries > 0 {
		interval = b.flushInterval / time.Duration(b.flushRetries)
	}
	pending := records
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			errs.Add(err)
			return count, errs.AsError()
		}
		response, writeErr := b.writer.Write(ctx, index, pending)
		var result *ESWriteResult
		retryable := true
		if writeErr == nil {
			result, retryable, writeErr = readBulkResponse(response, len(pending))
		}
		if writeErr != nil {
			if !retryable || attempt >= b.flushRetries {
				errs.Add(writeErr)
				return count, errs.AsError()
			}
		} else {
			// 已确认成功和永久失败的文档都不再发送，只保留可以重试的条目。
			var retry Records
			for i, item := range result.Items {
				item := item.Index
				switch {
				case item.Status < http.StatusMultipleChoices && item.Error == nil:
					count++
				case retryableBulkStatus(item.Status) && attempt < b.flushRetries:
					retry = append(retry, pending[i])
				default:
					errs.Add(errors.Errorf("elasticsearch bulk index %s item %s status %d: %+v", item.Index, item.ID, item.Status, item.Error))
				}
			}
			pending = retry
			if len(pending) == 0 {
				return count, errs.AsError()
			}
		}
		logging.Warnf("backend %v retry %d/%d for %d documents in %s", b, attempt+1, b.flushRetries, len(pending), index)
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			errs.Add(ctx.Err())
			return count, errs.AsError()
		case <-timer.C:
		}
	}
}
