// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxql

import (
	"fmt"

	"github.com/influxdata/influxql"
)

// DataSource :
type DataSource struct {
	db        string
	retention string
	table     string
}

func newDataSource(db string, retention string, table string) *DataSource {
	return &DataSource{db, retention, table}
}

// GetDB ;
func (s *DataSource) GetDB() string {
	return s.db
}

// GetRetention :
func (s *DataSource) GetRetention() string {
	return s.retention
}

// GetTable :
func (s *DataSource) GetTable() string {
	return s.table
}

// String :
func (s *DataSource) String() string {
	return fmt.Sprintf("db:%s,table:%s", s.db, s.table)
}

func makeDataSource(preDB string, measurements influxql.Measurements) []*DataSource {
	// 如果measurements为0，手动制造一个datasource返回
	if len(measurements) == 0 {
		return []*DataSource{newDataSource(preDB, "", "")}
	}
	list := make([]*DataSource, len(measurements))
	for idx, measurement := range measurements {
		db := measurement.Database
		retention := measurement.RetentionPolicy
		table := measurement.Name
		if db == "" {
			db = preDB
		}
		list[idx] = newDataSource(db, retention, table)
	}

	return list
}

func getMeasurementsBySource(source influxql.Source) []*influxql.Measurement {
	list := make([]*influxql.Measurement, 0)
	switch source.(type) {
	case *influxql.Measurement:
		list = append(list, source.(*influxql.Measurement))
	case *influxql.SubQuery:
		query := source.(*influxql.SubQuery)
		stmt := query.Statement
		measurements := getMeasurementsBySources(stmt.Sources)
		list = append(list, measurements...)
	}
	return list
}

func getMeasurementsBySources(sources []influxql.Source) []*influxql.Measurement {
	list := make([]*influxql.Measurement, 0)
	for _, source := range sources {
		measurements := getMeasurementsBySource(source)
		list = append(list, measurements...)
	}
	return list
}

// 去重
func removeRepeatDataSource(dataSources []*DataSource) []*DataSource {
	// 只有一个datasource或者没有就不用循环了
	if len(dataSources) <= 1 {
		return dataSources
	}
	// 去重用的map
	repeatMap := make(map[string]map[string]int)

	// 返回的结果
	resultList := make([]*DataSource, 0, len(dataSources))

	// 遍历datasource
	// 如果db存在，则确认table是否存在,若table不存在，则新建table
	// 如果db不存在，则新建db和table
	// 新建table时将source加入resultList
	for _, dataSource := range dataSources {
		db := dataSource.GetDB()
		table := dataSource.GetTable()
		if dbMap, ok := repeatMap[db]; ok {
			if _, ok := dbMap[table]; ok {
				continue
			}
			dbMap[table] = 1
			resultList = append(resultList, dataSource)
			continue
		}
		repeatMap[db] = map[string]int{table: 1}
		resultList = append(resultList, dataSource)
	}
	return resultList
}

func getDataSourcesStatement(stmt influxql.Statement) ([]*DataSource, error) {
	var dataSources []*DataSource
	switch stmt.(type) {
	case *influxql.SelectStatement:
		selectStmt := stmt.(*influxql.SelectStatement)
		measurements := getMeasurementsBySources(selectStmt.Sources)
		dataSources = makeDataSource("", measurements)
	case *influxql.ShowDatabasesStatement:
		// show database 没有source，就创建一个空的
		dataSources = []*DataSource{newDataSource("", "", "")}
	case *influxql.ShowMeasurementsStatement:
		showStmt := stmt.(*influxql.ShowMeasurementsStatement)
		measurements := getMeasurementsBySource(showStmt.Source)
		dataSources = makeDataSource(showStmt.Database, measurements)
	case *influxql.ShowTagKeysStatement:
		showStmt := stmt.(*influxql.ShowTagKeysStatement)
		measurements := getMeasurementsBySources(showStmt.Sources)
		dataSources = makeDataSource(showStmt.Database, measurements)
	case *influxql.ShowTagValuesStatement:
		showStmt := stmt.(*influxql.ShowTagValuesStatement)
		measurements := getMeasurementsBySources(showStmt.Sources)
		dataSources = makeDataSource(showStmt.Database, measurements)
	case *influxql.ShowFieldKeysStatement:
		showStmt := stmt.(*influxql.ShowFieldKeysStatement)
		measurements := getMeasurementsBySources(showStmt.Sources)
		dataSources = makeDataSource(showStmt.Database, measurements)
	case *influxql.ShowSeriesStatement:
		showStmt := stmt.(*influxql.ShowSeriesStatement)
		measurements := getMeasurementsBySources(showStmt.Sources)
		dataSources = makeDataSource(showStmt.Database, measurements)
	default:
		return nil, ErrUnsupportedStmt
	}
	return dataSources, nil
}

func getDataSourcesStatements(stmts []influxql.Statement) ([]*DataSource, error) {
	list := make([]*DataSource, 0)
	for _, stmt := range stmts {
		dataSources, err := getDataSourcesStatement(stmt)
		if err != nil {
			return nil, err
		}
		list = append(list, dataSources...)
	}
	return removeRepeatDataSource(list), nil
}

// GetDataSourceBySQL 通过influxql的api分析sql，获取db和table
func GetDataSourceBySQL(sql string) ([]*DataSource, error) {
	q, err := influxql.ParseQuery(sql)
	if err != nil {
		return nil, err
	}
	return getDataSourcesStatements(q.Statements)
}

// GetDataSourceByStatement 通过influxql的api分析sql，获取db和table,该方法与GetDataSourceBySQL的区别在于，GetDataSourceBySQL会有多个statement的情况
func GetDataSourceByStatement(sql string) ([]*DataSource, error) {
	stmt, err := influxql.ParseStatement(sql)
	if err != nil {
		return nil, err
	}
	return getDataSourcesStatement(stmt)
}

// GetSingleDataSource 只获取一个route，多了就报错
func GetSingleDataSource(sql string) (*DataSource, error) {
	dataSources, err := GetDataSourceByStatement(sql)
	if err != nil {
		return nil, err
	}
	if len(dataSources) > 1 {
		return nil, ErrMultiRoutePath
	}
	if len(dataSources) == 0 {
		return nil, ErrNoRouteMatched
	}
	return dataSources[0], nil
}
