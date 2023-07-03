// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cluster

import (
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/common"
)

// Response cluster的返回信息
type Response struct {
	Result   string
	Err      error
	Code     int
	ErrCount int
}

func (br *Response) String() string {
	return fmt.Sprintf("result:%s,code:%d", br.Result, br.Code)
}

// NewResponse :
func NewResponse(result string, code int) *Response {
	return &Response{result, nil, code, 0}
}

// QueryParams cluster的查询请求参数
type QueryParams struct {
	DB          string `json:"db"`
	Measurement string `json:"measurement"`
	SQL         string `json:"q"`
	Epoch       string `json:"epoch"`
	Pretty      string `json:"pretty"`
	Chunked     string `json:"chunked"`
	ChunkSize   string `json:"chunk_size"`
	TagNames    []string
}

// NewQueryParams :
func NewQueryParams(db, measurement, sql, epoch, pretty, chunked, chunkSize string, tagNames []string) *QueryParams {
	return &QueryParams{db, measurement, sql, epoch, pretty, chunked, chunkSize, tagNames}
}

func (q *QueryParams) String() string {
	return fmt.Sprintf("db:%s sql:%s epoch:%s pretty:%s chunked:%s chunk_size:%s", q.DB, q.SQL, q.Epoch, q.Pretty, q.Chunked, q.ChunkSize)
}

// WriteParams cluster的写入参数
type WriteParams struct {
	DB          string `json:"db"`
	Consistency string `json:"consistency"`
	Precision   string `json:"precision"`
	RP          string `json:"rp"`
	Points      common.Points
	AllData     []byte
	TagNames    []string
}

// NewWriteParams :
func NewWriteParams(db, consistency, precision, rp string, points common.Points, allData []byte, tagNames []string) *WriteParams {
	return &WriteParams{db, consistency, precision, rp, points, allData, tagNames}
}

func (w *WriteParams) String() string {
	return fmt.Sprintf("db:%s consistency:%s precision:%s rp:%s", w.DB, w.Consistency, w.Precision, w.RP)
}

// Info 从consul获取的集群信息
type Info struct {
	HostList           []string `json:"host_list"`
	UnReadableHostList []string `json:"unreadable_host_list"`
}

// Compare 比较二者是否相同
func (c *Info) Compare(val *Info) bool {
	if c.stringSliceEqual(val.HostList, c.HostList) &&
		c.stringSliceEqual(val.UnReadableHostList, c.UnReadableHostList) {
		return true
	}

	return false
}

func (c *Info) stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	if (a == nil) != (b == nil) {
		return false
	}

	for i, v := range a {
		if v != b[i] {
			return false
		}
	}

	return true
}
