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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
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
	Message    string
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
		resultCh:           make(chan SliceQueryResult, QueryMaxRouting),
		allLabelMap:        make(map[string][]function.LabelMapValue),
		resultTableOptions: make(metadata.ResultTableOptions),
		pool:               pool,
		sessionLock:        &sync.Mutex{},
	}
}

func (e *ScrollQueryExecutor) submitSliceQuery(ctx context.Context, qry *metadata.Query, slice redisUtil.SliceInfo, storage *tsdb.Storage, scrollSessionHelperInstance *redisUtil.ScrollSessionHelper) error {
	e.sendWg.Add(1)
	return e.pool.Submit(func() {
		defer e.sendWg.Done()
		if err := processSliceQueryWithHelper(ctx, newSliceQueryContext(e, scrollSessionHelperInstance, qry, slice, storage)); err != nil {
			log.Warnf(e.ctx, "Failed to submit slice query: %v", err)
			result := SliceQueryResult{
				Connect:    storage.Address,
				TableID:    qry.TableID,
				SliceIndex: slice.SliceIndex,
				ScrollID:   slice.ScrollID,
				TsDbType:   "",
				Size:       0,
				Error:      err,
				Message:    fmt.Sprintf("Failed to submit slice query: %v", err),
			}
			e.resultCh <- result
			return
		}
	})

}

func processSliceQueryWithHelper(ctx context.Context, queryCtx *SliceQueryContext) error {
	var err error
	ctx, span := trace.NewSpan(ctx, "process-slice-query-with-helper")
	defer span.End(&err)
	instance := prometheus.GetTsDbInstance(queryCtx.Ctx, queryCtx.Query)
	if instance == nil {
		return fmt.Errorf("no instance found for storage %s", queryCtx.Query.StorageID)
	}
	if queryCtx.QueryTs.Scroll != "" {
		queryCtx.Query.Scroll = queryCtx.QueryTs.Scroll
	}

	size, options, err := instance.QueryRawData(queryCtx.Ctx, queryCtx.Query, queryCtx.StartTime, queryCtx.EndTime, queryCtx.DataCh)

	result := SliceQueryResult{
		Connect:    queryCtx.Storage.Address,
		TableID:    queryCtx.Query.TableID,
		SliceIndex: queryCtx.Slice.SliceIndex,
		ScrollID:   queryCtx.Slice.ScrollID,
		TsDbType:   instance.InstanceType(),
		Size:       size,
		Options:    options,
		Error:      err,
	}
	span.Set("scroll-query-result", result)

	if err != nil {
		result.Message = fmt.Sprintf(
			"query %s:%s slice %d is error: %s",
			queryCtx.Query.TableID,
			queryCtx.Query.Fields,
			queryCtx.Slice.SliceIndex,
			err.Error(),
		)
		queryCtx.ResultCh <- result

		queryCtx.SessionLock.Lock()
		queryCtx.Session.RemoveScrollID(queryCtx.Storage.Address, queryCtx.Query.TableID, queryCtx.Slice.SliceIndex)
		queryCtx.Session.MarkSliceDone(queryCtx.Storage.Address, queryCtx.Query.TableID, queryCtx.Slice.SliceIndex)

		_, hasMoreData := queryCtx.Session.HasMoreData(instance.InstanceType())
		if !hasMoreData {
			queryCtx.Session.Status = redisUtil.SessionStatusDone
		}

		if updateErr := redisUtil.UpdateSession(queryCtx.Ctx, queryCtx.SessionKey, queryCtx.Session); updateErr != nil {
			err = updateErr

		}
		queryCtx.SessionLock.Unlock()

		return err
	}

	queryCtx.SessionLock.Lock()
	err = redisUtil.ScrollProcessSliceResult(
		queryCtx.Ctx,
		queryCtx.SessionKey,
		queryCtx.Session,
		queryCtx.Storage.Address,
		queryCtx.Query.TableID,
		queryCtx.Slice.SliceIndex,
		instance.InstanceType(),
		size,
		options,
	)
	queryCtx.SessionLock.Unlock()

	if err != nil {
		result.Error = err
		result.Message = fmt.Sprintf("Failed to process slice result: %v", err)
	}

	if options != nil {
		resultOption := options.GetOption(queryCtx.Query.TableID, queryCtx.Storage.Address)
		if resultOption != nil && resultOption.ScrollID != "" {
			result.ScrollID = resultOption.ScrollID
		}
	}

	queryCtx.ResultCh <- result
	return nil
}

func (e *ScrollQueryExecutor) processQueryForStorage(ctx context.Context, storage *tsdb.Storage, qry *metadata.Query, scrollSessionHelperInstance *redisUtil.ScrollSessionHelper) error {
	instance := prometheus.GetTsDbInstance(e.ctx, qry)
	if instance == nil {
		return fmt.Errorf("no instance found for storage %s", storage.Address)
	}

	slices, err := e.session.MakeSlices(
		instance.InstanceType(),
		storage.Address,
		qry.TableID,
	)
	if err != nil {
		return err
	}

	for _, slice := range slices {
		cpQry, err := e.createSliceQuery(qry, slice, instance.InstanceType(), storage)
		if err != nil {
			return err
		}
		if sErr := e.submitSliceQuery(ctx, cpQry, slice, storage, scrollSessionHelperInstance); sErr != nil {
			return sErr
		}
	}
	return nil
}

func (e *ScrollQueryExecutor) createSliceQuery(originalQry *metadata.Query, slice redis.SliceInfo, instanceType string, storage *tsdb.Storage) (qry *metadata.Query, err error) {
	qry = &metadata.Query{}
	err = copier.CopyWithOption(qry, originalQry, copier.Option{
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
		// 这里是因为在BkSql在获取resultOption时候没有指定address
		address = ""
	}
	qry.ResultTableOptions.SetOption(qry.TableID, address, option)
	return qry, nil
}

func (e *ScrollQueryExecutor) processStorageQueries(ctx context.Context, storageId string, queries []*metadata.Query, scrollSessionHelperInstance *redisUtil.ScrollSessionHelper) error {
	storage, err := tsdb.GetStorage(storageId)
	if err != nil {
		return err
	}

	for _, qry := range queries {
		if err = e.processQueryForStorage(ctx, storage, qry, scrollSessionHelperInstance); err != nil {
			return err
		}
	}

	return nil
}

func (e *ScrollQueryExecutor) executeQueries(ctx context.Context, storageQueryMap map[string][]*metadata.Query, scrollSessionHelperInstance *redisUtil.ScrollSessionHelper) error {
	defer func() {
		e.sendWg.Wait()
		close(e.dataCh)
		close(e.resultCh)
	}()

	for storageId, queries := range storageQueryMap {
		if err := e.processStorageQueries(ctx, storageId, queries, scrollSessionHelperInstance); err != nil {
			return err
		}
	}
	return nil
}

func (e *ScrollQueryExecutor) cleanup() {
	if e.pool != nil {
		e.pool.Release()
	}
}
