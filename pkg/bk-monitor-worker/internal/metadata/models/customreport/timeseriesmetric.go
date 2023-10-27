// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package customreport

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mapx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/slicex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/stringx"
)

//go:generate goqueryset -in timeseriesmetric.go -out qs_tsmetric.go

// TimeSeriesMetric: time series metric model
// gen:qs
type TimeSeriesMetric struct {
	GroupID        uint      `json:"group_id" gorm:"unique"`
	TableID        string    `json:"table_id" gorm:"size:255"`
	FieldID        uint      `json:"field_id" gorm:"primary_key"`
	FieldName      string    `json:"field_name" gorm:"size:255;unique"`
	TagList        string    `json:"tag_list" sql:"type:text"`
	LastModifyTime time.Time `json:"last_modify_time" gorm:"column:last_modify_time"`
	LastIndex      uint      `json:"last_index"`
	Label          string    `json:"label" gorm:"size:255"`
}

// TableName table alias name
func (TimeSeriesMetric) TableName() string {
	return "metadata_timeseriesmetric"
}

// UpdateMetrics: update ts metrics
func (tsm *TimeSeriesMetric) UpdateMetrics(MetricInfoList []map[string]interface{}) error {
	// 判断是否真的存在某个group_id
	var tsGroup TimeSeriesGroup
	dbSession := mysql.GetDBSession()
	qs := NewTimeSeriesGroupQuerySet(dbSession.DB)
	if err := qs.TimeSeriesGroupIDEq(tsm.GroupID).One(&tsGroup); err != nil {
		return fmt.Errorf("timeseriesgroup: %d not exist", tsm.GroupID)
	}

	// 更新 ts metric 和 tag 记录
	for _, metricInfo := range MetricInfoList {
		// 获取tag list
		var tagList []string
		if mapx.IsMapKey("tag_value_list", metricInfo) {
			tagValue := metricInfo["tag_value_list"].(map[string]interface{})
			tagList = mapx.GetMapKeys(tagValue)
		} else {
			tagList = metricInfo["tag_list"].([]string)
		}
		// 必然会追加target这个维度内容
		tagList = append(tagList, "target")

		fieldName := metricInfo["field_name"]
		lastModifyTime := metricInfo["last_modify_time"].(float64)
		lastTime := time.Unix(int64(lastModifyTime), 0)
		lastTimeStr := lastTime.Format(config.TimeLayout)

		tsmRecord := TimeSeriesMetric{
			FieldName:      fieldName.(string),
			GroupID:        tsm.GroupID,
			LastModifyTime: lastTime,
		}
		tsmObj, created, err := tsmRecord.GetOrCreate()
		if err != nil {
			return fmt.Errorf("get or create time series metric error: %v", err)
		}

		// 生成/更新真实表id
		dbName := stringx.SplitStringByDot(tsGroup.TableID)[0]
		// 处理 tag list
		var objTagList []string
		json.Unmarshal([]byte(dbName), &objTagList)
		oldTagList := slicex.StringList2Set(objTagList)
		newTagList := slicex.StringList2Set(tagList)
		resultTagList := oldTagList.Union(newTagList)
		// 修改已有的配置, 但是考虑需要保持已有的维度，需要将新旧两个维度merge
		if created || lastTimeStr > tsmObj.LastModifyTime.String() {
			qs := NewTimeSeriesMetricQuerySet(dbSession.DB)
			updater := qs.GetUpdater().CustomSetTagList(
				slicex.StringSet2List(resultTagList),
			).SetLastModifyTime(lastTime).SetTableID(
				fmt.Sprintf("%s.%s", dbName, tsmObj.FieldName),
			)
			if err := updater.Update(); err != nil {
				return fmt.Errorf("update ts metric error: %v", err)
			}
		}
	}
	return nil
}

// GetOrCreate retrieve or create a record, and return the record info
func (tsm *TimeSeriesMetric) GetOrCreate() (*TimeSeriesMetric, bool, error) {
	dbSession := mysql.GetDBSession()
	qs := NewTimeSeriesMetricQuerySet(dbSession.DB)
	qs = qs.FieldNameEq(tsm.FieldName).GroupIDEq(tsm.GroupID)

	var tsmRecord TimeSeriesMetric
	created := false
	if err := qs.One(&tsmRecord); err != nil {
		created = true
		// create a record
		if err := tsm.Create(dbSession.DB); err != nil {
			return nil, created, err
		}

		// 查询数据，然后返回
		if err := qs.One(&tsmRecord); err != nil {
			return nil, created, err
		}
	}

	return &tsmRecord, created, nil
}
