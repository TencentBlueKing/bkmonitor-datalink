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

	"github.com/jinzhu/copier"
	ants "github.com/panjf2000/ants/v2"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	redisUtil "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/prometheus"
)

type SliceQueryResult struct {
	Connect    string
	TableID    string
	SliceIndex int
	ScrollID   string
	TsDbType   string
	Size       int64
	Options    metadata.ResultTableOptions
	Error      error
}

type SliceQueryContext struct {
	Ctx                 context.Context
	Session             *redisUtil.ScrollSession
	ScrollSessionHelper *redisUtil.ScrollSessionHelper
	SessionKey          string
	QueryTs             *structured.QueryTs

	Query   *metadata.Query
	Slice   redisUtil.SliceInfo
	Storage *tsdb.Storage

	StartTime time.Time
	EndTime   time.Time

	DataCh             chan<- map[string]any
	ResultCh           chan<- SliceQueryResult
	AllLabelMap        map[string][]function.LabelMapValue
	ResultTableOptions metadata.ResultTableOptions
	Total              *int64
	Message            *strings.Builder

	Lock        *sync.Mutex
	SessionLock *sync.Mutex
}

func newSliceQueryContext(
	executor *ScrollQueryExecutor,
	scrollSessionHelperInstance *redisUtil.ScrollSessionHelper,
	query *metadata.Query,
	slice redisUtil.SliceInfo,
	storage *tsdb.Storage,
) *SliceQueryContext {
	return &SliceQueryContext{
		Ctx:                 executor.ctx,
		Session:             executor.session,
		ScrollSessionHelper: scrollSessionHelperInstance,
		SessionKey:          executor.sessionKey,
		QueryTs:             executor.queryTs,

		Query:   query,
		Slice:   slice,
		Storage: storage,

		StartTime: executor.startTime,
		EndTime:   executor.endTime,

		DataCh:             executor.dataCh,
		ResultCh:           executor.resultCh,
		AllLabelMap:        executor.allLabelMap,
		ResultTableOptions: executor.resultTableOptions,
		Total:              &executor.total,
		Message:            &executor.message,

		Lock:        executor.lock,
		SessionLock: executor.sessionLock,
	}
}

type ScrollQueryExecutor struct {
	ctx        context.Context
	sessionKey string
	session    *redisUtil.ScrollSession
	queryTs    *structured.QueryTs
	startTime  time.Time
	endTime    time.Time

	dataCh             chan map[string]any
	resultCh           chan SliceQueryResult
	allLabelMap        map[string][]function.LabelMapValue
	resultTableOptions metadata.ResultTableOptions
	total              int64
	message            strings.Builder

	lock        *sync.Mutex
	sessionLock *sync.Mutex
	sendWg      sync.WaitGroup
	pool        *ants.Pool
}

func newScrollQueryExecutor(
	ctx context.Context,
	sessionKey string,
	session *redisUtil.ScrollSession,
	queryTs *structured.QueryTs,
	start, end time.Time,
) *ScrollQueryExecutor {
	pool, _ := ants.NewPool(QueryMaxRouting)

	return &ScrollQueryExecutor{
		ctx:                ctx,
		sessionKey:         sessionKey,
		session:            session,
		queryTs:            queryTs,
		startTime:          start,
		endTime:            end,
		dataCh:             make(chan map[string]any),
		resultCh:           make(chan SliceQueryResult),
		allLabelMap:        make(map[string][]function.LabelMapValue),
		resultTableOptions: make(metadata.ResultTableOptions),
		pool:               pool,
		lock:               &sync.Mutex{},
		sessionLock:        &sync.Mutex{},
	}
}

func (e *ScrollQueryExecutor) submitSliceQuery(
	qry *metadata.Query,
	slice redisUtil.SliceInfo,
	storage *tsdb.Storage,
	scrollSessionHelperInstance *redisUtil.ScrollSessionHelper,
) {
	e.sendWg.Add(1)
	err := e.pool.Submit(func() {
		defer e.sendWg.Done()
		if err := processSliceQueryWithHelper(newSliceQueryContext(e, scrollSessionHelperInstance, qry, slice, storage)); err != nil {
			return
		}
	})

	if err != nil {
		e.message.WriteString(
			fmt.Sprintf(
				"failed to submit slice %d task for %s: %v ",
				slice.SliceIndex,
				qry.TableID,
				err,
			),
		)
	}
}

func processSliceQueryWithHelper(queryCtx *SliceQueryContext) error {
	ctx := queryCtx.Ctx
	instance := prometheus.GetTsDbInstance(queryCtx.Ctx, queryCtx.Query)
	if instance == nil {
		return fmt.Errorf("no instance found for storage %s", queryCtx.Query.StorageID)
	}
	qry := queryCtx.Query
	queryTs := queryCtx.QueryTs
	if queryCtx.QueryTs.Scroll != "" {
		qry.Scroll = queryTs.Scroll
	}

	start := queryCtx.StartTime
	end := queryCtx.EndTime
	dataCh := queryCtx.DataCh
	size, options, err := instance.QueryRawData(
		ctx,
		qry,
		start,
		end,
		dataCh,
	)
	message := queryCtx.Message
	slice := queryCtx.Slice
	if err != nil {
		message.WriteString(
			fmt.Sprintf(
				"query %s:%s slice %d is error: %s ",
				qry.TableID,
				qry.Fields,
				slice.SliceIndex,
				err.Error(),
			),
		)
		return err
	}
	lock := queryCtx.Lock
	resultTableOptions := queryCtx.ResultTableOptions

	lock.Lock()
	if options != nil {
		resultTableOptions.MergeOptions(options)
	}
	*queryCtx.Total += size
	lock.Unlock()
	sessionLock := queryCtx.SessionLock
	storage := queryCtx.Storage
	connect := storage.Address
	tableId := qry.TableID
	sessionKey := queryCtx.SessionKey
	session := queryCtx.Session
	sessionLock.Lock()
	if err = redisUtil.ScrollProcessSliceResult(ctx, sessionKey, session, connect, tableId, slice.SliceIndex, instance.InstanceType(), size, options); err != nil {
		log.Warnf(ctx, "Failed to process slice result: %v", err)
		sessionLock.Unlock()
		return err
	}
	sessionLock.Unlock()

	result := SliceQueryResult{
		Connect:    connect,
		TableID:    tableId,
		SliceIndex: slice.SliceIndex,
		ScrollID:   slice.ScrollID,
		TsDbType:   instance.InstanceType(),
		Size:       size,
		Options:    options,
		Error:      nil,
	}

	if options != nil {
		resultOption := options.GetOption(tableId, connect)
		if resultOption != nil && resultOption.ScrollID != "" {
			result.ScrollID = resultOption.ScrollID
		}
	}
	return nil
}

func (e *ScrollQueryExecutor) processQueryForStorage(
	storage *tsdb.Storage,
	qry *metadata.Query,
	scrollSessionHelperInstance *redisUtil.ScrollSessionHelper,
) {
	connect := storage.Address
	tableId := qry.TableID
	instance := prometheus.GetTsDbInstance(e.ctx, qry)
	if instance == nil {
		return
	}

	slices, err := e.session.MakeSlices(
		instance.InstanceType(),
		connect,
		tableId,
	)
	if err != nil {
		e.message.WriteString(
			fmt.Sprintf("failed to make slices for %s: %v ", tableId, err),
		)
		return
	}

	for _, slice := range slices {
		qry, err := e.createSliceQuery(qry, slice, instance.InstanceType(), storage)
		if err != nil {
			e.message.WriteString(
				fmt.Sprintf("failed to create slice query for %s: %v ", tableId, err),
			)
			return
		}
		e.submitSliceQuery(qry, slice, storage, scrollSessionHelperInstance)
	}
}

func (e *ScrollQueryExecutor) createSliceQuery(originalQry *metadata.Query, slice redis.SliceInfo, instanceType string, storage *tsdb.Storage) (*metadata.Query, error) {
	qry := &metadata.Query{}
	err := copier.CopyWithOption(qry, originalQry, copier.Option{
		DeepCopy: true,
	})
	if err != nil {
		return nil, err
	}
	qry.ResultTableOptions = make(metadata.ResultTableOptions)
	if e.queryTs.Scroll != "" {
		qry.Scroll = e.queryTs.Scroll
	}

	var from int
	if slice.Index >= 0 {
		from = slice.Index * e.session.Limit
	}
	fromPtr := &from
	option := &metadata.ResultTableOption{
		SliceID:  &slice.SliceIndex,
		SliceMax: &e.session.MaxSlice,
		ScrollID: slice.ScrollID,
		From:     fromPtr,
	}

	address := storage.Address
	if instanceType == consul.BkSqlStorageType {
		address = ""
	}
	qry.ResultTableOptions.SetOption(qry.TableID, address, option)
	return qry, nil
}

func (e *ScrollQueryExecutor) processStorageQueries(
	storageId string,
	queries []*metadata.Query,
	scrollSessionHelperInstance *redisUtil.ScrollSessionHelper,
) {
	storage, err := tsdb.GetStorage(storageId)
	if err != nil {
		e.message.WriteString(
			fmt.Sprintf("failed to get storage for %s: %v ", storageId, err),
		)
		return
	}

	for _, qry := range queries {
		e.processQueryForStorage(storage, qry, scrollSessionHelperInstance)
	}
}

func (e *ScrollQueryExecutor) executeQueries(
	storageQueryMap map[string][]*metadata.Query,
	scrollSessionHelperInstance *redisUtil.ScrollSessionHelper,
) {
	defer func() {
		e.sendWg.Wait()
		close(e.dataCh)
		close(e.resultCh)
	}()

	for storageId, queries := range storageQueryMap {
		e.processStorageQueries(storageId, queries, scrollSessionHelperInstance)
	}
}

func (e *ScrollQueryExecutor) cleanup() {
	if e.pool != nil {
		e.pool.Release()
	}
}
