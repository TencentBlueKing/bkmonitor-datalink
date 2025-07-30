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
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/prometheus"
)

func prepareQueryTs(ctx context.Context, queryTs *structured.QueryTs) (queryList []*metadata.Query, err error) {
	if queryTs.SpaceUid == "" {
		queryTs.SpaceUid = metadata.GetUser(ctx).SpaceUID
	}
	for _, ql := range queryTs.QueryList {
		// 时间复用
		ql.Timezone = queryTs.Timezone
		ql.Start = queryTs.Start
		ql.End = queryTs.End

		// 排序复用
		ql.OrderBy = queryTs.OrderBy
		ql.DryRun = queryTs.DryRun

		// 如果 qry.Step 不存在去外部统一的 step
		if ql.Step == "" {
			ql.Step = queryTs.Step
		}

		if queryTs.ResultTableOptions != nil {
			ql.ResultTableOptions = queryTs.ResultTableOptions
		}

		// 如果 Limit / From 没有单独指定的话，同时外部指定了的话，使用外部的
		if ql.Limit == 0 && queryTs.Limit > 0 {
			ql.Limit = queryTs.Limit
		}

		// 在使用 multiFrom 模式下，From 需要保持为 0，因为 from 存放在 resultTableOptions 里面
		if queryTs.IsMultiFrom {
			queryTs.From = 0
		}

		if ql.From == 0 && queryTs.From > 0 {
			ql.From = queryTs.From
		}

		// 复用 scroll 配置，如果配置了 scroll 优先使用 scroll
		if queryTs.Scroll != "" {
			ql.Scroll = queryTs.Scroll
			queryTs.IsMultiFrom = false
		}

		// 复用字段配置，没有特殊配置的情况下使用公共配置
		if len(ql.KeepColumns) == 0 && len(queryTs.ResultColumns) != 0 {
			ql.KeepColumns = queryTs.ResultColumns
		}

		qm, qmErr := ql.ToQueryMetric(ctx, queryTs.SpaceUid)
		if qmErr != nil {
			err = qmErr
			return
		}

		for _, qry := range qm.QueryList {
			if qry != nil {
				if qry.ResultTableOptions == nil {
					qry.ResultTableOptions = make(metadata.ResultTableOptions)
				}
				queryList = append(queryList, qry)
			}
		}
	}
	return
}

func collectStorageQuery(qryList []*metadata.Query) map[string][]*metadata.Query {
	storageQuery := make(map[string][]*metadata.Query)
	for _, qry := range qryList {
		if qry == nil || qry.StorageID == "" {
			continue
		}
		storageQuery[qry.StorageID] = append(storageQuery[qry.StorageID], qry)
	}
	return storageQuery
}

type StorageScrollQuery struct {
	QueryList []*metadata.Query
	Storage   *tsdb.Storage
	Connect   string
	TableID   string
}

func collectStorageScrollQuery(ctx context.Context, session *redis.ScrollSession, storageQueryMap map[string][]*metadata.Query) (list []StorageScrollQuery, err error) {
	for storageID, qryList := range storageQueryMap {
		storage, gErr := tsdb.GetStorage(storageID)
		if gErr != nil {
			err = gErr
			return
		}

		connect := storage.Address
		for _, qry := range qryList {
			storage.Instance = prometheus.GetTsDbInstance(ctx, qry)
			tableID := qry.TableID
			slices, mErr := session.MakeSlices(storage.Instance.InstanceType(), connect, tableID)
			if mErr != nil {
				err = mErr
				return
			}
			var injectedScrollQueryList []*metadata.Query
			for _, slice := range slices {
				qryCp, iErr := injectScrollQuery(qry, connect, tableID, slice)
				if iErr != nil {
					err = iErr
					return
				}
				injectedScrollQueryList = append(injectedScrollQueryList, qryCp)
			}
			list = append(list, StorageScrollQuery{
				QueryList: injectedScrollQueryList,
				Storage:   storage,
				Connect:   connect,
				TableID:   tableID,
			})
		}
	}
	return
}

func injectScrollQuery(qry *metadata.Query, connect, tableID string, sliceInfo *redis.SliceInfo) (*metadata.Query, error) {
	qryCp := &metadata.Query{}
	if err := copier.CopyWithOption(qryCp, qry, copier.Option{
		DeepCopy: true,
	}); err != nil {
		return nil, err
	}

	if qryCp.ResultTableOptions == nil {
		qryCp.ResultTableOptions = make(metadata.ResultTableOptions)
	}

	option := &metadata.ResultTableOption{
		ScrollID:   sliceInfo.ScrollID,
		SliceIndex: &sliceInfo.SliceIdx,
		SliceMax:   &sliceInfo.SliceMax,
	}

	var connectKey string
	if sliceInfo.StorageType == consul.BkSqlStorageType {
		option.From = &sliceInfo.Offset
		connectKey = ""
	} else {
		connectKey = connect
	}

	qryCp.ResultTableOptions.SetOption(tableID, connectKey, option)

	return qryCp, nil
}

func scrollQueryWorker(ctx context.Context, session *redis.ScrollSession, connect, tableID string, qry *metadata.Query, start time.Time, end time.Time, instance tsdb.Instance) (data []map[string]any, err error) {
	dataCh := make(chan map[string]any)
	var oldSliceResultOption *metadata.ResultTableOption
	if qry.ResultTableOptions != nil {
		oldSliceResultOption = qry.ResultTableOptions.GetOption(tableID, connect)
	}

	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for d := range dataCh {
			data = append(data, d)
		}
	}()

	total, option, err := instance.QueryRawData(ctx, qry, start, end, dataCh)
	close(dataCh)
	wg.Wait()
	var sliceResultOption *metadata.ResultTableOption
	if option != nil {
		var connectKey string
		if instance.InstanceType() == consul.BkSqlStorageType {
			connectKey = ""
		} else {
			connectKey = connect
		}

		sliceResultOption = option.GetOption(tableID, connectKey)
	}
	switch instance.InstanceType() {
	case consul.ElasticsearchStorageType:
		if err != nil {
			if sliceResultOption != nil && sliceResultOption.SliceIndex != nil {
				err = session.UpdateScrollID(ctx, connect, tableID, sliceResultOption.ScrollID, sliceResultOption.SliceIndex, redis.StatusFailed)
			}
			return
		}

		if sliceResultOption != nil && sliceResultOption.SliceIndex != nil {
			if oldSliceResultOption == nil || (oldSliceResultOption != nil && oldSliceResultOption.ScrollID != sliceResultOption.ScrollID) {
				if err = session.UpdateScrollID(ctx, connect, tableID, sliceResultOption.ScrollID, sliceResultOption.SliceIndex, redis.StatusRunning); err != nil {
					return
				}
			}
		}

		isEmptyData := len(data) == 0 && total == 0
		isScrollIDEmpty := sliceResultOption != nil && sliceResultOption.ScrollID == ""

		if isEmptyData && oldSliceResultOption != nil && oldSliceResultOption.SliceIndex != nil {
			if err = session.UpdateScrollID(ctx, connect, tableID, "", oldSliceResultOption.SliceIndex, redis.StatusCompleted); err != nil {
				return
			}
		}

		if (isEmptyData || isScrollIDEmpty) && (sliceResultOption != nil && sliceResultOption.SliceIndex != nil) {
			if err = session.UpdateScrollID(ctx, connect, tableID, sliceResultOption.ScrollID, sliceResultOption.SliceIndex, redis.StatusCompleted); err != nil {
				return
			}
		} else if len(data) > 0 && (sliceResultOption != nil && sliceResultOption.SliceIndex != nil) && !isScrollIDEmpty {
			if err = session.UpdateScrollID(ctx, connect, tableID, sliceResultOption.ScrollID, sliceResultOption.SliceIndex, redis.StatusRunning); err != nil {
				return
			}
		}

	case consul.BkSqlStorageType:
		if err != nil {
			var targetSliceOption *metadata.ResultTableOption
			if sliceResultOption != nil && sliceResultOption.SliceIndex != nil {
				targetSliceOption = sliceResultOption
			} else if oldSliceResultOption != nil && oldSliceResultOption.SliceIndex != nil {
				targetSliceOption = oldSliceResultOption
			}
			if targetSliceOption != nil {
				err = session.UpdateDoris(ctx, tableID, targetSliceOption.SliceIndex, redis.StatusFailed)
			}
			return
		}

		var targetSliceOption *metadata.ResultTableOption
		if sliceResultOption != nil && sliceResultOption.SliceIndex != nil {
			targetSliceOption = sliceResultOption
		} else if oldSliceResultOption != nil && oldSliceResultOption.SliceIndex != nil {
			targetSliceOption = oldSliceResultOption
		}

		if targetSliceOption != nil && targetSliceOption.SliceIndex != nil {
			if len(data) > 0 {
				if err = session.RollDoris(ctx, tableID, targetSliceOption.SliceIndex); err != nil {
					return
				}
			} else {
				if err = session.UpdateDoris(ctx, tableID, targetSliceOption.SliceIndex, redis.StatusCompleted); err != nil {
					return
				}
			}
		}
	default:
		err = errors.New("unknown storage type")
	}
	return
}

func generateScrollSuffix(name string, ts structured.QueryTs) (string, error) {
	ts.ClearCache = false
	key, err := json.StableMarshal(ts)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s:%s", name, key), nil
}
