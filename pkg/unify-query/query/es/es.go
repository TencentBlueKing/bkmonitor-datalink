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
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/es"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
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
	// 是否模糊匹配，该参数开启时，按旧版get_es_data的查询逻辑进行查询
	// 开启模糊匹配后，start和end将失效，查询的index将直接传入
	// 关闭模糊匹配时，查询模块将根据format、index、start、end自动生成一系列查询别名传入到查询请求中
	FuzzyMatching bool
}

// Query 查询数据，将结果以json格式返回
func Query(q *Params) (string, error) {
	info, err := es.GetStorageID(q.TableID)
	if err != nil {
		return "", metadata.Sprintf(
			metadata.MsgQueryES,
			"ES 获取数据库失败 %v",
			q.TableID,
		).Error(context.TODO(), err)
	}
	aliases := formatAliases(info, q)
	if len(aliases) == 0 {
		return "", metadata.Sprintf(
			metadata.MsgQueryES,
			"ES 查询失败 %v",
			q.TableID,
		).Error(context.TODO(), ErrNoAliases)
	}
	return es.SearchByStorage(info.StorageID, q.Body, aliases)
}

// 根据格式处理成对应的alias
func formatAliases(info *es.TableInfo, q *Params) []string {
	// 启动模糊匹配时，直接传入index即可
	if q.FuzzyMatching {
		log.Debugf(context.TODO(), "query %#v use fuzzy matching", q)
		return []string{es.ConvertTableIDToFuzzyIndexName(q.TableID)}
	}
	// 否则根据format规则生成一系列别名查询
	appendTimeList := make([]string, 0)
	start := time.Unix(q.Start, 0)
	end := time.Unix(q.End, 0)
	temp := start
	// 去重
	lastDate := ""
	for temp.Before(end) {
		appendTime := temp.Format(info.DateFormat)
		if lastDate != appendTime {
			appendTimeList = append(appendTimeList, appendTime)
			lastDate = appendTime
		}
		temp = temp.Add(time.Duration(info.DateStep) * time.Hour)
	}
	result := make([]string, 0)
	for _, appendTime := range appendTimeList {
		alias := strings.Replace(info.AliasFormat, "{index}", es.ConvertTableIDToIndexName(q.TableID), -1)
		alias = strings.Replace(alias, "{time}", appendTime, -1)
		if es.AliasExist(q.TableID, alias) {
			result = append(result, alias)
		}
	}
	log.Debugf(context.TODO(), "query %#v get aliases:%v", q, result)
	return result
}
