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
	"context"
	"fmt"
	"sync"
	"time"

	ants "github.com/panjf2000/ants/v2"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/client"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

const (
	BKTaskIndex = "bk_task_index"
)

// 全局table信息
var tableLock sync.RWMutex

var tableInfos map[string]*TableInfo

var perQueryMaxGoroutine int

// InitGlobalInstance 兼容单元测试
func InitGlobalInstance(ctx context.Context, params *Params, client client.Client) error {
	storageLock.Lock()
	defer storageLock.Unlock()
	instance, err := NewInstance(ctx, params, client)
	if err != nil {
		return err
	}
	storageMap[""] = instance
	storageMap["0"] = instance

	return nil
}

// GetInstance 初始化全局influxdb实例
func GetInstance(clusterID string) (*Instance, error) {
	storageLock.Lock()
	defer storageLock.Unlock()
	instance, ok := storageMap[clusterID]
	if !ok {
		return nil, fmt.Errorf("%s: clusterID: %s", ErrStorageNotFound, clusterID)
	}
	return instance, nil
}

// SetTablesInfo
func SetTablesInfo(infos map[string]*consul.InfluxdbTableInfo) {
	result := make(map[string]*TableInfo)
	for key, info := range infos {
		result[key] = &TableInfo{
			SegmentedQueryEnable: info.SegmentedQueryEnable,
			IsPivotTable:         info.PivotTable,
			InfluxdbVersion:      info.InfluxdbVersion,
		}
	}
	log.Debugf(context.TODO(), "update influxdb table info:%v", result)
	tableLock.Lock()
	defer tableLock.Unlock()
	tableInfos = result
}

var SegmentedQueryEnable = func(db, measurement string) bool {
	tableLock.RLock()
	defer tableLock.RUnlock()

	for _, tableID := range []string{
		fmt.Sprintf("%s.%s", db, measurement),
		fmt.Sprintf("%s.__default__", db),
	} {
		if value, ok := tableInfos[tableID]; ok {
			return value.SegmentedQueryEnable
		}
	}

	return false
}

// IsPivotTable 判断是否为自定义类型table
var IsPivotTable = func(tableID string) bool {
	tableLock.RLock()
	defer tableLock.RUnlock()
	if value, ok := tableInfos[tableID]; ok {
		return value.IsPivotTable
	}
	return false
}

// TableInfo
type TableInfo struct {
	// 是否是行转列表
	IsPivotTable bool

	// 是否开启分段查询
	SegmentedQueryEnable bool

	// table_id对应的influxdb集群版本号，用于决定ast逻辑的版本
	InfluxdbVersion string
}

// SQLInfo
type SQLInfo struct {
	ClusterID    string
	DB           string
	SQL          string
	Limit        int
	SLimit       int
	WithGroupBy  bool
	IsCountGroup bool
	MetricName   string
	BkTaskValue  string
}

// QueryInfosAsync
func QueryInfosAsync(ctx context.Context, sqlInfos []SQLInfo, precision string, limit int) (*Tables, []error) {
	// 如果指标不存在则返回空数据
	if len(sqlInfos) == 0 {
		return nil, nil
	}

	var (
		length      = len(sqlInfos)
		tablesCh    = make(chan *Tables, 1)
		totalTables = NewTables()
		recvDone    = make(chan struct{})
		errs        []error // 由于查询模块无法知道指标在某个具体表上，所以当任意表查询失败，都返回失败
		wg          sync.WaitGroup
		start       = time.Now()
		err         error
	)

	ctx, span := trace.NewSpan(ctx, "query-infos-async")
	defer span.End(&err)

	span.Set("sql-nums", len(sqlInfos))
	span.Set("query-max-goroutine", perQueryMaxGoroutine)

	log.Debugf(ctx, "query sql async length:%d", length)

	go func() {
		defer func() { recvDone <- struct{}{} }()
		var tableList []*Tables
		for tables := range tablesCh {
			tableList = append(tableList, tables)
		}
		if len(tableList) == 0 {
			return
		}

		totalTables = MergeTables(tableList, false)
	}()

	p, _ := ants.NewPoolWithFunc(perQueryMaxGoroutine, func(i any) {
		defer wg.Done()
		index, ok := i.(int)
		if ok {
			if index < len(sqlInfos) {
				var (
					sqlInfo    = sqlInfos[index]
					clusterID  = sqlInfo.ClusterID
					db         = sqlInfo.DB
					sql        = sqlInfo.SQL
					metricName = sqlInfo.MetricName
					limit      = sqlInfo.Limit
				)

				instance, err := GetInstance(clusterID)
				if err != nil {
					log.Errorf(ctx, "%s %s", err.Error(), clusterID)
					return
				}

				tables, err := instance.QueryInfos(ctx, metricName, db, sql, precision, limit)
				if err != nil {
					log.Errorf(ctx, "query failed,db:%s,sql:%s,error:%s", db, sql, err)
					errs = append(errs, err)
					return
				}

				if tables != nil && tables.Length() > 0 {

					span.Set(fmt.Sprintf("table_num_%d", i), len(tables.Tables))
					log.Debugf(ctx,
						"influxdb query info async:db:[%s], sql:[%s], table:[%d]", db, sql, len(tables.Tables),
					)

					// 增加一个顺序标记位
					tables.Index = index
					tablesCh <- tables
				}
			} else {
				log.Errorf(ctx, "sql index error: %+v", index)
			}
		} else {
			log.Errorf(ctx, "sql index error: %+v", index)
		}
	})
	defer p.Release()

	for i := range sqlInfos {
		wg.Add(1)
		p.Invoke(i)
	}
	wg.Wait()

	close(tablesCh)
	<-recvDone

	span.Set("total_table_num", totalTables.Length())

	log.Debugf(ctx, "influxdb query info async:%v, query total cost:%s", sqlInfos, time.Since(start))

	// return mergeTablesInfo(totalTables), nil
	// totalTables 后续的处理中有做Fill调整格式，同时做了去重。这里暂时先不做去重
	return totalTables, nil
}

// QueryAsync 异步查询数据
// 此方法会将所有series放在同一个Tables中，所以仅仅用于查询单指标的情况，请勿查询不同指标
func QueryAsync(ctx context.Context, sqlInfos []SQLInfo, precision string) (*Tables, []error) {
	// 如果指标不存在则返回空数据
	if len(sqlInfos) == 0 {
		return nil, nil
	}

	var (
		length      = len(sqlInfos)
		tablesCh    = make(chan *Tables, 1)
		totalTables = NewTables()
		recvDone    = make(chan struct{})
		errs        []error // 由于查询模块无法知道指标在某个具体表上，所以当任意表查询失败，都返回失败

		err error
		wg  sync.WaitGroup
	)
	ctx, span := trace.NewSpan(ctx, "query-async")
	defer span.End(&err)

	span.Set("sql-nums", len(sqlInfos))
	span.Set("query-max-goroutine", perQueryMaxGoroutine)

	log.Debugf(ctx, "query sql async length:%d", length)

	go func() {
		defer func() { recvDone <- struct{}{} }()
		var tableList []*Tables
		for tables := range tablesCh {
			tableList = append(tableList, tables)
		}
		if len(tableList) == 0 {
			return
		}

		totalTables = MergeTables(tableList, true)
	}()

	p, _ := ants.NewPoolWithFunc(perQueryMaxGoroutine, func(i any) {
		defer wg.Done()
		index, ok := i.(int)
		if ok {
			if index < len(sqlInfos) {
				var (
					sqlInfo      = sqlInfos[index]
					clusterID    = sqlInfo.ClusterID
					db           = sqlInfo.DB
					sql          = sqlInfo.SQL
					metricName   = sqlInfo.MetricName
					bkTaskValue  = sqlInfo.BkTaskValue
					withGroupBy  = sqlInfo.WithGroupBy
					isCountGroup = sqlInfo.IsCountGroup
					limit        = sqlInfo.Limit
					slimit       = sqlInfo.SLimit
				)

				instance, err := GetInstance(clusterID)
				if err != nil {
					log.Errorf(ctx, fmt.Sprintf("%s [%v]", err.Error(), sqlInfo))
					return
				}

				// 增加序号维度，以区分不同db（tableID）出来的数据
				expandMap := make(map[string]string)
				expandMap[BKTaskIndex] = bkTaskValue

				tables, err := instance.Query(
					ctx, metricName, db, sql, precision, withGroupBy, isCountGroup, expandMap, limit, slimit,
				)

				if err != nil {
					errs = append(errs, fmt.Errorf("db: %s, err:[%s]", db, err))
					return
				}
				if tables == nil || tables.Length() == 0 {
					return
				}

				// 增加一个顺序标记位
				tables.Index = index
				tablesCh <- tables
			} else {
				log.Errorf(ctx, "sql index error: %+v", index)
			}
		} else {
			log.Errorf(ctx, "sql index error: %+v", index)
		}
	})
	defer p.Release()

	for i := range sqlInfos {
		wg.Add(1)
		p.Invoke(i)
	}
	wg.Wait()

	close(tablesCh)
	<-recvDone

	// 增加去重逻辑
	return totalTables, errs
}

// init
func init() {
	storageMap = make(map[string]*Instance)
	storageLock = new(sync.RWMutex)
}
