// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package service

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/diffutil"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/slicex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// EsSnapshotRestoreSvc es snapshot restore service
type EsSnapshotRestoreSvc struct {
	*storage.EsSnapshotRestore
}

func NewEsSnapshotRestoreSvc(obj *storage.EsSnapshotRestore) EsSnapshotRestoreSvc {
	return EsSnapshotRestoreSvc{
		EsSnapshotRestore: obj,
	}
}

// CleanAllExpiredRestore 清理到期的回溯索引
func (*EsSnapshotRestoreSvc) CleanAllExpiredRestore(ctx context.Context, goRoutineLimit int) error {
	db := mysql.GetDBSession().DB
	now := time.Now().UTC()
	var expiredRestores []storage.EsSnapshotRestore
	if err := storage.NewEsSnapshotRestoreQuerySet(db).ExpiredDeleteNe(true).IsDeletedNe(true).ExpiredTimeLte(now).All(&expiredRestores); err != nil {
		return errors.Wrap(err, "query expired restores failed")
	}
	wg := &sync.WaitGroup{}
	ch := make(chan bool, goRoutineLimit)
	wg.Add(len(expiredRestores))
	for _, restore := range expiredRestores {
		ch <- true
		go func(restore *storage.EsSnapshotRestore, wg *sync.WaitGroup, ch chan bool) {
			defer func() {
				<-ch
				wg.Done()
			}()
			svc := NewEsSnapshotRestoreSvc(restore)
			err := svc.DeleteRestoreIndices(ctx)
			if err != nil {
				logger.Errorf("clean expired restore [%v] failed, %v", restore.RestoreID, err)
				return
			}
			restore.ExpiredDelete = true
			if cfg.BypassSuffixPath != "" {
				logger.Info(diffutil.BuildLogStr("clean_expired_restore", diffutil.OperatorTypeDBUpdate, diffutil.NewSqlBody(restore.TableName(), map[string]interface{}{
					storage.EsSnapshotRestoreDBSchema.RestoreID.String():     restore.RestoreID,
					storage.EsSnapshotRestoreDBSchema.ExpiredDelete.String(): restore.ExpiredDelete,
				}), ""))
			} else {
				if err := restore.Update(db, storage.EsSnapshotRestoreDBSchema.ExpiredDelete); err != nil {
					logger.Errorf("update es snapshot restore [%v] expired_delete field to true failed, %v", restore.RestoreID, err)
					return
				}
			}

			logger.Infof("restore [%v] has expired, has be clean", restore.RestoreID)
		}(&restore, wg, ch)
	}
	wg.Wait()
	return nil
}

func (s *EsSnapshotRestoreSvc) DeleteRestoreIndices(ctx context.Context) error {
	if s.EsSnapshotRestore == nil {
		return errors.New("DeleteRestoreIndices EsSnapshotRestore obj can not be nil")
	}
	var restoreIndexList []string
	indices := strings.Split(s.Indices, ",")
	now := time.Now().UTC()
	for _, index := range indices {
		restored, err := s.isRestoredIndex(index, now, s.RestoreID)
		if err != nil {
			return errors.Wrap(err, "judge isRestoredIndex failed")
		}
		if !restored {
			restoreIndexList = append(restoreIndexList, s.buildRestoreIndexName(index))
		}
	}
	db := mysql.GetDBSession().DB
	var ess storage.ESStorage
	if err := storage.NewESStorageQuerySet(db).TableIDEq(s.TableID).One(&ess); err != nil {
		return errors.Wrap(err, "query es storage failed")
	}
	essSvc := NewEsStorageSvc(&ess)
	client, err := essSvc.GetESClient(ctx)
	if err != nil {
		return errors.Wrapf(err, "get es [%v] client failed", essSvc.StorageClusterID)
	}
	// es index 删除是通过url带参数 防止索引太多超过url长度限制 所以进行多批删除
	logger.Infof("restore [%v] need delete indices [%s]", s.RestoreID, strings.Join(restoreIndexList, ","))
	indexChunk := slicex.ChunkStringsBySize(&restoreIndexList, cfg.DefaultStringFilterSize, ",")
	for _, idxStr := range indexChunk {
		if cfg.BypassSuffixPath != "" {
			body, _ := jsonx.MarshalString(strings.Split(idxStr, ","))
			logger.Info(diffutil.BuildLogStr("clean_expired_restore", diffutil.OperatorTypeAPIDelete, diffutil.NewStringBody(body), ""))
		} else {
			resp, err := client.DeleteIndex(ctx, strings.Split(idxStr, ","))
			if err != nil {
				logger.Errorf("restore [%v] delete indices [%s] failed, %v", s.RestoreID, idxStr, err)
				continue
			}
			resp.Close()
		}
		logger.Infof("restore [%v] has delete indices [%s]", s.RestoreID, idxStr)
	}
	logger.Infof("restore [%v] has clean complete maybe expired or delete", s.RestoreID)
	return nil
}

// 判断索引是否已经被回溯
func (s *EsSnapshotRestoreSvc) isRestoredIndex(index string, now time.Time, restoreId int) (bool, error) {
	db := mysql.GetDBSession().DB
	qs := storage.NewEsSnapshotRestoreQuerySet(db).IsDeletedNe(true).ExpiredDeleteNe(true)
	if restoreId != 0 {
		qs = qs.RestoreIDNe(restoreId)
	}
	count, err := qs.IndicesLike(fmt.Sprintf("%%s%s%%s", index)).ExpiredTimeGt(now).Count()
	if err != nil {
		return false, errors.Wrap(err, "query expired index failed")
	}
	if count != 0 {
		return true, nil
	}
	return false, nil
}

// 判断索引是否已经被回溯
func (s *EsSnapshotRestoreSvc) buildRestoreIndexName(indexName string) string {
	return fmt.Sprintf("%s%s", s.restoreIndexPrefix(), indexName)
}

// 索引前缀
func (s *EsSnapshotRestoreSvc) restoreIndexPrefix() string {
	return "restore_"
}

func (s *EsSnapshotRestoreSvc) GetCompleteDocCount(ctx context.Context) (int, error) {
	if s.EsSnapshotRestore == nil {
		return 0, errors.New("isRestoredIndex EsSnapshotRestore obj can not be nil")
	}
	//  avoid es count api return data inconsistency
	if s.CompleteDocCount >= s.TotalDocCount {
		return s.TotalDocCount, nil
	}
	db := mysql.GetDBSession().DB
	var ess storage.ESStorage
	if err := storage.NewESStorageQuerySet(db).TableIDEq(s.TableID).One(&ess); err != nil {
		return 0, errors.Wrapf(err, "query es storage for table_id [%s] failed", s.TableID)
	}
	esClient, err := ess.GetESClient(ctx)
	if err != nil {
		return 0, errors.Wrapf(err, "get es client for es storage cluster [%v] failed", ess.StorageClusterID)
	}
	searchIndexList := []string{fmt.Sprintf("%s*", s.restoreIndexPrefix())}
	resp, err := esClient.CatIndices(ctx, searchIndexList, "json")
	if err != nil {
		return 0, errors.Wrapf(err, "cat indices [%v] from es storage cluster [%v] failed", searchIndexList, ess.StorageClusterID)
	}
	defer resp.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, errors.Wrapf(err, "read respose body for CatIndices [%v] failed", searchIndexList)
	}
	var indicesInfos []map[string]string
	err = jsonx.Unmarshal(body, &indicesInfos)
	if err != nil {
		return 0, errors.Wrapf(err, "unmarshal indices info [%s] failed", body)
	}
	indexNameInfoMap := make(map[string]map[string]string)
	for _, info := range indicesInfos {
		indexNameInfoMap[info["index"]] = info
	}
	indexNameList := strings.Split(s.Indices, ",")
	var completeDocCount int
	for _, indexName := range indexNameList {
		restoreIndexName := s.buildRestoreIndexName(indexName)
		indexInfo, ok := indexNameInfoMap[restoreIndexName]
		if !ok {
			continue
		}
		docsCountStr := indexInfo["docs.count"]
		docsCount, err := strconv.Atoi(docsCountStr)
		if err != nil {
			logger.Errorf("conver docs.count [%s] to int failed, %v", docsCountStr, err)
			continue
		}
		completeDocCount += docsCount
	}
	var updateFields []storage.EsSnapshotRestoreDBSchemaField
	if s.TotalDocCount <= completeDocCount {
		s.Duration = int(time.Now().Sub(s.CreateTime).Seconds())
		updateFields = append(updateFields, storage.EsSnapshotRestoreDBSchema.Duration)
		logger.Infof("restore [%v] restore complete duration [%v]", s.RestoreID, s.Duration)
	}
	s.CompleteDocCount = completeDocCount
	s.LastModifyTime = time.Now()
	updateFields = append(updateFields, storage.EsSnapshotRestoreDBSchema.CompleteDocCount, storage.EsSnapshotRestoreDBSchema.LastModifyTime)
	if cfg.BypassSuffixPath != "" {
		logger.Info(diffutil.BuildLogStr("refresh_es_restore", diffutil.OperatorTypeDBUpdate, diffutil.NewSqlBody(s.TableName(), map[string]interface{}{
			storage.EsSnapshotRestoreDBSchema.RestoreID.String():        s.RestoreID,
			storage.EsSnapshotRestoreDBSchema.CompleteDocCount.String(): s.CompleteDocCount,
		}), ""))
	} else {
		if err := s.Update(db, updateFields...); err != nil {
			return 0, errors.Wrapf(err, "update restore [%v] failed", s.RestoreID)
		}
	}
	return s.CompleteDocCount, nil

}
