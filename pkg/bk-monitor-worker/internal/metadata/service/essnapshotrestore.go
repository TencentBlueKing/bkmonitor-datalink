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
	"time"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
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
	if err := s.Update(db, updateFields...); err != nil {
		return 0, errors.Wrapf(err, "update restore [%v] failed", s.RestoreID)
	}
	return s.CompleteDocCount, nil

}

// 判断索引是否已经被回溯
func (s *EsSnapshotRestoreSvc) buildRestoreIndexName(indexName string) string {
	return fmt.Sprintf("%s%s", s.restoreIndexPrefix(), indexName)
}

// 索引前缀
func (s *EsSnapshotRestoreSvc) restoreIndexPrefix() string {
	return "restore_"
}
