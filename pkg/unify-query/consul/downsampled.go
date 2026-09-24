// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consul

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/hashicorp/consul/api"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/utils"
)

const (
	Cq = "cq"
)

var (
	downsampledPath     = "downsampled"
	rwMutex             = new(sync.RWMutex)
	DownsampledInfo     = new(Info)
	downsampledInfoHash string
)

// Info RP 和 CQ 配置信息，用于降精度
type Info struct {
	Databases          map[string]DownsampledDatabase
	RetentionPolicies  map[string]DownsampledRetentionPolicy
	DBMeasurementRPMap map[string]DownsampledRetentionPolicy // db.measurement: rp_name
	ContinuousQueries  map[string][]DownsampledContinuousQuery
	Measurements       map[string]map[string]struct{}
}

// DownsampledDatabase 降精度 database 配置
type DownsampledDatabase struct {
	Database       string
	TagName        string   `json:"tag_name"`
	TagValue       []string `json:"tag_value"`
	Enable         bool     `json:"enable"`
	LastModifyTime string   `json:"last_modify_time"`
}

// Check comment 判定 database 是否开启降精度
func (d *DownsampledDatabase) Check(keys []string) bool {
	if len(keys) == 2 && keys[1] == Cq {
		d.Database = keys[0]
		return true
	}
	return false
}

// Key 获取 database 唯一键
func (d *DownsampledDatabase) Key() string {
	return d.Database
}

// DownsampledRetentionPolicy RP 配置信息
type DownsampledRetentionPolicy struct {
	Database       string
	RpName         string
	Measurement    string `json:"measurement"`
	Duration       string `json:"duration"`
	Resolution     int64  `json:"resolution"`
	LastModifyTime string `json:"last_modify_time"`
}

// Check 判定 RP 是否开启
func (d *DownsampledRetentionPolicy) Check(keys []string) bool {
	if len(keys) == 3 && keys[1] == "rp" {
		d.Database = keys[0]
		d.RpName = keys[2]
		return true
	}
	return false
}

// Key 获取 rp 唯一键
func (d *DownsampledRetentionPolicy) Key() string {
	return fmt.Sprintf("%s/%s", d.Database, d.RpName)
}

// TableIDKey 获取 TableID 唯一键
func (d *DownsampledRetentionPolicy) TableIDKey() string {
	return fmt.Sprintf("%s.%s", d.Database, d.Measurement)
}

// DownsampledContinuousQuery 降精度字段、聚合函数、RP 信息
type DownsampledContinuousQuery struct {
	Database       string
	Measurement    string
	Field          string
	Aggregation    string
	RpName         string
	SourceRp       string `json:"source_rp"`
	LastModifyTime string `json:"last_modify_time"`
}

// Check 判断 字段，聚合函数，RP 是否开启降精度
func (d *DownsampledContinuousQuery) Check(keys []string) bool {
	if len(keys) == 6 && keys[1] == Cq {
		d.Database = keys[0]
		d.Measurement = keys[2]
		d.Field = keys[3]
		d.Aggregation = keys[4]
		d.RpName = keys[5]
		return true
	}
	return false
}

// Key 唯一判定键
func (d *DownsampledContinuousQuery) Key() string {
	return fmt.Sprintf("%s/%s/%s/%s", d.Database, d.Measurement, d.Field, d.Aggregation)
}

// client consul 信息解析实例
type client struct {
	prefixPath string

	*Info
}

// load 加载 consul 配置信息
func (c *client) load() error {
	pairs, err := GetDataWithPrefix(c.prefixPath)
	if err != nil {
		return err
	}
	err = c.format(pairs)
	return err
}

// format consul 信息格式化
func (c *client) format(kvPairs api.KVPairs) error {
	for _, kvPair := range kvPairs {
		var db DownsampledDatabase
		var rp DownsampledRetentionPolicy
		var cq DownsampledContinuousQuery
		var err error
		var key string
		keys := strings.Split(strings.ReplaceAll(kvPair.Key, c.prefixPath, ""), "/")

		if len(keys) > 1 {
			switch {
			// downsampledDatabase {database}/cq
			case db.Check(keys):
				key = db.Database
				err = json.Unmarshal(kvPair.Value, &db)
				c.Databases[db.Key()] = db
			// downsampledRetentionPolicy {database}/rp/{rp_name}
			case rp.Check(keys):
				err = json.Unmarshal(kvPair.Value, &rp)
				c.RetentionPolicies[rp.Key()] = rp
				if rp.Measurement != "" {
					c.DBMeasurementRPMap[rp.TableIDKey()] = rp
				}
			// downsampledContinuousQuery {database}/cq/{measurement}/{field}/{aggregation}/{rp_name}
			case cq.Check(keys):
				// downsampledContinuousQuery
				err = json.Unmarshal(kvPair.Value, &cq)
				if _, ok := c.Measurements[cq.Database]; !ok {
					c.Measurements[cq.Database] = make(map[string]struct{}, 0)
				}
				c.Measurements[cq.Database][cq.Measurement] = struct{}{}
				if _, ok := c.ContinuousQueries[cq.Key()]; !ok {
					c.ContinuousQueries[cq.Key()] = make([]DownsampledContinuousQuery, 0)
				}
				c.ContinuousQueries[cq.Key()] = append(c.ContinuousQueries[cq.Key()], cq)
			default:
				err = fmt.Errorf("downsampled key值异常， %s", key)
				return err
			}
			if err != nil {
				return err
			}
			log.Debugf(context.TODO(), "load consul %s: %s", key, kvPair.Value)
		} else {
			err = fmt.Errorf("keys error: %s", key)
			return err
		}
	}

	return nil
}

// consulPath consul 存放路径
func consulPath() string {
	return fmt.Sprintf("%s/%s/", MetadataPath, downsampledPath)
}

// LoadDownsampledInfo 从consul获取downsampled信息
func LoadDownsampledInfo() error {
	c := &client{
		prefixPath: consulPath(),
		Info: &Info{
			Databases:          make(map[string]DownsampledDatabase),
			RetentionPolicies:  make(map[string]DownsampledRetentionPolicy),
			DBMeasurementRPMap: make(map[string]DownsampledRetentionPolicy),
			ContinuousQueries:  make(map[string][]DownsampledContinuousQuery),
			Measurements:       make(map[string]map[string]struct{}),
		},
	}
	err := c.load()

	newInfoHash := utils.HashIt(*c.Info)
	if downsampledInfoHash == newInfoHash {
		return nil
	}

	// 写入缓存
	log.Debugf(context.TODO(), "set downsampledInfo %v", c.Info)

	rwMutex.Lock()
	defer rwMutex.Unlock()
	DownsampledInfo = c.Info
	downsampledInfoHash = newInfoHash
	return err
}

// WatchDownsampledInfo 监听 consul 配置信息是否变化
func WatchDownsampledInfo(ctx context.Context) (<-chan any, error) {
	return WatchChange(ctx, consulPath())
}
