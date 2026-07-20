// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package es

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/es"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

const (
	consulAliasFormat = "{index}_{time}_read"
	consulDateFormat  = "20060102"
)

// Params 查询传入参数
type Params struct {
	// 用于查找对应存储的唯一id
	TableID string
	// 查询参数载体
	Body string
	// 查询时间起点
	Start int64
	// 查询时间终点
	End int64
	// 是否模糊匹配。开启时会根据查询时间生成带日期的索引通配符。
	// 关闭时根据 format、index、start、end 生成查询别名。
	FuzzyMatching bool
}

// Query 查询数据，将结果以json格式返回
func Query(ctx context.Context, q *Params) (string, error) {
	if err := validateTimeRange(q); err != nil {
		return "", err
	}

	info, err := es.GetStorageID(q.TableID)
	if err != nil {
		log.Errorf(ctx, "get storage id by table id:%s failed,error:%s", q.TableID, err)
		return "", err
	}
	targets, err := formatQueryTargets(info, q)
	if err != nil {
		return "", err
	}
	if len(targets) == 0 {
		log.Errorf(ctx, "no es query target found by query:%#v", q)
		return "", ErrNoAliases
	}
	return es.SearchByStorage(ctx, info.StorageID, q.Body, targets)
}

func validateTimeRange(q *Params) error {
	if q == nil || q.End <= q.Start {
		return ErrInvalidTimeRange
	}

	maxTimeRange := maxQueryTimeRange()
	start := time.Unix(q.Start, 0)
	end := time.Unix(q.End, 0)
	if end.Sub(start) > maxTimeRange {
		return fmt.Errorf("%w: maximum is %s", ErrTimeRangeTooLarge, maxTimeRange)
	}
	return nil
}

// 根据格式处理成对应的 alias 或索引通配符。
func formatQueryTargets(info *es.TableInfo, q *Params) ([]string, error) {
	if info == nil || info.DateFormat != consulDateFormat {
		return nil, ErrInvalidDateFormat
	}

	// 3.8 ES 元数据默认按 UTC 创建索引和别名；旧 Consul 协议没有下发自定义时区。
	start := time.Unix(q.Start, 0).UTC()
	end := time.Unix(q.End, 0).UTC()
	appendTimeList := make([]string, 0)
	appendTimeSet := make(map[string]struct{})
	startDate := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location())
	endDate := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, end.Location())
	for current := startDate; !current.After(endDate); current = current.AddDate(0, 0, 1) {
		date := current.Format(info.DateFormat)
		if _, ok := appendTimeSet[date]; ok {
			continue
		}
		appendTimeSet[date] = struct{}{}
		appendTimeList = append(appendTimeList, date)
	}

	result := make([]string, 0)
	indexName := es.ConvertTableIDToIndexName(q.TableID)
	if !q.FuzzyMatching && info.AliasFormat != consulAliasFormat {
		return nil, ErrInvalidAliasFormat
	}
	for _, date := range appendTimeList {
		if q.FuzzyMatching {
			// 兼容 v1/v2 物理索引，同时用表名前缀边界避免匹配到其他结果表。
			result = append(result,
				fmt.Sprintf("%s_%s*", indexName, date),
				fmt.Sprintf("v2_%s_%s*", indexName, date),
			)
			continue
		}
		alias := strings.Replace(info.AliasFormat, "{index}", indexName, -1)
		// Consul 只下发日格式，实际 read alias 可按小时创建，因此日期后保留受限通配符。
		alias = strings.Replace(alias, "{time}", date+"*", -1)
		result = append(result, alias)
	}
	log.Debugf(context.TODO(), "query %#v get aliases:%v", q, result)
	return result, nil
}
