// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	ants "github.com/panjf2000/ants/v2"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	redisUtil "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/prometheus"
)

var ScrollSessionHelperInstance = redisUtil.NewScrollSessionHelper(
	ScrollSliceLimit,
	ScrollWindow,
	ScrollMaxSlice,
	ScrollLockTimeout,
)

type SliceQueryContext struct {
	Ctx     context.Context
	Session *redisUtil.ScrollSession
	QueryTs *structured.QueryTs

	Query   *metadata.Query
	Slice   redisUtil.SliceInfo
	Storage *tsdb.Storage

	StartTime time.Time
	EndTime   time.Time

	DataCh             chan<- map[string]any
	AllLabelMap        map[string][]function.LabelMapValue
	ResultTableOptions metadata.ResultTableOptions
	Total              *int64
	Message            *strings.Builder

	Lock        *sync.Mutex
	SessionLock *sync.Mutex
}

type ScrollQueryExecutor struct {
	ctx       context.Context
	session   *redisUtil.ScrollSession
	queryTs   *structured.QueryTs
	startTime time.Time
	endTime   time.Time

	dataCh             chan map[string]any
	allLabelMap        map[string][]function.LabelMapValue
	resultTableOptions metadata.ResultTableOptions
	total              int64
	message            strings.Builder

	lock        sync.Mutex
	sessionLock sync.Mutex
	sendWg      sync.WaitGroup
	pool        *ants.Pool
}

func NewScrollQueryExecutor(ctx context.Context, session *redisUtil.ScrollSession, queryTs *structured.QueryTs,
	start, end time.Time) *ScrollQueryExecutor {

	pool, _ := ants.NewPool(QueryMaxRouting)

	return &ScrollQueryExecutor{
		ctx:                ctx,
		session:            session,
		queryTs:            queryTs,
		startTime:          start,
		endTime:            end,
		dataCh:             make(chan map[string]any),
		allLabelMap:        make(map[string][]function.LabelMapValue),
		resultTableOptions: make(metadata.ResultTableOptions),
		pool:               pool,
	}
}

func (e *ScrollQueryExecutor) submitSliceQuery(qry *metadata.Query, slice redisUtil.SliceInfo, storage *tsdb.Storage) {
	e.sendWg.Add(1)

	submitErr := e.pool.Submit(func() {
		defer e.sendWg.Done()

		queryCtx := &SliceQueryContext{
			Ctx:                e.ctx,
			Session:            e.session,
			QueryTs:            e.queryTs,
			Query:              qry,
			Slice:              slice,
			Storage:            storage,
			StartTime:          e.startTime,
			EndTime:            e.endTime,
			DataCh:             e.dataCh,
			AllLabelMap:        e.allLabelMap,
			ResultTableOptions: e.resultTableOptions,
			Total:              &e.total,
			Message:            &e.message,
			Lock:               &e.lock,
			SessionLock:        &e.sessionLock,
		}

		if err := processSliceQueryWithHelper(queryCtx); err != nil {
			return
		}
	})

	if submitErr != nil {
		e.sendWg.Done()
		e.message.WriteString(fmt.Sprintf("failed to submit slice %d task for %s: %v ", slice.SliceIndex, qry.TableID, submitErr))
	}
}

func processSliceQueryWithHelper(queryCtx *SliceQueryContext) error {
	ctx := queryCtx.Ctx
	session := queryCtx.Session
	qry := queryCtx.Query
	slice := queryCtx.Slice
	storage := queryCtx.Storage
	queryTs := queryCtx.QueryTs
	dataCh := queryCtx.DataCh
	allLabelMap := queryCtx.AllLabelMap
	resultTableOptions := queryCtx.ResultTableOptions
	total := queryCtx.Total
	message := queryCtx.Message
	lock := queryCtx.Lock
	sessionLock := queryCtx.SessionLock
	start := queryCtx.StartTime
	end := queryCtx.EndTime

	connect := storage.Address
	tableId := qry.TableID
	instance := prometheus.GetTsDbInstance(ctx, qry)
	if instance == nil {
		return fmt.Errorf("no instance found for storage %s", qry.StorageID)
	}

	sliceQuery := *qry
	if queryTs.Scroll != "" {
		sliceQuery.Scroll = queryTs.Scroll
	}

	if sliceQuery.ResultTableOptions == nil {
		sliceQuery.ResultTableOptions = make(metadata.ResultTableOptions)
	}

	option := &metadata.ResultTableOption{
		SliceID:  &slice.SliceIndex,
		SliceMax: &session.MaxSlice,
		ScrollID: slice.ScrollID,
	}

	if slice.Index >= 0 {
		sliceQuery.From = slice.Index * session.Limit
	} else {
		sliceQuery.From = 0
		if slice.Index < 0 {
			log.Warnf(ctx, "Invalid index %d for slice %d, using 0", slice.Index, slice.SliceIndex)
		}
	}
	sliceQuery.Size = session.Limit
	sliceQuery.ResultTableOptions.SetOption(sliceQuery.TableID, storage.Address, option)

	labelMap, labelErr := sliceQuery.LabelMap()
	if labelErr == nil {
		lock.Lock()
		for k, lm := range labelMap {
			if _, ok := allLabelMap[k]; !ok {
				allLabelMap[k] = make([]function.LabelMapValue, 0)
			}
			allLabelMap[k] = append(allLabelMap[k], lm...)
		}
		lock.Unlock()
	}

	size, options, queryErr := instance.QueryRawData(ctx, &sliceQuery, start, end, dataCh)
	if queryErr != nil {
		message.WriteString(fmt.Sprintf("query %s:%s slice %d is error: %s ", sliceQuery.TableID, sliceQuery.Fields, slice.SliceIndex, queryErr.Error()))
		return queryErr
	}

	lock.Lock()
	if options != nil {
		resultTableOptions.MergeOptions(options)
	}
	*total += size
	lock.Unlock()

	sessionLock.Lock()
	if processErr := ScrollSessionHelperInstance.ProcessSliceResults(ctx, session, connect, tableId, slice.ScrollID, slice.SliceIndex, instance.InstanceType(), size, options); processErr != nil {
		log.Warnf(ctx, "Failed to process slice result: %v", processErr)
	} else {
		if updateErr := ScrollSessionHelperInstance.UpdateSession(ctx, session); updateErr != nil {
			log.Warnf(ctx, "Failed to update session: %v", updateErr)
		}
	}
	sessionLock.Unlock()

	return nil
}

func (e *ScrollQueryExecutor) processQueryForStorage(storage *tsdb.Storage, qry *metadata.Query) {
	connect := storage.Address
	tableId := qry.TableID
	instance := prometheus.GetTsDbInstance(e.ctx, qry)
	if instance == nil {
		return
	}

	e.sessionLock.Lock()
	slices, sliceErr := e.session.MakeSlices(e.ctx, instance.InstanceType(), connect, tableId)
	e.sessionLock.Unlock()

	if sliceErr != nil {
		e.message.WriteString(fmt.Sprintf("failed to make slices for %s: %v ", tableId, sliceErr))
		return
	}

	for _, slice := range slices {
		e.submitSliceQuery(qry, slice, storage)
	}
}

func (e *ScrollQueryExecutor) processStorageQueries(storageId string, queries []*metadata.Query) {
	storage, storageErr := tsdb.GetStorage(storageId)
	if storageErr != nil {
		e.message.WriteString(fmt.Sprintf("failed to get storage for %s: %v ", storageId, storageErr))
		return
	}

	for _, qry := range queries {
		e.processQueryForStorage(storage, qry)
	}
}

func (e *ScrollQueryExecutor) executeQueries(storageQueryMap map[string][]*metadata.Query) {
	defer func() {
		e.sendWg.Wait()
		close(e.dataCh)
	}()

	for storageId, queries := range storageQueryMap {
		e.processStorageQueries(storageId, queries)
	}
}

func (e *ScrollQueryExecutor) cleanup() {
	if e.pool != nil {
		e.pool.Release()
	}
}
