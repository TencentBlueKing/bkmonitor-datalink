// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package shard

import (
	"context"
	"fmt"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/log"
)

type StatusCode int

const (
	Move StatusCode = iota + 1
	Rebuild
	Finish
	Delete
	Archived
	Discard
)

var codeName = map[StatusCode]string{
	Move:     "move",
	Rebuild:  "rebuild",
	Finish:   "finish",
	Delete:   "delete",
	Archived: "archive",
	Discard:  "discard",
}

type Meta struct {
	ClusterName     string `json:"cluster_name"`
	Database        string `json:"database"`
	RetentionPolicy string `json:"retention_policy"`
	TagRouter       string `json:"tag_router"`
}

type Instance struct {
	InstanceType string `json:"instance_type"`
	Name         string `json:"name"`
	ShardID      string `json:"shard_id"`
	Path         string `json:"path"`
}

type Spec struct {
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
	Expired time.Time `json:"expired"`

	Source Instance `json:"source"`
	Target Instance `json:"target"`
	Final  Instance `json:"final"`
}

type Status struct {
	Code    StatusCode `json:"code"`
	Message string     `json:"message"`
}

type Shard struct {
	Ctx context.Context `json:"-"`
	Log log.Logger      `json:"-"`

	Meta   Meta   `json:"meta"`
	Spec   Spec   `json:"spec"`
	Status Status `json:"status"`
}

func (m *Meta) String() string {
	k := fmt.Sprintf(
		"%s|%s|%s|%s",
		m.ClusterName, m.TagRouter, m.Database, m.RetentionPolicy,
	)
	return k
}

// RawDataInfo 是调用influxdb query 接口返回的数据格式
type RawDataInfo struct {
	Results []*Result `json:"results"`
}

type Series struct {
	Name    string          `json:"name"`
	Columns []string        `json:"columns"`
	Values  [][]interface{} `json:"values"`
}

type Result struct {
	StatementID int       `json:"statement_id"`
	Series      []*Series `json:"series"`
}

type Info struct {
	Results []*Result `json:"results"`
}

// SimpleShard 封装的简单shard到元信息
type SimpleShard struct {
	ShardID         float64
	Database        string
	RetentionPolicy string
	Start           time.Time
	End             time.Time
	Expired         time.Time
}
