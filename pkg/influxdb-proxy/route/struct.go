// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package route

import (
	"fmt"
	"net/http"
)

// Info :
type Info struct {
	Cluster      string   `json:"cluster"`
	PartitionTag []string `json:"partition_tag"`
}

// Compare 比较二者是否相同
func (t *Info) Compare(val *Info) bool {
	if val.Cluster != t.Cluster {
		return false
	}
	if len(val.PartitionTag) != len(t.PartitionTag) {
		return false
	}
	for index, tag := range val.PartitionTag {
		if tag != t.PartitionTag[index] {
			return false
		}
	}

	return true
}

// ExecuteResult 执行结果返回
type ExecuteResult struct {
	Message string
	Code    int
	Err     error
}

// NewExecuteResult 执行结果返回
func NewExecuteResult(str string, code int, err error) *ExecuteResult {
	return &ExecuteResult{str, code, err}
}

// QueryParams 查询参数
type QueryParams struct {
	DB        string
	SQL       string
	Epoch     string
	Pretty    string
	Chunked   string
	ChunkSize string
	Params    string
	Header    http.Header
	Flow      uint64
}

// NewQueryParams 查询参数
func NewQueryParams(db, sql, epoch, pretty, chunked, chunkSize string, params string, header http.Header, flow uint64) *QueryParams {
	return &QueryParams{db, sql, epoch, pretty, chunked, chunkSize, params, header, flow}
}

func (p *QueryParams) String() string {
	return fmt.Sprintf("%#v", p)
}

// WriteParams 写入参数
type WriteParams struct {
	DB          string
	Precision   string
	Consistency string
	RP          string
	Data        []byte
	Header      http.Header
	Flow        uint64
}

// NewWriteParams 写入参数
func NewWriteParams(db, precision, consistency, rp string, data []byte, header http.Header, flow uint64) *WriteParams {
	return &WriteParams{db, precision, consistency, rp, data, header, flow}
}

func (p *WriteParams) String() string {
	return fmt.Sprintf("%#v", p)
}

// CreateDBParams 建库参数
type CreateDBParams struct {
	DB      string
	Cluster string
	Header  http.Header
	Flow    uint64
}

// NewCreateDBParams 建库参数
func NewCreateDBParams(db, cluster string, header http.Header, flow uint64) *CreateDBParams {
	return &CreateDBParams{db, cluster, header, flow}
}

func (p *CreateDBParams) String() string {
	return fmt.Sprintf("%#v", p)
}
