// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package structured

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
)

// mergeFieldTag
func mergeFieldTag(fields, tags []string) string {
	var s bytes.Buffer
	for i := 0; i < len(fields); i++ {
		s.WriteString(fmt.Sprintf("%s::field", fields[i]))
		if i != len(fields)-1 {
			s.WriteString(", ")
		}
	}

	if len(tags) > 0 {
		s.WriteString(", ")
	}

	for i := 0; i < len(tags); i++ {
		s.WriteString(fmt.Sprintf("%s::tag", tags[i]))
		if i != len(tags)-1 {
			s.WriteString(", ")
		}
	}
	return s.String()
}

// MakeInfluxdbQueryByStruct
func MakeInfluxdbQueryByStruct(
	queries *CombinedQueryParams, getMaxPoints func() int64, getChunked func() bool,
) ([]influxdb.SQLInfo, error) {
	var sqlInfos []influxdb.SQLInfo
	for _, query := range queries.QueryList {
		var displayCol string
		var whereList = promql.NewWhereList(false)
		var displayFields, displayTags []string
		var sqlInfo influxdb.SQLInfo
		var measurement string

		dbMeasurement := strings.Split(string(query.TableID), ".")
		if len(dbMeasurement) != 2 {
			return sqlInfos, errors.New("invalid database or measurement")
		}
		sqlInfo.DB = dbMeasurement[0]
		measurement = dbMeasurement[1]

		if len(query.FieldList) <= 0 {
			return sqlInfos, errors.New("empty filed name")
		}

		// field 支持逗号分割
		for _, field := range query.FieldList {
			displayFields = append(displayFields, string(field))
		}
		for _, tag := range query.KeepColumns {
			displayTags = append(displayTags, tag)
		}

		displayCol = mergeFieldTag(displayFields, displayTags)
		if query.IsFreeSchema {
			whereList.Append(promql.AndOperator, promql.NewWhere(
				promql.StaticMetricName,
				string(query.FieldName),
				promql.EqualOperator,
				promql.StringType,
			))
			displayCol = promql.StaticMetricValue
		}

		fieldsCount := len(query.Conditions.FieldList)
		if fieldsCount > 0 && len(query.Conditions.ConditionList)+1 != fieldsCount {
			return sqlInfos, errors.New("invalid condition list")
		}
		// 第一个查询条件为 and
		query.Conditions.ConditionList = append([]string{"and"}, query.Conditions.ConditionList...)

		for idx, cond := range query.Conditions.FieldList {
			if len(cond.Value) <= 0 {
				continue
			}
			// 正则优化 有可能改变 Operator/Value 值
			cf := &ConditionField{
				DimensionName: cond.DimensionName,
				Value:         cond.Value,
				Operator:      cond.Operator,
			}
			cf = cf.ContainsToPromReg()

			valueType := promql.RegexpType
			switch cf.Operator {
			case ConditionEqual, ConditionNotEqual:
				valueType = promql.StringType
			}

			operator, ok := promql.PromqlOperatorMapping[cf.ToPromOperator()]
			if !ok {
				continue
			}
			whereList.Append(
				query.Conditions.ConditionList[idx],
				promql.NewWhere(cf.DimensionName, cf.Value[0], operator, valueType),
			)
		}

		st, err := strconv.Atoi(query.Start)
		if err != nil {
			return sqlInfos, fmt.Errorf("invalid start timestamp: %v", query.Start)
		}
		et, err := strconv.Atoi(query.End)
		if err != nil {
			return sqlInfos, fmt.Errorf("invalid end timestamp: %v", query.End)
		}

		// ns 单位
		start := strconv.FormatInt(int64(st)*1000000000, 10)
		stop := strconv.FormatInt(int64(et)*1000000000, 10)
		whereList.Append(promql.AndOperator, promql.NewWhere("time", start, promql.UpperEqualOperator, promql.NumType))
		whereList.Append(promql.AndOperator, promql.NewWhere("time", stop, promql.LowerOperator, promql.NumType))

		limit := int(query.Limit)
		maxPoints := getMaxPoints()
		chunk := getChunked()

		// 非chunk的情况下才需要对最大值进行限制
		if !chunk {
			// 选择二者中的小值，且limit不能为0
			if limit == 0 || limit > int(maxPoints) {
				limit = int(maxPoints)
			}
		}

		limitStr := ""
		if limit > 0 {
			limitStr = fmt.Sprintf(" limit %d", limit)
		}

		// 增加默认过滤span_id和trace_id不能同时为空
		sqlInfo.SQL = fmt.Sprintf(
			`select %s, time as _time from %s where %s and (bk_span_id != "" or bk_trace_id != "")%s`,
			displayCol, measurement, whereList.String(), limitStr,
		)
		if err = influxdb.CheckSQLInject(sqlInfo.SQL); err != nil {
			return sqlInfos, fmt.Errorf("invalid influxdb sql: %s", err.Error())
		}
		sqlInfos = append(sqlInfos, sqlInfo)
	}

	return sqlInfos, nil
}
