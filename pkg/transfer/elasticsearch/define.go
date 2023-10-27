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

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
)

// IndexRenderFn :
type IndexRenderFn func(record *Record) (string, error)

// Transport :
type Transport interface {
	Perform(*http.Request) (*http.Response, error)
}

// Response :
type Response struct {
	StatusCode int
	Header     http.Header
	Body       io.ReadCloser
}

// IsError :
func (r *Response) IsError() bool {
	return r.StatusCode > 299
}

// IsSysError :
func (r *Response) IsSysError() bool {
	return r.StatusCode > 499
}

// Client
type BulkWriter interface {
	Write(ctx context.Context, index string, records Records) (*Response, error)
	Close() error
}

// BulkWriterCreator :
type BulkWriterCreator func(config map[string]interface{}) (BulkWriter, error)

var writers = make(map[string]BulkWriterCreator)

// RegisterBulkWriter :
func RegisterBulkWriter(version string, creator BulkWriterCreator) {
	writers[version] = creator
}

// NewBulkWriter :
var NewBulkWriter = func(version string, config map[string]interface{}) (BulkWriter, error) {
	creator, ok := writers[version]
	if !ok {
		return nil, errors.WithMessagef(define.ErrItemNotFound, "version %s", version)
	}
	return creator(config)
}

// ESWriteResultError
type ESWriteResultError struct {
	Type     string `json:"type"`
	Reason   string `json:"reason"`
	CausedBy struct {
		Type   string `json:"type"`
		Reason string `json:"reason"`
	} `json:"caused_by"`
}

// ESWriteResult
type ESWriteResult struct {
	Took   int  `json:"took"`
	Errors bool `json:"errors"`
	Items  []struct {
		Index struct {
			Index  string              `json:"_index"`
			Type   string              `json:"_type"`
			ID     string              `json:"_id"`
			Status int                 `json:"status"`
			Error  *ESWriteResultError `json:"error"`
		} `json:"index"`
	} `json:"items"`
}
