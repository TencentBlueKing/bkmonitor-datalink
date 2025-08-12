// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

//
//type StorageScrollQuery struct {
//	QueryList []*metadata.Query
//	Instance  tsdb.Instance
//	Connect   string
//	TableID   string
//}
//
//func collectStorageScrollQuery(ctx context.Context, session *redis.ScrollSession, qryList []*metadata.Query) (list []StorageScrollQuery, err error) {
//	for _, qry := range qryList {
//		instance := prometheus.GetTsDbInstance(ctx, qry)
//		slices, mErr := instance.ScrollHandler().MakeSlices(ctx, session, qry.TableUUID())
//		if mErr != nil {
//			err = mErr
//			return
//		}
//		var injectedScrollQueryList []*metadata.Query
//		for _, slice := range slices {
//			qryCp, iErr := injectScrollQuery(qry, slice)
//			if iErr != nil {
//				err = iErr
//				return
//			}
//			injectedScrollQueryList = append(injectedScrollQueryList, qryCp)
//		}
//		list = append(list, StorageScrollQuery{
//			QueryList: injectedScrollQueryList,
//			Connect:   qry.StorageID,
//			Instance:  instance,
//			TableID:   qry.TableID,
//		})
//	}
//	return
//}
//
//func injectScrollQuery(qry *metadata.Query, sliceInfo *redis.SliceInfo) (*metadata.Query, error) {
//	qryCp := &metadata.Query{}
//	if err := copier.CopyWithOption(qryCp, qry, copier.Option{
//		DeepCopy: true,
//	}); err != nil {
//		return nil, err
//	}
//
//	if qryCp.ResultTableOptions == nil {
//		qryCp.ResultTableOptions = make(metadata.ResultTableOptions)
//	}
//
//	option := &metadata.ResultTableOption{
//		ScrollID:   sliceInfo.ScrollID,
//		SliceIndex: &sliceInfo.SliceIdx,
//		SliceMax:   &sliceInfo.SliceMax,
//		From:       &sliceInfo.Offset,
//	}
//
//	qryCp.ResultTableOptions.SetOption(qry.TableUUID(), option)
//
//	return qryCp, nil
//}
//
//func scrollQueryWorker(ctx context.Context, session *redis.ScrollSession, qry *metadata.Query, start time.Time, end time.Time, instance tsdb.Instance) (data []map[string]any, err error) {
//	dataCh := make(chan map[string]any)
//	wg := sync.WaitGroup{}
//	wg.Add(1)
//	go func() {
//		defer wg.Done()
//		for d := range dataCh {
//			data = append(data, d)
//		}
//	}()
//
//	_, option, err := instance.QueryRawData(ctx, qry, start, end, dataCh)
//	close(dataCh)
//	wg.Wait()
//
//	if option == nil {
//		option = qry.ResultTableOptions.GetOption(qry.TableUUID())
//		if option != nil {
//			option.ScrollID = ""
//		}
//	}
//
//	// 下载逻辑一定要生成 sliceResultOption，否则无法进行下次查询
//	if option == nil {
//		err = fmt.Errorf("no result option found for table_uuid: %s", qry.TableUUID())
//		return
//	}
//
//	var sliceStatus string
//	if err != nil {
//		sliceStatus = redis.StatusFailed
//	}
//	scrollHandler := instance.ScrollHandler()
//	isCompleted := scrollHandler.IsCompleted(option, len(data))
//	if isCompleted {
//		sliceStatus = redis.StatusCompleted
//	} else {
//		sliceStatus = redis.StatusRunning
//	}
//	err = instance.ScrollHandler().UpdateScrollStatus(ctx, session, qry.TableUUID(), option, sliceStatus)
//	return
//}
//
////func generateScrollKey(name string, ts structured.QueryTs) (string, error) {
////	ts.ClearCache = false
////	key, err := json.StableMarshal(ts)
////	if err != nil {
////		return "", err
////	}
////	return fmt.Sprintf("%s:%s", name, key), nil
////}
//
//func generateScrollSliceStatusKey(args ...interface{}) string {
//	var entries []string
//	for _, arg := range args {
//		if s, err := cast.ToStringE(arg); err == nil {
//			entries = append(entries, s)
//		} else {
//			continue
//		}
//	}
//	return strings.Join(entries, ":")
//}
