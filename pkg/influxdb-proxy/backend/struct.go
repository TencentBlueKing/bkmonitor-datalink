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
	"fmt"
	"time"
)

// BasicConfig backend基础配置
type BasicConfig struct {
	Name     string
	Address  string
	Port     int
	Username string
	Password string
	Protocol string

	Disabled        bool
	BackupRateLimit float64

	ForceBackup bool
	IgnoreKafka bool

	Auth    Auth
	Timeout time.Duration
}

// MakeBasicConfig : 创建一个新的后台配置, 传入的参数是已经通过Configure拿到的配置Map
func MakeBasicConfig(name string, host *Info, forceBackup bool, ignoreKafka bool, timeout time.Duration) *BasicConfig {
	cfg := &BasicConfig{
		Name:            name,
		Address:         host.DomainName,
		Port:            host.Port,
		Username:        host.Username,
		Password:        host.Password,
		Protocol:        host.Protocol,
		Disabled:        host.Disabled,
		BackupRateLimit: host.BackupRateLimit,
		ForceBackup:     forceBackup,
		IgnoreKafka:     ignoreKafka,
		Timeout:         timeout,
		Auth:            NewBasicAuth(host.Username, host.Password),
	}
	return cfg
}

// GetBasicAuth 获取基础认证接口
func (bc *BasicConfig) GetBasicAuth() Auth {
	return NewBasicAuth(bc.Username, bc.Password)
}

// QueryParams backend使用的查询参数
type QueryParams struct {
	DB        string `json:"db"`
	SQL       string `json:"q"`
	Epoch     string `json:"epoch"`
	Pretty    string `json:"pretty"`
	Chunked   string `json:"chunked"`
	ChunkSize string `json:"chunk_size"`
}

// NewQueryParams :
func NewQueryParams(db, sql, epoch, pretty, chunked, chunkSize string) *QueryParams {
	return &QueryParams{db, sql, epoch, pretty, chunked, chunkSize}
}

func (q *QueryParams) String() string {
	return fmt.Sprintf("db:%s sql:%s epoch:%s pretty:%s chunked:%s chunk_size:%s", q.DB, q.SQL, q.Epoch, q.Pretty, q.Chunked, q.ChunkSize)
}

// WriteParams backend使用的写入参数
type WriteParams struct {
	DB          string `json:"db"`
	Consistency string `json:"consistency"`
	Precision   string `json:"precision"`
	RP          string `json:"rp"`
}

// NewWriteParams :
func NewWriteParams(db, consistency, precision, rp string) *WriteParams {
	return &WriteParams{db, consistency, precision, rp}
}

func (w *WriteParams) String() string {
	return fmt.Sprintf("db:%s consistency:%s precision:%s rp:%s", w.DB, w.Consistency, w.Precision, w.RP)
}

// Response backend的返回信息
type Response struct {
	Result string
	Code   int
}

func (br *Response) String() string {
	return fmt.Sprintf("result:%s,code:%d", br.Result, br.Code)
}

// NewResponse :
func NewResponse(result string, code int) *Response {
	return &Response{result, code}
}

// Info 从consul获取的主机信息
type Info struct {
	DomainName string `json:"domain_name"`
	Port       int    `json:"port"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	Protocol   string `json:"protocol"`
	// 兼容默认值为 false 需要保持开启，所以用反状态
	Disabled        bool    `json:"status,omitempty"`
	BackupRateLimit float64 `json:"backup_rate_limit,omitempty"`
}

// Compare 比较二者是否相同
func (h *Info) Compare(val *Info) bool {
	if val == nil {
		return false
	}

	if val.Username == h.Username && val.Password == h.Password && val.DomainName == h.DomainName && val.Port == h.Port && val.Disabled == h.Disabled && val.BackupRateLimit == h.BackupRateLimit {
		return true
	}

	return false
}

// Status backend的状态信息集合
type Status struct {
	Read         bool
	Write        bool
	InnerWrite   bool
	InvalidCount int64
	Recovery     bool
	UpdateTime   int64
}

func (s Status) String() string {
	return fmt.Sprintf("read:%t write:%t innerWrite:%t invalidCount:%d recovery:%t updateTime:%d", s.Read, s.Write, s.InnerWrite, s.InvalidCount, s.Recovery, s.UpdateTime)
}
