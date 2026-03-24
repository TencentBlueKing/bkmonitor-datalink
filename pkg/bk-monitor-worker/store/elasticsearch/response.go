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
	"io"
	"net/http"
	"time"

	"github.com/pkg/errors"
)

// Response 对es查询返回结果的封装
type Response struct {
	StatusCode int
	Header     http.Header
	Body       io.ReadCloser
}

// IsError 根据状态码判断异常
func (r *Response) IsError() bool {
	return r.StatusCode > 299
}

// IsSysError 根据状态码判断异常
func (r *Response) IsSysError() bool {
	return r.StatusCode > 499
}

// DealStatusCodeError 对异常状态码进行捕获
// 404 -> NotFoundErr
func (r *Response) DealStatusCodeError() error {
	if r.IsError() {
		switch r.StatusCode {
		case http.StatusNotFound:
			if r.Body != nil {
				r.Body.Close()
			}
			return NotFoundErr
		default:
			var body []byte
			if r.Body != nil {
				defer r.Close()
				body, _ = io.ReadAll(r.Body)
			}
			return errors.Errorf("es resp error, status code [%v], body:[%s]", r.StatusCode, body)
		}
	}
	return nil
}

func (r *Response) Close() {
	if r.Body != nil {
		r.Body.Close()
	}
}

// IndexStat IndexStat的Resp
type IndexStat struct {
	// 总计数据
	All struct {
		Primaries struct {
			Store IndexStatStoreInfo `json:"store"`
		} `json:"primaries"`
	} `json:"_all"`
	// 按索引分别统计数据
	Indices map[string]struct {
		Primaries struct {
			Store IndexStatStoreInfo `json:"store"`
		} `json:"primaries"`
	} `json:"indices"`
}

// IndexStatStoreInfo index stats中的store数据
type IndexStatStoreInfo struct {
	SizeInBytes             int64 `json:"size_in_bytes"`
	TotalDataSetSizeInBytes int64 `json:"total_data_set_size_in_bytes"`
	ReservedInBytes         int64 `json:"reserved_in_bytes"`
}

// IndexAlias GetAlias的resp
type IndexAlias map[string]struct {
	Aliases map[string]map[string]any `json:"aliases"`
}

// CountResp CountByIndex的resp
type CountResp struct {
	Count int64 `json:"count"`
}

// Snapshot GetSnapshot的Resp
type Snapshot struct {
	Snapshot           string    `json:"snapshot"`
	Uuid               string    `json:"uuid"`
	Repository         string    `json:"repository"`
	VersionId          int       `json:"version_id"`
	Version            string    `json:"version"`
	Indices            []string  `json:"indices"`
	DataStreams        []any     `json:"data_streams"`
	IncludeGlobalState bool      `json:"include_global_state"`
	State              string    `json:"state"`
	StartTime          time.Time `json:"start_time"`
	StartTimeInMillis  int64     `json:"start_time_in_millis"`
	EndTime            time.Time `json:"end_time"`
	EndTimeInMillis    int64     `json:"end_time_in_millis"`
	DurationInMillis   int       `json:"duration_in_millis"`
	Failures           []any     `json:"failures"`
	FeatureStates      []struct {
		FeatureName string   `json:"feature_name"`
		Indices     []string `json:"indices"`
	} `json:"feature_states"`
}

// SnapshotResp es快照查询resp
type SnapshotResp struct {
	Snapshots []Snapshot `json:"snapshots"`
}

// AliasResp GetAlias的resp
type AliasResp map[string]struct {
	Aliases map[string]struct {
		IsWriteIndex bool `json:"is_write_index"`
	} `json:"aliases"`
}
