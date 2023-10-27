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
	"encoding/json"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/logging"
)

const (
	// RouteBasePath 表route基础路径
	RouteBasePath = "router"
)

// :
var (
	RoutePath string
)

func initRoutePath() {
	RoutePath = TotalPrefix + "/" + RouteBasePath
}

// GetRouteInfo 获取单个表的映射信息
var GetRouteInfo = func(db string, table string) (*RouteInfo, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Tracef("called,db:%s,table:%s", db, table)
	path := TotalPrefix + "/" + RouteBasePath + "/" + db + "/" + table
	res, err := consulClient.Get(path)
	if err != nil {
		flowLog.Errorf("get failed")
		return nil, err
	}
	if res == nil {
		flowLog.Errorf("res is nil")
		return nil, ErrPathDismatch
	}

	ti := new(RouteInfo)
	err = json.Unmarshal(res.Value, ti)
	if err != nil {
		flowLog.Errorf("Unmarshal failed,error:%s", err)
		return nil, err
	}
	flowLog.Tracef("done")
	return ti, nil
}

// GetDBsName 获取全部db名称
var GetDBsName = func() ([]string, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Tracef("called")
	paths, err := consulClient.GetChild(TotalPrefix+"/"+RouteBasePath, "/")
	if err != nil {
		flowLog.Errorf("get child failed")
		return nil, err
	}
	list := make([]string, len(paths))
	for index, path := range paths {
		path = strings.Replace(path, TotalPrefix+"/"+RouteBasePath+"/", "", 1)
		list[index] = strings.Split(path, "/")[0]
	}
	flowLog.Tracef("done")
	return list, nil
}

// GetTablesName 获取指定db下的表的名称
var GetTablesName = func(db string) ([]string, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Tracef("called,db:%s", db)
	paths, err := consulClient.GetChild(TotalPrefix+"/"+RouteBasePath+"/"+db, "/")
	if err != nil {
		flowLog.Errorf("get child failed")
		return nil, err
	}
	list := make([]string, len(paths))
	for index, path := range paths {
		path = strings.Replace(path, TotalPrefix+"/"+RouteBasePath+"/"+db+"/", "", 1)
		list[index] = strings.Split(path, "/")[0]
	}
	flowLog.Tracef("done")
	return list, nil
}

// GetAllRoutesData 获取全部表级映射信息
var GetAllRoutesData = func() (map[string]*RouteInfo, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Tracef("called")
	data, err := consulClient.GetPrefix(TotalPrefix+"/"+RouteBasePath, "/")
	if err != nil {
		flowLog.Errorf("GetPrefix failed")
		return nil, err
	}
	RouteMap := make(map[string]*RouteInfo)
	for _, kvPair := range data {

		ti, err := kvToRouteInfo(kvPair)
		if err != nil {
			flowLog.Errorf("kvToRouteInfo failed")
			return nil, err
		}
		route, err := formatRoutePath(kvPair.Key)
		if err != nil {
			flowLog.Errorf("formatRoutePath failed,error:%s", err)
			return nil, err
		}
		RouteMap[route] = ti

	}
	flowLog.Tracef("done")
	return RouteMap, nil
}

func formatRoutePath(path string) (string, error) {
	dbAndTable := strings.Replace(path, RoutePath+"/", "", 1)
	dbAndTable = strings.TrimSuffix(dbAndTable, "/")
	slice := strings.Split(dbAndTable, "/")
	db := slice[0]
	measurement := slice[1]
	route := db + "." + measurement

	return route, nil
}
