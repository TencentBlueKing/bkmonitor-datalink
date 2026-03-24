// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

import (
	"time"

	client "github.com/influxdata/influxdb1-client/v2"
	"github.com/pkg/errors"
)

// GetClient 根据参数获取influxdb实例
func GetClient(address, username, password string, timeout int) (client.Client, error) {
	clientItem, err := client.NewHTTPClient(client.HTTPConfig{
		Addr:     address,
		Username: username,
		Password: password,
		Timeout:  time.Duration(timeout) * time.Second,
	})
	if err != nil {
		return nil, err
	}
	_, _, err = clientItem.Ping(time.Duration(timeout) * time.Second)
	if err != nil {
		return nil, errors.Wrapf(err, "ping indluxdb [%s] failed", address)
	}
	return clientItem, nil
}

// QueryDB convenience function to query the database
func QueryDB(clnt client.Client, cmd string, database string, params map[string]any) (res []client.Result, err error) {
	q := client.Query{
		Command:    cmd,
		Database:   database,
		Parameters: params,
	}
	if response, err := clnt.Query(q); err == nil {
		if response.Error() != nil {
			return res, response.Error()
		}
		res = response.Results
	} else {
		return res, err
	}
	return res, nil
}

func ParseResult(result client.Result) []map[string]any {
	dataList := make([]map[string]any, 0)
	for _, series := range result.Series {
		for _, row := range series.Values {
			data := make(map[string]any)
			for i, colName := range series.Columns {
				data[colName] = row[i]
			}
			dataList = append(dataList, data)
		}
	}
	return dataList
}
