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

	"github.com/jinzhu/gorm"
)

// CustomSetTagList set tag list value
func (u TimeSeriesMetricUpdater) CustomSetTagList(tagList []string) TimeSeriesMetricUpdater {
	jsonTagList, _ := json.Marshal(tagList)
	u.fields["tag_list"] = jsonTagList
	return u
}

// CustomUpdate update fields, and support tag list value
func (o *TimeSeriesMetric) CustomUpdate(db *gorm.DB, fields ...TimeSeriesMetricDBSchemaField) error {
	dbNameToFieldName := map[string]interface{}{
		"group_id":         o.GroupID,
		"table_id":         o.TableID,
		"field_id":         o.FieldID,
		"field_name":       o.FieldName,
		"tag_list":         o.TagList,
		"last_modify_time": o.LastModifyTime,
	}
	u := map[string]interface{}{}
	for _, f := range fields {
		fs := f.String()
		u[fs] = dbNameToFieldName[fs]
	}
	if err := db.Model(o).Updates(u).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return err
		}

		return fmt.Errorf("can't update TimeSeriesMetric %v fields %v: %s",
			o, fields, err)
	}

	return nil
}
