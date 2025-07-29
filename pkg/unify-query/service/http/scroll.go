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
) error {
	e.sendWg.Add(1)
	return e.pool.Submit(func() {
		defer e.sendWg.Done()
		if err := processSliceQueryWithHelper(newSliceQueryContext(e, scrollSessionHelperInstance, qry, slice, storage)); err != nil {
			return
		}
	})
}

func processSliceQueryWithHelper(queryCtx *SliceQueryContext) error {
	instance := prometheus.GetTsDbInstance(queryCtx.Ctx, queryCtx.Query)
	if instance == nil {
		return fmt.Errorf("no instance found for storage %s", queryCtx.Query.StorageID)
	}
	if queryCtx.QueryTs.Scroll != "" {
		queryCtx.Query.Scroll = queryCtx.QueryTs.Scroll
	}

	size, options, err := instance.QueryRawData(
		queryCtx.Ctx,
		queryCtx.Query,
		queryCtx.StartTime,
		queryCtx.EndTime,
		queryCtx.DataCh,
	)
	if err != nil {
		queryCtx.Message.WriteString(
			fmt.Sprintf(
				"query %s:%s slice %d is error: %s ",
				queryCtx.Query.TableID,
				queryCtx.Query.Fields,
				queryCtx.Slice.SliceIndex,
				err.Error(),
			),
		)
		return err
	}
	queryCtx.Lock.Lock()
	if options != nil {
		queryCtx.ResultTableOptions.MergeOptions(options)
	}
	*queryCtx.Total += size
	queryCtx.Lock.Unlock()
	queryCtx.SessionLock.Lock()
	defer queryCtx.SessionLock.Unlock()
	if err = redisUtil.ScrollProcessSliceResult(queryCtx.Ctx, queryCtx.Slice, queryCtx.SessionKey, queryCtx.Session, queryCtx.Storage.Address, queryCtx.Query.TableID, queryCtx.Slice.SliceIndex, instance.InstanceType(), size, options); err != nil {
		log.Warnf(queryCtx.Ctx, "Failed to process slice result: %v", err)
		return err
	}
	return nil
}

func (e *ScrollQueryExecutor) processQueryForStorage(
	storage *tsdb.Storage,
	qry *metadata.Query,
	scrollSessionHelperInstance *redisUtil.ScrollSessionHelper,
) error {
	instance := prometheus.GetTsDbInstance(e.ctx, qry)
	if instance == nil {
		return fmt.Errorf("no instance found for storage %s", qry.StorageID)
	}

	slices, err := e.session.MakeSlices(
		instance.InstanceType(),
		storage.Address,
		qry.TableID,
	)
	if err != nil {
		e.message.WriteString(
			fmt.Sprintf("failed to make slices for %s: %v ", qry.TableID, err),
		)
		return err
	}

	for _, slice := range slices {
		qryCp, cErr := e.createSliceQuery(qry, slice, instance.InstanceType(), storage)
		if cErr != nil {
			e.message.WriteString(
				fmt.Sprintf("failed to create slice query for %s: %v ", qryCp.TableID, cErr),
			)
			return cErr
		}
		sErr := e.submitSliceQuery(qryCp, slice, storage, scrollSessionHelperInstance)
		if sErr != nil {
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
		address = ""
	}
	qry.ResultTableOptions.SetOption(qry.TableID, address, option)
	return qry, nil
}

func (e *ScrollQueryExecutor) processStorageQueries(
	storageId string,
	queries []*metadata.Query,
	scrollSessionHelperInstance *redisUtil.ScrollSessionHelper,
) error {
	storage, err := tsdb.GetStorage(storageId)
	if err != nil {
		return err
	}

	for _, qry := range queries {
		if err = e.processQueryForStorage(storage, qry, scrollSessionHelperInstance); err != nil {
			return err
		}
	}
	return nil
}

func (e *ScrollQueryExecutor) executeQueries(
	storageQueryMap map[string][]*metadata.Query,
	scrollSessionHelperInstance *redisUtil.ScrollSessionHelper,
) error {
	defer func() {
		e.sendWg.Wait()
		close(e.dataCh)
		close(e.resultCh)
	}()

	for storageId, queries := range storageQueryMap {
		if err := e.processStorageQueries(storageId, queries, scrollSessionHelperInstance); err != nil {
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
