// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package transport

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	client "github.com/influxdata/influxdb1-client/v2"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/logging"
)

// GetClient 根据参数获取influxdb实例
func GetClient(address, username, password string) (client.Client, error) {
	clientItem, err := client.NewHTTPClient(client.HTTPConfig{
		Addr:               address,
		Username:           username,
		Password:           password,
		InsecureSkipVerify: true,
	})
	if err != nil {
		return nil, err
	}
	return clientItem, nil
}

func combineQuerySQL(db, measurement string, tags common.Tags, order string, limit int, start, end int64) string {
	sql := fmt.Sprintf("select * from %s where ", measurement)
	for _, tag := range tags {
		sql = fmt.Sprintf(sql+"%s='%s' ", tag.Key, tag.Value)
	}

	if start != 0 {
		sql = fmt.Sprintf(sql+"and time > %d ", time.Unix(start, 0).UnixNano())
	}
	if end != 0 {
		sql = fmt.Sprintf(sql+"and time < %d ", time.Unix(end, 0).UnixNano())
	}
	if order != "" {
		sql = fmt.Sprintf(sql+"order by time %s ", order)
	}
	if limit != 0 {
		sql = fmt.Sprintf(sql+"limit %d ", limit)
	}

	return sql
}

// getFirstColumns 过去该result的第一列数据集合，可以用来解析tag/field key
// int值用于记录column的index
func getFirstColumns(result client.Result) map[string]int {
	series := result.Series[0]
	values := series.Values
	columns := make(map[string]int)
	for _, value := range values {
		columns[value[0].(string)] = -1
	}
	return columns
}

func getTimestamp(result client.Result) []interface{} {
	series := result.Series[0]
	values := series.Values
	timestamps := make([]interface{}, 0)
	for _, value := range values {
		timestamps = append(timestamps, value[0])
	}
	return timestamps
}

// QueryTimestamp 查询时间戳序列,获取最早的时间戳
func QueryTimestamp(db, measurement string, tags common.Tags, backend client.Client) (int64, error) {
	querySQL := combineQuerySQL(db, measurement, tags, "asc", 10, 0, 0)
	params1 := client.NewQuery(querySQL, db, "s")
	// 查询tag字段名
	resp, err := backend.Query(params1)
	if err != nil {
		return 0, err
	}

	result := resp.Results[0]
	values := getTimestamp(result)
	if len(values) < 1 {
		return 0, ErrConvertTimestampFailed
	}
	timestampNumber := values[0].(json.Number)
	timestamp, err := timestampNumber.Int64()
	if err != nil {
		return 0, err
	}
	return timestamp, nil
}

func hasData(results []client.Result) bool {
	if len(results) == 0 || len(results[0].Series) == 0 ||
		len(results[0].Series[0].Columns) == 0 || len(results[0].Series[0].Values) == 0 {
		return false
	}
	return true
}

func (t *Transport) GetParams(db, measurement string, tags common.Tags, start, end int64) (client.Query, client.Query, client.Query) {
	sql1 := fmt.Sprintf("show tag keys from %s", measurement)
	sql2 := fmt.Sprintf("show field keys from %s", measurement)
	params1 := client.NewQuery(sql1, db, "ns")
	params2 := client.NewQuery(sql2, db, "ns")

	querySQL := combineQuerySQL(db, measurement, tags, "asc", 0, start, end)
	params3 := client.NewQuery(querySQL, db, "ns")

	return params1, params2, params3
}

func (t *Transport) queryTagOrField(backend client.Client, params client.Query, logger *logging.Entry, ignoreErrors bool) (*client.Result, error) {
	resp, err := backend.Query(params)
	if err != nil {
		logger.Errorf("query tag or field name failed,error:%s", err)
		return nil, err
	}

	if !hasData(resp.Results) {
		logger.Error("got empty tag or field data")
		if ignoreErrors {
			return nil, nil
		}
		return nil, ErrQueryTagFailed
	}
	result := resp.Results[0]
	return &result, nil
}

// queryClientToWriteData 查询数据，并以write的数据格式返回
func (t *Transport) queryClientToWriteData(db, measurement string, tags common.Tags, start, end int64, backend client.Client, ch chan<- client.BatchPoints) error {
	logger := logging.NewEntry(map[string]interface{}{
		"modules":     moduleName,
		"db":          db,
		"measurement": measurement,
		"start":       start,
		"end":         end,
	})
	params1, params2, params3 := t.GetParams(db, measurement, tags, start, end)
	// 查询tag字段名
	resultTag, err := t.queryTagOrField(backend, params1, logger, false)
	if err != nil {
		return err
	}
	tagColumns := getFirstColumns(*resultTag)
	resultField, err := t.queryTagOrField(backend, params2, logger, false)
	if err != nil {
		return err
	}
	fieldColumns := getFirstColumns(*resultField)
	logger.Debugf("get unformatted tag index:%v", tagColumns)
	logger.Debugf("get unformatted field index:%v", fieldColumns)

	// 真实查询数据
	result, err := t.queryTagOrField(backend, params3, logger, true)
	// 对比列，获取所有的tag和field定位
	columns := result.Series[0].Columns
	logger.Debugf("get columns from data:%v", columns)
	for index, column := range columns {
		if _, ok := tagColumns[column]; ok {
			tagColumns[column] = index
		}
		if _, ok := fieldColumns[column]; ok {
			fieldColumns[column] = index
		}
	}
	logger.Debugf("get formatted tag index:%v", tagColumns)
	logger.Debugf("get formatted field index:%v", fieldColumns)
	batchPoints, err := client.NewBatchPoints(
		client.BatchPointsConfig{
			Database:         db,
			Precision:        "ns",
			WriteConsistency: "",
			RetentionPolicy:  "",
		},
	)
	if err != nil {
		logger.Errorf("new batch points failed,error:%s", err)
		return err
	}
	count := 0
	queryNum := len(result.Series[0].Values)
	logger.Debugf("get %d lines", queryNum)
	// 查询超过预期值则直接报错退出，由上层决定如何处理
	if queryNum > t.maxQueryLines {
		logger.Errorf("get too much data from one query,lines:%d", queryNum)
		return ErrQueryOverflow
	}
	// 根据上面获取的tag定位，将所有数据的tag和field分离，形成points
	for _, value := range result.Series[0].Values {
		tagSets := make(map[string]string)
		for name, index := range tagColumns {
			if index == -1 {
				logger.Warnf("tag field not found,name:%s,data:%v", name, value)
				continue
			}
			tagValue, ok := value[index].(string)
			if !ok {
				logger.Warnf("get nil tag,name:%s", name)
				continue
			}
			tagSets[name] = tagValue
		}

		fieldSets := make(map[string]interface{})
		for name, index := range fieldColumns {
			if index == -1 {
				logger.Warnf("metric field not found,name:%s,data:%v", name, value)
				continue
			}
			num, err := getFloatValue(value[index])
			if err != nil {
				logger.Warnf("get metric failed,data:%v,error:%s", value, err)
				continue
			}
			fieldSets[name] = num
		}

		// select * 语句的第一列默认是time
		timestampInt, err := getIntValue(value[0])
		if err != nil {
			logger.Warnf("get timestamp failed,error:%s,data:%v", err, value)
			continue
		}
		timeStamp := time.Unix(0, timestampInt)

		point, err := client.NewPoint(measurement, tagSets, fieldSets, timeStamp)
		if err != nil {
			logger.Errorf("new point failed,error:%s", err)
			continue
		}
		// logger.Debugf("append point:%s\n", point.String())
		batchPoints.AddPoint(point)
		count++
		if count >= t.writeBatchSize {
			count = 0
			ch <- batchPoints
			batchPoints, err = client.NewBatchPoints(
				client.BatchPointsConfig{
					Database:         db,
					Precision:        "ns",
					WriteConsistency: "",
					RetentionPolicy:  "",
				},
			)
			if err != nil {
				logger.Errorf("new batch points failed,error:%s", err)
				return err
			}
		}

	}
	ch <- batchPoints

	return nil
}

func getIntValue(val interface{}) (int64, error) {
	logger := logging.NewEntry(map[string]interface{}{
		"modules": moduleName,
	})
	switch res := val.(type) {
	case nil:
		return 0, ErrTypeIsNil
	case string:
		return strconv.ParseInt(res, 10, 64)
	case json.Number:
		return res.Int64()
	default:
		logger.Warnf("val:%v,change to number failed", val)
		return 0, ErrConvertTypeFailed
	}
}

func getFloatValue(val interface{}) (float64, error) {
	logger := logging.NewEntry(map[string]interface{}{
		"modules": moduleName,
	})
	switch res := val.(type) {
	case nil:
		return 0, ErrTypeIsNil
	case string:
		return strconv.ParseFloat(res, 64)
	case json.Number:
		return res.Float64()
	default:
		logger.Warnf("val:%v,change to number failed", val)
		return 0, ErrConvertTypeFailed
	}
}

// WriteClient 向目标机器写入
func WriteClient(db string, points client.BatchPoints, backend client.Client) error {
	return backend.Write(points)
}
