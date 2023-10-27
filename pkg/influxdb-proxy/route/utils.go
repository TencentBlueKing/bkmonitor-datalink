// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package route

import (
	"regexp"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/cluster"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/consul"
)

// FormatRoute 将路由拼接，并使用指针传出
func FormatRoute(db string, table string) string {
	return db + "." + table
}

// GetDBNames 获取所有表名，用于show measurements
func GetDBNames() ([]string, error) {
	return consul.GetDBsName()
}

// GetTableNames  获取指定路径下所有表名，用于show measurements
func GetTableNames(db string) ([]string, error) {
	return consul.GetTablesName(db)
}

// GetRouteClusterByName 通过名称获取集群信息
func GetRouteClusterByName(flow uint64, name string) (cluster.Cluster, error) {
	return GetClusterByName(flow, name)
}

// GetRouteCluster 判断哪个集群可以处理指定的数据
func GetRouteCluster(flow uint64, route string) (cluster.Cluster, error) {
	return GetClusterByRoute(flow, route)
}

// RegexpMatch 匹配子字符串
func RegexpMatch(matchExp *regexp.Regexp, s string) []string {
	result := matchExp.FindStringSubmatch(s)
	if len(result) >= 1 {
		return result[1:]
	}
	return []string{}
}

// CheckDBAndTable 检查Db和table是否为空
func CheckDBAndTable(db, table string) error {
	if err := CheckDB(db); err != nil {
		return err
	}
	if err := CheckTable(table); err != nil {
		return err
	}
	return nil
}

// CheckDB 检查DB是否为空
func CheckDB(db string) error {
	if db == "" {
		return ErrMissingDB
	}
	return nil
}

// CheckTable 检查table是否为空
func CheckTable(table string) error {
	if table == "" {
		return ErrMissingTable
	}
	return nil
}

// CheckSingleWord  检查输入的db是否是一个单词，如果中间有空格则是错误输入
func CheckSingleWord(db string) bool {
	// 单个单词匹配
	singleWordExp := regexp.MustCompile(`^\S+$`)
	if db == "" {
		return false
	}
	idx := singleWordExp.FindStringIndex(db)
	// 匹配不上singleWordExp证明不是single word，这时返回false
	if idx == nil {
		return false
	}
	// 匹配成功说明是singleword
	return true
}
