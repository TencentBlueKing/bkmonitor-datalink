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
	"github.com/spf13/cast"

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
				queryList = append(queryList, qry)
			}
		}
	}
	return
}

type StorageScrollQuery struct {
	QueryList []*metadata.Query
	Instance  tsdb.Instance
	Connect   string
	TableID   string
}

func collectStorageScrollQuery(ctx context.Context, session *redis.ScrollSession, qryList []*metadata.Query) (list []StorageScrollQuery, err error) {
	for _, qry := range qryList {
		instance := prometheus.GetTsDbInstance(ctx, qry)
		connects := instance.InstanceConnects()
		if len(connects) == 0 {
			err = fmt.Errorf("no connects found for query: %v", qry)
			return
		}
		for _, connect := range connects {
			slices, mErr := instance.ScrollHandler().MakeSlices(ctx, session, connect, qry.TableID)
			if mErr != nil {
				err = mErr
				return
			}
			var injectedScrollQueryList []*metadata.Query
			for _, slice := range slices {
				qryCp, iErr := injectScrollQuery(qry, connect, qry.TableID, slice)
				if iErr != nil {
					err = iErr
					return
				}
				injectedScrollQueryList = append(injectedScrollQueryList, qryCp)
			}
			list = append(list, StorageScrollQuery{
				QueryList: injectedScrollQueryList,
				Connect:   connect,
				Instance:  instance,
				TableID:   qry.TableID,
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
		From:       &sliceInfo.Offset,
	}

	qryCp.ResultTableOptions.SetOption(tableID, connect, option)

	return qryCp, nil
}

func scrollQueryWorker(ctx context.Context, session *redis.ScrollSession, connect, tableID string, qry *metadata.Query, start time.Time, end time.Time, instance tsdb.Instance) (data []map[string]any, err error) {
	dataCh := make(chan map[string]any)
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for d := range dataCh {
			data = append(data, d)
		}
	}()

	_, resultOption, err := instance.QueryRawData(ctx, qry, start, end, dataCh)
	close(dataCh)
	wg.Wait()

	var sliceResultOption *metadata.ResultTableOption
	if resultOption != nil {
		if opt := resultOption.GetOption(tableID, connect); opt != nil {
			sliceResultOption = opt
		}
	} else {
		qryOption := qry.ResultTableOptions.GetOption(tableID, connect)
		if qryOption != nil {
			qryOption.ScrollID = ""
			sliceResultOption = qryOption
		}
	}

	// 下载逻辑一定要生成 sliceResultOption，否则无法进行下次查询
	if sliceResultOption == nil {
		err = fmt.Errorf("no result option found for tableID: %s, connect: %s", tableID, connect)
		return
	}

	var sliceStatus string
	if err != nil {
		sliceStatus = redis.StatusFailed
	}
	scrollHandler := instance.ScrollHandler()
	isCompleted := scrollHandler.IsCompleted(sliceResultOption, len(data))
	if isCompleted {
		sliceStatus = redis.StatusCompleted
	} else {
		sliceStatus = redis.StatusRunning
	}
	err = instance.ScrollHandler().UpdateScrollStatus(ctx, session, connect, tableID, sliceResultOption, sliceStatus)
	return
}

func generateScrollKey(name string, ts structured.QueryTs) (string, error) {
	ts.ClearCache = false
	key, err := json.StableMarshal(ts)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s:%s", name, key), nil
}

func generateScrollSliceStatusKey(args ...interface{}) string {
	var entries []string
	for _, arg := range args {
		if s, err := cast.ToStringE(arg); err == nil {
			entries = append(entries, s)
		} else {
			continue
		}
	}
	return strings.Join(entries, ":")
}
