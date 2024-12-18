// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jinzhu/gorm"
	"github.com/pkg/errors"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/elasticsearch"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/diffutil"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/slicex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/timex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

//go:generate goqueryset -in esstorage.go -out qs_esstorage_gen.go

// ESStorage es storage model
// gen:qs
type ESStorage struct {
	TableID           string                       `json:"table_id" gorm:"primary_key;size:128"`
	DateFormat        string                       `json:"date_format" gorm:"size:64"`
	SliceSize         uint                         `json:"slice_size" gorm:"column:slice_size"`
	SliceGap          int                          `json:"slice_gap" gorm:"column:slice_gap"`
	Retention         int                          `json:"retention" gorm:"column:retention"`
	WarmPhaseDays     int                          `json:"warm_phase_days" gorm:"column:warm_phase_days"`
	WarmPhaseSettings string                       `json:"warm_phase_settings" gorm:"warm_phase_settings"`
	TimeZone          int8                         `json:"time_zone" gorm:"column:time_zone"`
	IndexSettings     string                       `json:"index_settings" gorm:"index_settings"`
	MappingSettings   string                       `json:"mapping_settings" gorm:"mapping_settings"`
	StorageClusterID  uint                         `json:"storage_cluster_id" gorm:"storage_cluster_id"`
	SourceType        string                       `json:"source_type" gorm:"column:source_type"`
	IndexSet          string                       `json:"index_set" gorm:"column:index_set"`
	NeedCreateIndex   bool                         `json:"need_create_index" gorm:"column:need_create_index;default:true"`
	esClient          *elasticsearch.Elasticsearch `gorm:"-"`
}

// TableName 用于设置表的别名
func (ESStorage) TableName() string {
	return "metadata_esstorage"
}

// BeforeCreate 默认值
func (b *ESStorage) BeforeCreate(tx *gorm.DB) error {
	if b.DateFormat == "" {
		b.DateFormat = "%Y%m%d%H"
	}
	if b.SliceSize == 0 {
		b.SliceSize = 500
	}
	if b.SliceGap == 0 {
		b.SliceGap = 120
	}

	return nil
}

// GetDateFormat 解析python日期格式化字符串返回go类型的格式化字符串
func (e *ESStorage) GetDateFormat() string {
	return timex.ParsePyDateFormat(e.DateFormat)
}

// GetESClient 获取ES客户端
func (e *ESStorage) GetESClient(ctx context.Context) (*elasticsearch.Elasticsearch, error) {
	if e.esClient != nil {
		return e.esClient, nil
	}
	dbSession := mysql.GetDBSession()
	var esClusterInfo ClusterInfo
	if err := NewClusterInfoQuerySet(dbSession.DB).ClusterIDEq(e.StorageClusterID).One(&esClusterInfo); err != nil {
		logger.Errorf("find es storage record [%v] error, %v", e.StorageClusterID, err)
		return nil, err
	}

	client, err := esClusterInfo.GetESClient(ctx)
	if err != nil {
		logger.Errorf("cluster [%v] get es client error, %v", e.StorageClusterID, err)
		return nil, err
	}
	e.esClient = client
	return client, nil
}

// ManageESStorage es_storage生命周期管理
func (e *ESStorage) ManageESStorage(ctx context.Context) error {
	exist, err := e.CheckIndexExist(ctx)
	if err != nil {
		logger.Errorf("es_storage [%s] judge index error: [%v]", e.TableID, err)
		return err
	}
	if !exist {
		// 如果该table_id的index在es中不存在，则走初始化流程
		logger.Infof("table_id [%s] found no index in es, will create new one", e.TableID)
		err := e.CreateIndexAndAliases(ctx, e.SliceGap)
		if err != nil {
			logger.Errorf("table_id [%s] create index and alias error, %v", e.TableID, err)
			return err
		}
	} else {
		// 否则走更新流程
		err := e.UpdateIndexAndAliases(ctx, e.SliceGap)
		if err != nil {
			logger.Errorf("table_id [%s] update index and alias error, %v", e.TableID, err)
			return err
		}
	}

	// 创建快照
	err = e.CreateSnapshot(ctx)
	if err != nil {
		logger.Errorf("table_id [%s] create snapshot error, %v", e.TableID, err)
		return err
	}
	// 清理过期的index
	err = e.CleanIndexV2(ctx)
	if err != nil {
		logger.Errorf("table_id [%s] clean index error, %v", e.TableID, err)
		return err
	}
	//# 清理过期快照
	err = e.CleanSnapshot(ctx)
	if err != nil {
		logger.Errorf("table_id [%s] clean snapshot error, %v", e.TableID, err)
		return err
	}

	//# 重新分配索引数据
	err = e.ReallocateIndex(ctx)
	if err != nil {
		logger.Errorf("table_id [%s] reallocate index error, %v", e.TableID, err)
		return err
	}

	return nil
}

// CreateIndexAndAliases 创建索引和别名
func (e *ESStorage) CreateIndexAndAliases(ctx context.Context, aheadTime int) error {
	err := e.CreateIndexV2(ctx)
	if err != nil {
		return err
	}
	err = e.CreateOrUpdateAliases(ctx, aheadTime)
	if err != nil {
		return err
	}
	return nil
}

// UpdateIndexAndAliases 更新索引和别名
func (e *ESStorage) UpdateIndexAndAliases(ctx context.Context, aheadTime int) error {
	err := e.UpdateIndexV2(ctx)
	if err != nil {
		return err
	}
	err = e.CreateOrUpdateAliases(ctx, aheadTime)
	if err != nil {
		return err
	}

	return nil
}

// CheckIndexExist 判断索引是否存在
func (e *ESStorage) CheckIndexExist(ctx context.Context) (bool, error) {

	// 优先查询V2类型索引
	existV2, err := e.isIndexExist(ctx, e.searchFormatV2(), e.IndexReV2())
	if err != nil {
		return false, err
	}
	if existV2 {
		return true, nil
	}

	// 再查询V1类型索引
	existV1, err := e.isIndexExist(ctx, e.searchFormatV1(), e.IndexReV1())
	if err != nil {
		return false, err
	}
	if existV1 {
		return true, nil
	}

	return false, nil
}

// indexExist 判断索引是否存在
func (e *ESStorage) isIndexExist(ctx context.Context, searchFormat string, matchRe *regexp.Regexp) (bool, error) {
	client, err := e.GetESClient(ctx)
	if err != nil {
		return false, err
	}
	resp, err := client.GetIndices([]string{searchFormat})
	if err != nil {
		if errors.Is(err, elasticsearch.NotFoundErr) {
			return false, nil
		}
		return false, err
	}
	defer resp.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}
	var indicesResult map[string]interface{}
	err = jsonx.Unmarshal(body, &indicesResult)
	if err != nil {
		return false, err
	}
	for indexName := range indicesResult {
		if matchRe.MatchString(indexName) {
			return true, nil
		}
	}
	return false, nil
}

// IndexName 索引名
func (e *ESStorage) IndexName() string {
	return strings.ReplaceAll(e.TableID, ".", "_")
}

// searchFormatV1 索引查询V1
func (e *ESStorage) searchFormatV1() string {
	return fmt.Sprintf("%s_*", e.IndexName())
}

// searchFormatV2 索引查询V2
func (e *ESStorage) searchFormatV2() string {
	return fmt.Sprintf("v2_%s_*", e.IndexName())
}

// IndexReV1 获取这个存储的V1正则匹配
func (e *ESStorage) IndexReV1() *regexp.Regexp {
	pattern := fmt.Sprintf(`%s_(?P<datetime>\d+)_(?P<index>\d+)`, e.IndexName())
	return regexp.MustCompile(pattern)
}

// IndexReV2 获取这个存储的V2正则匹配
func (e *ESStorage) IndexReV2() *regexp.Regexp {
	pattern := fmt.Sprintf(`v2_%s_(?P<datetime>\d+)_(?P<index>\d+)$`, e.IndexName())
	return regexp.MustCompile(pattern)
}

// SnapshotRe 获取这个存储快照的正则匹配
func (e *ESStorage) SnapshotRe() *regexp.Regexp {
	return regexp.MustCompile(fmt.Sprintf(`^%s_snapshot_(?P<datetime>\d+)$`, e.IndexName()))
}

// WriteAliasRe 获取这个存储的写入别名正则匹配
func (e *ESStorage) WriteAliasRe() *regexp.Regexp {
	return regexp.MustCompile(fmt.Sprintf(`write_(?P<datetime>\d+)_%s`, e.IndexName()))
}

// ReadAliasRe 获取这个存储的读别名正则匹配
func (e *ESStorage) ReadAliasRe() *regexp.Regexp {
	return regexp.MustCompile(fmt.Sprintf(`%s_(?P<datetime>\d+)_read`, e.IndexName()))
}

// OldWriteAliasRe 获取这个存储的旧版写入别名正则匹配
func (e *ESStorage) OldWriteAliasRe() *regexp.Regexp {
	return regexp.MustCompile(fmt.Sprintf(`%s_(?P<datetime>\d+)_write`, e.IndexName()))
}

// SearchSnapshot 查询snapshot的通配符字符串
func (e *ESStorage) SearchSnapshot() string {
	return fmt.Sprintf("%s_snapshot_*", e.IndexName())
}

// SnapshotDateFormat 快照日期格式
func (e *ESStorage) SnapshotDateFormat() string {
	return "20060102"
}

// RestoreIndexPrefix restore索引前缀
func (e *ESStorage) RestoreIndexPrefix() string {
	return "restore_"
}

// MakeIndexName 构造index名
func (e *ESStorage) MakeIndexName(zoneTime *time.Time, index uint, version string) string {
	dateStr := zoneTime.Format(e.GetDateFormat())
	if version == "v2" {
		return fmt.Sprintf("v2_%s_%s_%v", e.IndexName(), dateStr, index)
	}
	return fmt.Sprintf("%s_%s_%v", e.IndexName(), dateStr, index)
}

// MakeSnapshotName 构造snapshot名
func (e *ESStorage) MakeSnapshotName(now time.Time, indexName string) string {
	return fmt.Sprintf("%s_snapshot_%s", indexName, now.Format(e.SnapshotDateFormat()))
}

// Now 返回调整时区后的time对象
func (e *ESStorage) Now() time.Time {
	utcTime := time.Now().UTC()
	return utcTime.Add(time.Duration(e.TimeZone) * time.Hour)
}

// CreateIndexV2 创建索引
func (e *ESStorage) CreateIndexV2(ctx context.Context) error {
	enabled, err := e.IsIndexEnable()
	if err != nil {
		return err
	}
	if !enabled {
		return errors.Errorf("es storage is disabled or deleted")
	}

	nowTime := e.Now()
	client, err := e.GetESClient(ctx)
	if err != nil {
		return err
	}
	indexName := e.MakeIndexName(&nowTime, 0, "v2")
	body, err := e.IndexBody()
	if err != nil {
		logger.Errorf("table_id [%s] make index body error, %v", e.TableID, err)
		return err
	}
	logger.Infof("table_id [%s] create index body [%s]", e.TableID, string(body))
	metrics.ESChangeCount(e.TableID, "CreateIndex")
	if cfg.BypassSuffixPath != "" && !slicex.IsExistItem(cfg.SkipBypassTasks, "refresh_es_storage") {
		logger.Info(diffutil.BuildLogStr("refresh_es_storage", diffutil.OperatorTypeAPIPut, diffutil.NewStringBody(string(body)), ""))
	} else {
		resp, err := client.CreateIndex(ctx, indexName, bytes.NewReader(body))
		if err != nil {
			logger.Errorf("table_id [%s] create index error, %v", e.TableID, err)
			return err
		}
		defer resp.Close()
	}
	logger.Infof("table_id [%s] has created new index [%s]", e.TableID, indexName)
	return nil
}

// UpdateIndexV2 判断index是否需要分裂，并提前建立index别名的功能
// 此处仍然保留每个小时创建新的索引，主要是为了在发生异常的时候，可以降低影响的索引范围（最多一个小时）
func (e *ESStorage) UpdateIndexV2(ctx context.Context) error {
	enabled, err := e.IsIndexEnable()
	if err != nil {
		return err
	}
	if !enabled {
		return errors.Errorf("es storage is disabled or deleted")
	}
	nowTimeObj := e.Now()
	client, err := e.GetESClient(ctx)
	if err != nil {
		return err
	}
	indexInfo, err := e.CurrentIndexInfo(ctx)
	if err != nil {
		if errors.Is(err, elasticsearch.NotFoundErr) {
			return e.CreateIndexV2(ctx)
		}
		return err
	}

	lastIndexName := e.MakeIndexName(indexInfo.TimeObject, indexInfo.Index, indexInfo.IndexVersion)
	indexSizeInByte := indexInfo.size

	// 兼容旧任务，将不合理的超前index清理掉
	// 如果最新时间超前了，要对应处理一下,通常发生在旧任务应用新的es代码过程中
	// 循环处理，以应对预留时间被手动加长,导致超前index有多个的场景
	for indexInfo.TimeObject.After(nowTimeObj) {
		logger.Warnf("table_id [%s] delete index [%s] because it has ahead time", e.TableID, lastIndexName)
		err := func() error {
			if cfg.BypassSuffixPath != "" && !slicex.IsExistItem(cfg.SkipBypassTasks, "refresh_es_storage") {
				logger.Info(diffutil.BuildLogStr("refresh_es_storage", diffutil.OperatorTypeAPIDelete, diffutil.NewStringBody(lastIndexName), ""))
			} else {
				resp, err := client.DeleteIndex(ctx, []string{lastIndexName})
				if err != nil {
					return err
				}
				defer resp.Close()
			}
			return nil
		}()
		if err != nil {
			return err
		}

		// 重新获取最新的index
		indexInfo, err = e.CurrentIndexInfo(ctx)
		lastIndexName = e.MakeIndexName(indexInfo.TimeObject, indexInfo.Index, indexInfo.IndexVersion)
	}

	// 判断index是否需要分割
	shouldCreate := false
	if uint(indexSizeInByte/1024/1024/1024) > e.SliceSize {
		logger.Infof(
			"table_id [%s] index [%s] current_size [%v] is larger than slice size [%v], create new index slice",
			e.TableID, lastIndexName, indexSizeInByte, e.SliceSize,
		)
		shouldCreate = true
	}

	//  mapping 不一样，需要创建新的index
	isSame, err := e.IsMappingSame(ctx, lastIndexName)
	if err != nil {
		return err
	}
	if !isSame {
		logger.Infof("table_id [%s] index [%s] mapping is not the same, will create the new", e.TableID, lastIndexName)
		shouldCreate = true
	}

	// 达到保存期限进行分裂
	expiredTimePoint := e.Now().Add(-time.Duration(e.Retention) * time.Hour * 24)
	if expiredTimePoint.After(*indexInfo.TimeObject) {
		logger.Infof("table_id [%s] index [%s] has arrive retention date, will create the new", e.TableID, lastIndexName)
		shouldCreate = true
	}

	// arrive warm_phase_days date to split index
	// avoid index always not split, it is not be allocated to cold node
	if e.WarmPhaseDays > 0 {
		expiredTimePoint = e.Now().Add(-time.Duration(e.WarmPhaseDays) * time.Hour * 24)
		if expiredTimePoint.After(*indexInfo.TimeObject) {
			logger.Infof("table_id->[%s] index->[%s] has arrive warm_phase_days date, will create the new", e.TableID, lastIndexName)
			shouldCreate = true
		}
	}

	// 判断新的index信息：日期以及对应的index
	if !shouldCreate {
		logger.Infof("table_id [%s] index [%s] everything is ok, need not to split index", e.TableID, lastIndexName)
		return nil
	}

	var newIndex uint
	if nowTimeObj.Format(e.GetDateFormat()) == indexInfo.TimeObject.Format(e.GetDateFormat()) {
		// 如果当前index并没有写入过数据(count==0),则对其进行删除重建操作即可
		resp, err := client.CountByIndex(ctx, []string{lastIndexName})
		if err != nil {
			return err
		}
		defer resp.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		var count elasticsearch.CountResp
		err = jsonx.Unmarshal(body, &count)
		if err != nil {
			return err
		}
		if count.Count == 0 {
			newIndex = indexInfo.Index
			if cfg.BypassSuffixPath != "" && !slicex.IsExistItem(cfg.SkipBypassTasks, "refresh_es_storage") {
				logger.Info(diffutil.BuildLogStr("refresh_es_storage", diffutil.OperatorTypeAPIDelete, diffutil.NewStringBody(lastIndexName), ""))
			} else {
				resp, err := client.DeleteIndex(ctx, []string{lastIndexName})
				if err != nil {
					return err
				}
				defer resp.Close()
			}
			logger.Infof(
				"table_id [%s] has index [%s] which has not data, will be deleted for new index create.", e.TableID, lastIndexName,
			)
		} else {
			newIndex = indexInfo.Index + 1
			logger.Infof("table_id [%s] index->[%v] has data, so new index will create", e.TableID, newIndex)
		}
	}
	newIndexName := e.MakeIndexName(&nowTimeObj, newIndex, "v2")
	logger.Infof("table_id [%s] will create new index [%s]", e.TableID, newIndexName)
	// 创建新的index
	payload, err := e.IndexBody()
	logger.Infof("table_id [%s] create new index [%s] with body [%s]", e.TableID, newIndexName, string(payload))
	if err != nil {
		return err
	}
	metrics.ESChangeCount(e.TableID, "CreateIndex")
	if cfg.BypassSuffixPath != "" && !slicex.IsExistItem(cfg.SkipBypassTasks, "refresh_es_storage") {
		logger.Info(diffutil.BuildLogStr("refresh_es_storage", diffutil.OperatorTypeAPIPut, diffutil.NewStringBody(lastIndexName), ""))
	} else {
		resp, err := client.CreateIndex(ctx, newIndexName, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		defer resp.Close()
	}
	logger.Infof("table_id [%s] new index_name [%s] is created now", e.TableID, newIndexName)
	return nil
}

// CreateOrUpdateAliases 更新alias，如果有已存在的alias，则将其指向最新的index，并根据ahead_time前向预留一定的alias
func (e *ESStorage) CreateOrUpdateAliases(ctx context.Context, aheadTime int) error {
	client, err := e.GetESClient(ctx)
	if err != nil {
		return err
	}
	currentIndexInfo, err := e.CurrentIndexInfo(ctx)
	if err != nil {
		return err
	}
	lastIndexName := e.MakeIndexName(currentIndexInfo.TimeObject, currentIndexInfo.Index, currentIndexInfo.IndexVersion)
	nowTimeObject := e.Now()

	var nowGap int
	indexName := e.IndexName()
	for nowGap <= aheadTime {
		roundTime := nowTimeObject.Add(time.Duration(nowGap) * time.Minute)
		roundTimeStr := roundTime.Format(e.GetDateFormat())
		roundAliasName := fmt.Sprintf("write_%s_%s", roundTimeStr, indexName)
		roundReadAliasName := fmt.Sprintf("%s_%s_read", indexName, roundTimeStr)

		err := func() error {
			// 判断这个别名是否有指向旧的index，如果存在则需要解除
			var deleteList []string
			resp, err := client.GetAlias(ctx, roundAliasName)
			if err != nil {
				if errors.Is(err, elasticsearch.NotFoundErr) {
					// 没有找到指定索引别名，可能是在创建未来的alias，所以不一定会有别名关联的index
					logger.Infof("table_id [%s] alias_name [%s] not found index relay, will not delete any thing.", e.TableID, roundAliasName)
				} else {
					return err
				}
			} else {
				defer resp.Close()
				var indexAlias elasticsearch.IndexAlias
				payload, err := io.ReadAll(resp.Body)
				if err != nil {
					return err
				}
				err = jsonx.Unmarshal(payload, &indexAlias)
				if err != nil {
					return err
				}
				for aliasIndex := range indexAlias {
					if aliasIndex != lastIndexName {
						deleteList = append(deleteList, aliasIndex)
					}
				}
			}

			// 需要将循环中的别名都指向最新的index
			updateJson := fmt.Sprintf(
				`{"actions": [{"add": {"index": "%s", "alias": "%s"}},{"add": {"index": "%s", "alias": "%s"}}]}`,
				lastIndexName, roundAliasName, lastIndexName, roundReadAliasName,
			)
			if cfg.BypassSuffixPath != "" && !slicex.IsExistItem(cfg.SkipBypassTasks, "refresh_es_storage") {
				logger.Info(diffutil.BuildLogStr("refresh_es_storage", diffutil.OperatorTypeAPIPost, diffutil.StringBody{Body: updateJson}, ""))
			} else {
				resp, err = client.UpdateAlias(ctx, strings.NewReader(updateJson))
				if err != nil {
					logger.Errorf("table_id [%s] update alias [%s] error, %s", e.TableID, updateJson, err)
					return err
				}
				defer resp.Close()
			}
			logger.Infof("table_id [%s] now has index [%s] and alias [%s | %s]", e.TableID, lastIndexName, roundAliasName, roundReadAliasName)
			// 只有当index相关列表不为空的时候，进行别名关联清理
			if len(deleteList) != 0 {
				logger.Infof(
					"table_id [%s] found alias_name [%s] is relay with index [%v] all will be deleted.", e.TableID, roundAliasName, deleteList,
				)
				if cfg.BypassSuffixPath != "" && !slicex.IsExistItem(cfg.SkipBypassTasks, "refresh_es_storage") {
					logger.Info(diffutil.BuildLogStr("refresh_es_storage", diffutil.OperatorTypeAPIDelete, diffutil.NewStringBody(roundAliasName), ""))
				} else {
					resp, err := client.DeleteAlias(ctx, deleteList, []string{roundAliasName})
					if err != nil {
						return err
					}
					defer resp.Close()
				}
				logger.Infof(
					"table_id [%s] index [%v] alias [%s] relations now had delete.", e.TableID, deleteList, roundAliasName,
				)
			}
			return nil
		}()
		if err != nil {
			logger.Errorf("operations for index [%s] gap [%v] error, %v", e.TableID, nowGap, err)
		} else {
			logger.Infof("all operations for index [%s] gap [%v] now is done.", e.TableID, nowGap)
		}
		if e.SliceGap <= 0 {
			return nil
		}
		nowGap += e.SliceGap
	}

	return nil
}

// CurrentIndexInfo  返回当前使用的最新index相关的信息
func (e *ESStorage) CurrentIndexInfo(ctx context.Context) (*CurrentIndexInfo, error) {
	indexStat, err := e.GetIndexStat(ctx, e.searchFormatV2())
	if err != nil {
		return nil, err
	}
	var indexVersion string
	var indexRe *regexp.Regexp
	// 查找index，找不到v2的就找v1的
	if len(indexStat.Indices) != 0 {
		indexVersion = "v2"
		indexRe = e.IndexReV2()
	} else {
		indexStat, err = e.GetIndexStat(ctx, e.searchFormatV1())
		if err != nil {
			return nil, err
		}
		if len(indexStat.Indices) != 0 {
			indexVersion = "v1"
			indexRe = e.IndexReV1()
		}
	}
	// 如果index_re为空，说明没找到任何可用的index
	if indexVersion == "" {
		return nil, errors.Errorf("index [%s] has no index now", e.IndexName())
	}
	// 1.1 判断获取最新的index
	var maxIndex int
	var maxDatetimeObject *time.Time
	for statIndexName := range indexStat.Indices {
		matchResult := indexRe.FindStringSubmatch(statIndexName)
		if len(matchResult) == 0 {
			logger.Warnf("index [%s] is not match re, maybe something goes wrong?", statIndexName)
			continue
		}
		currentIndex, err := strconv.Atoi(matchResult[2])
		if err != nil {
			return nil, err
		}
		currentDatetimeStr := matchResult[1]
		currentTime := timex.TimeStrToTime(currentDatetimeStr, e.GetDateFormat(), e.TimeZone)
		logger.Infof(
			"current index info going to detect index [%s] datetime [%s] count [%v]", statIndexName, currentDatetimeStr, currentIndex,
		)
		// 初始化轮，直接赋值
		if maxDatetimeObject == nil {
			maxIndex = currentIndex
			maxDatetimeObject = currentTime
			logger.Debugf(
				"index [%s] current round is init round, will use datetime [%s] and count [%v]", statIndexName, currentDatetimeStr, currentIndex,
			)
			continue
		}
		// 当时间较大的时候，直接赋值使用
		if currentTime.After(*maxDatetimeObject) {
			maxDatetimeObject = currentTime
			maxIndex = currentIndex
			logger.Debugf("index [%s] current time [%s] is newer than max time [%s] will use it and reset count [%v]",
				statIndexName,
				currentDatetimeStr,
				maxDatetimeObject.Format(e.GetDateFormat()),
				currentIndex,
			)
			continue
		}
		//  判断如果时间一致且index较大，需要更新替换
		if currentTime.Equal(*maxDatetimeObject) && currentIndex > maxIndex {
			maxIndex = currentIndex
			logger.Debugf(
				"index [%s] current time [%s] found newer index [%v] will use it", statIndexName, maxDatetimeObject.Format(e.GetDateFormat()), currentIndex,
			)
		}
	}
	if maxDatetimeObject == nil {
		return nil, errors.Errorf("index [%s] can not find current index datetime", e.IndexName())
	}
	index := e.MakeIndexName(maxDatetimeObject, uint(maxIndex), indexVersion)
	size := indexStat.Indices[index].Primaries.Store.SizeInBytes

	currentIndexInfo := &CurrentIndexInfo{
		IndexVersion: indexVersion,
		TimeObject:   maxDatetimeObject,
		Index:        uint(maxIndex),
		size:         size,
	}
	return currentIndexInfo, nil
}

// GetIndexStat 从es中获取索引当前状态
func (e *ESStorage) GetIndexStat(ctx context.Context, index string) (*elasticsearch.IndexStat, error) {
	client, err := e.GetESClient(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := client.IndexStat(ctx, index, []string{})
	if err != nil {
		return nil, err
	}
	defer resp.Close()
	var indexStat elasticsearch.IndexStat
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	err = jsonx.Unmarshal(body, &indexStat)
	if err != nil {
		return nil, err
	}
	return &indexStat, nil
}

// IsIndexEnable 判断索引是否启用中
func (e *ESStorage) IsIndexEnable() (bool, error) {
	dbSession := mysql.GetDBSession()
	count, err := resulttable.NewResultTableQuerySet(dbSession.DB).TableIdEq(e.TableID).IsEnableEq(true).IsDeletedEq(false).Count()
	if err != nil {
		logger.Errorf("query result table [%s] error, %v", e.TableID, err)
		return false, err
	}
	if count == 0 {
		logger.Infof("table_id [%s] now is deleted or disable, no index will create.", e.TableID)
		return false, nil
	}
	// 同时需要判断这个结果表是否可能遗留自定义事件上报，需要考虑自定义上报已经关闭了
	var isEnable, isDelete bool
	if err := mysql.GetDBSession().DB.Raw(`select is_enable, is_delete from metadata_eventgroup where table_id = ?`, e.TableID).
		Row().Scan(&isEnable, &isDelete); err != nil {
		// 如果查找失败，那么这个存储是日志平台，而且rt没有被删除或废弃，需要继续建立index
		logger.Infof("table_id [%s] belong to log search, will create it", e.TableID)
		return true, nil
	}
	// 查找发现1.这个es存储是归属于自定义事件的，而且 2.未启动或被删除的，那么不需要创建这个索引
	if !isEnable || isDelete {
		logger.Infof("table_id [%s] is belong to event group and is disable or deleted, no index will create", e.TableID)
		return false, nil
	}

	return true, nil
}

// IndexBody ES创建索引的配置内容
func (e *ESStorage) IndexBody() ([]byte, error) {
	// 构造index配置
	configJson := fmt.Sprintf(`{"settings": %s, "mappings": %s}`, e.IndexSettings, e.MappingSettings)
	var indexConfig IndexConfig
	err := jsonx.UnmarshalString(configJson, &indexConfig)
	if err != nil {
		return nil, err
	}

	properties, err := e.MakeIndexConfigMappingsProperties()
	if err != nil {
		return nil, err
	}
	indexConfig.Mappings["properties"] = properties

	if e.GetEsVersion() < models.ESRemoveTypeVersion {
		indexConfig.Mappings = map[string]interface{}{e.TableID: indexConfig.Mappings}
	}
	body, err := jsonx.Marshal(indexConfig)
	if err != nil {
		return nil, err
	}
	return body, nil
}

// GetEsVersion 获取es存储版本
func (e ESStorage) GetEsVersion() string {
	dbSession := mysql.GetDBSession()
	qs := NewClusterInfoQuerySet(dbSession.DB).ClusterIDEq(e.StorageClusterID)
	var esClusterInfo ClusterInfo
	if err := qs.One(&esClusterInfo); err != nil {
		return "7"
	}
	var version string
	if esClusterInfo.Version != nil {
		version = *esClusterInfo.Version
	}
	return strings.Split(version, ".")[0]

}

// MakeIndexConfigMappingsProperties 生成索引的mappings - properties
func (e *ESStorage) MakeIndexConfigMappingsProperties() (map[string]map[string]interface{}, error) {
	var properties = make(map[string]map[string]interface{})

	dbSession := mysql.GetDBSession()
	// 获取rt表所有的field
	var resultTableFieldList []resulttable.ResultTableField
	if err := resulttable.NewResultTableFieldQuerySet(dbSession.DB).TableIDEq(e.TableID).All(&resultTableFieldList); err != nil {
		return nil, err
	}
	if len(resultTableFieldList) == 0 {
		return properties, nil
	}
	var resultTableFieldNames []string
	for _, field := range resultTableFieldList {
		resultTableFieldNames = append(resultTableFieldNames, field.FieldName)
	}

	var rtFieldOptions []resulttable.ResultTableFieldOption
	// 获取field的所有option
	if err := resulttable.NewResultTableFieldOptionQuerySet(dbSession.DB).TableIDEq(e.TableID).
		FieldNameIn(resultTableFieldNames...).All(&rtFieldOptions); err != nil {
		return nil, err
	}
	for _, option := range rtFieldOptions {
		if !strings.HasPrefix(option.Name, "es") {
			continue
		}
		realName := option.Name[3:]
		interfaceValue, err := option.InterfaceValue()
		if err != nil {
			return nil, err
		}
		optionsMap, ok := properties[option.FieldName]
		if ok {
			optionsMap[realName] = interfaceValue
		} else {
			properties[option.FieldName] = map[string]interface{}{realName: interfaceValue}
		}
	}

	return properties, nil
}

// IsMappingSame 判断es中index的配置是否和数据库的一致
func (e *ESStorage) IsMappingSame(ctx context.Context, indexName string) (bool, error) {
	client, err := e.GetESClient(ctx)
	if err != nil {
		return false, err
	}
	// 判断最后一个index的配置是否和数据库的一致，如果不是，表示需要重建
	resp, err := client.GetIndexMapping(ctx, []string{indexName})
	if err != nil {
		return false, err
	}
	defer resp.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		if errors.Is(err, elasticsearch.NotFoundErr) {
			logger.Infof("index_name [%s] is not exists, will think the mapping is not same.", indexName)
			return false, nil
		}
		return false, err
	}
	defer resp.Close()
	var mappingConfig map[string]*IndexConfig
	err = jsonx.Unmarshal(body, &mappingConfig)
	if err != nil {
		return false, err
	}
	config, ok := mappingConfig[indexName]
	if !ok {
		logger.Infof("index_name [%s] is not exists, will think the mapping is not same.", indexName)
		return false, nil
	}
	var currentPropertiesInterface interface{}
	if info, ok := config.Mappings[e.TableID]; ok {
		m := info.(map[string]interface{})
		currentPropertiesInterface = m["properties"]
	} else {
		currentPropertiesInterface = config.Mappings["properties"]
	}
	if currentPropertiesInterface == nil {
		logger.Infof("index_name [%s] is not exists, will think the mapping is not same.", indexName)
		return false, nil
	}
	currentProperties := currentPropertiesInterface.(map[string]interface{})
	var dbPropertiesInterface interface{}
	var indexConfig IndexConfig
	bodyBytes, err := e.IndexBody()
	if err != nil {
		return false, err
	}
	err = jsonx.Unmarshal(bodyBytes, &indexConfig)
	if err != nil {
		return false, err
	}
	if e.GetEsVersion() < models.ESRemoveTypeVersion {
		mapping := indexConfig.Mappings[e.TableID].(map[string]interface{})
		dbPropertiesInterface = mapping["properties"]
	} else {
		dbPropertiesInterface = indexConfig.Mappings["properties"]
	}
	dbProperties := dbPropertiesInterface.(map[string]interface{})
	var dbFieldList []string
	var currentFieldList []string
	for fieldName := range currentProperties {
		currentFieldList = append(currentFieldList, fieldName)
	}
	for fieldName := range dbProperties {
		dbFieldList = append(dbFieldList, fieldName)
		// 数据库中字段多于es中字段，则进行分裂
		if _, exist := currentProperties[fieldName]; !exist {
			return false, nil
		}
	}
	// 遍历判断字段的内容是否完全一致
	for fieldName, dbConfigInterface := range dbProperties {
		currentConfigInterface := currentProperties[fieldName]
		currentConfig := currentConfigInterface.(map[string]interface{})
		dbConfig := dbConfigInterface.(map[string]interface{})

		for _, fieldConfig := range []string{"type", "include_in_all", "doc_values", "format", "analyzer"} {
			dbValue := dbConfig[fieldConfig]
			currentValue := currentConfig[fieldConfig]

			if fieldConfig == "type" && currentValue == nil {
				currentFieldProperties := currentConfig["properties"]
				// object 字段动态写入数据后 不再有type这个字段 只有 properties
				if currentFieldProperties != nil && dbValue != models.ESFieldTypeObject {
					logger.Infof(
						"table_id [%s] index [%s] field [%s] config [%s] database [%s] es field type is object so not same",
						e.TableID, indexName, fieldName, fieldConfig, dbValue,
					)
					return false, nil
				}
				logger.Infof(
					"table_id [%s] index [%s] field [%s] config [%s] database [%s] es config is None, so nothing will do",
					e.TableID, indexName, fieldName, fieldConfig, dbValue,
				)
				continue
			}
			if !reflect.DeepEqual(dbValue, currentValue) {
				logger.Infof(
					"table_id [%s] index [%s] field [%s] config [%s] database [%s] es [%s] is not the same",
					e.TableID, indexName, fieldName, fieldConfig, dbValue, currentValue,
				)
				return false, nil
			}
		}

	}
	logger.Infof("table_id [%s] index->[%s] field config same.", e.TableID, indexName)
	return true, nil

}

// CreateSnapshot 创建snapshot
func (e *ESStorage) CreateSnapshot(ctx context.Context) error {
	client, err := e.GetESClient(ctx)
	if err != nil {
		return err
	}

	can, err := e.CanSnapshot()
	if err != nil {
		return err
	}
	if !can {
		return nil
	}
	currentSnapshotInfo, err := e.CurrentSnapshotInfo(ctx)
	if err != nil {
		return err
	}

	now := e.Now()
	if currentSnapshotInfo.Snapshot != nil {
		// 当天快照已经创建不再创建
		if currentSnapshotInfo.Datetime.Day() == now.Day() {
			return nil
		}
		// 快照未完成 不创建新的快照
		if !currentSnapshotInfo.IsSuccess {
			return nil
		}
	}

	// 如果最新快照不存在，创建
	newSnapshotName := e.MakeSnapshotName(now, e.IndexName())
	expiredIndexList, err := e.ExpiredIndex(ctx)
	if err != nil {
		return err
	}
	if len(expiredIndexList) == 0 {
		logger.Infof("table_id [%s] has no expired index, skip create snapshot", e.TableID)
		return nil
	}

	dbSession := mysql.GetDBSession()
	var indices []string
	var esSnapshotIndiceList []*EsSnapshotIndice
	for _, expiredIndex := range expiredIndexList {
		indices = append(indices, expiredIndex)
		esSnapshotIndice, err := e.CreateSnapshotIndice(ctx, expiredIndex, newSnapshotName)
		if err != nil {
			return err
		}
		esSnapshotIndiceList = append(esSnapshotIndiceList, esSnapshotIndice)
	}
	err = dbSession.DB.Transaction(func(tx *gorm.DB) error {
		snapshot, err := e.SnapshotObj()
		if err != nil {
			return errors.Wrapf(err, "get SnapshotObj with table_id [%s] failed", e.TableID)
		}
		for _, obj := range esSnapshotIndiceList {
			if cfg.BypassSuffixPath != "" && !slicex.IsExistItem(cfg.SkipBypassTasks, "refresh_es_storage") {
				logger.Info(diffutil.BuildLogStr("refresh_es_storage", diffutil.OperatorTypeDBCreate, diffutil.NewSqlBody(obj.TableName(), map[string]interface{}{
					EsSnapshotIndiceDBSchema.TableID.String():        obj.TableID,
					EsSnapshotIndiceDBSchema.SnapshotName.String():   obj.SnapshotName,
					EsSnapshotIndiceDBSchema.ClusterID.String():      obj.ClusterID,
					EsSnapshotIndiceDBSchema.RepositoryName.String(): obj.RepositoryName,
					EsSnapshotIndiceDBSchema.IndexName.String():      obj.IndexName,
					EsSnapshotIndiceDBSchema.DocCount.String():       obj.DocCount,
					EsSnapshotIndiceDBSchema.StoreSize.String():      obj.StoreSize,
					EsSnapshotIndiceDBSchema.StartTime.String():      obj.StartTime,
					EsSnapshotIndiceDBSchema.EndTime.String():        obj.EndTime,
				}), ""))
			} else {
				result := tx.Create(obj)
				if result.Error != nil {
					return result.Error
				}
			}
		}
		payload := fmt.Sprintf(`{"indices": "%s", "include_global_state": false}`, strings.Join(indices, ","))
		metrics.ESChangeCount(e.TableID, "CreateSnapshot")
		if cfg.BypassSuffixPath != "" && !slicex.IsExistItem(cfg.SkipBypassTasks, "refresh_es_storage") {
			logger.Info(diffutil.BuildLogStr("refresh_es_storage", diffutil.OperatorTypeAPIPut, diffutil.NewStringBody(payload), ""))
		} else {
			resp, err := client.CreateSnapshot(ctx, snapshot.TargetSnapshotRepositoryName, newSnapshotName, strings.NewReader(payload))
			if err != nil {
				return err
			}
			defer resp.Close()
		}
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

// SnapshotObj 获取esSnapshot对象
func (e *ESStorage) SnapshotObj() (*EsSnapshot, error) {
	dbSession := mysql.GetDBSession()
	var esSnapshot EsSnapshot
	err := NewEsSnapshotQuerySet(dbSession.DB).TableIDEq(e.TableID).One(&esSnapshot)
	if err != nil {
		return nil, err
	}
	return &esSnapshot, nil

}

// CanSnapshot 判断能否进行快照操作
func (e *ESStorage) CanSnapshot() (bool, error) {
	isEnabled, err := e.IsIndexEnable()
	if err != nil {
		return false, err
	}
	if !isEnabled {
		return false, nil
	}
	has, err := e.HasSnapshotConf()
	if err != nil {
		return false, err
	}
	return has, nil

}

// CanDelete 判断是否可以删除 当存在快照配置当时候需要判断是否可以删除
// - 无快照的结果表 直接删除
// - 当天有索引需要删除的时候 需要判断当天快照是否创建
// - 当天快照完成 删除索引
func (e *ESStorage) CanDelete(ctx context.Context) (bool, error) {
	has, err := e.HasSnapshotConf()
	if err != nil {
		return false, err
	}
	if !has {
		return true, nil
	}

	can, err := e.CanSnapshot()
	if err != nil {
		return false, err
	}
	if !can {
		return true, nil
	}
	snapshotInfo, err := e.CurrentSnapshotInfo(ctx)
	if err != nil {
		return false, err
	}
	expiredIndices, err := e.ExpiredIndex(ctx)
	if err != nil {
		return false, err
	}
	if len(expiredIndices) != 0 {
		if snapshotInfo.Datetime == nil {
			return false, nil
		}
		if snapshotInfo.Datetime.Day() != time.Now().UTC().Day() {
			return false, nil
		}
	}
	if snapshotInfo.Datetime != nil {
		return snapshotInfo.IsSuccess, nil
	}
	return true, nil

}

// CurrentSnapshotInfo 获取当前最新的快照信息
func (e *ESStorage) CurrentSnapshotInfo(ctx context.Context) (*SnapshotInfo, error) {
	client, err := e.GetESClient(ctx)
	if err != nil {
		return nil, err
	}
	snapshot, err := e.SnapshotObj()
	if err != nil {
		return nil, err
	}
	var snapshotResp elasticsearch.SnapshotResp

	resp, err := client.GetSnapshot(ctx, snapshot.TargetSnapshotRepositoryName, []string{e.SearchSnapshot()})
	if err != nil {
		if errors.Is(err, elasticsearch.NotFoundErr) {
			return &SnapshotInfo{
				Snapshot:  nil,
				Datetime:  nil,
				IsSuccess: false,
			}, nil
		}
		return nil, err
	}
	defer resp.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	err = jsonx.Unmarshal(body, &snapshotResp)
	if err != nil {
		return nil, err
	}
	snapshotRe := e.SnapshotRe()
	var maxDatetime *time.Time
	var maxSnapshot *elasticsearch.Snapshot
	e.IndexReV2()
	for _, snap := range snapshotResp.Snapshots {
		snapshotName := snap.Snapshot
		reResult := snapshotRe.FindStringSubmatch(snapshotName)
		if len(reResult) != 0 {
			currentDatetimeStr := reResult[1]
			currentDatetime := timex.TimeStrToTime(timex.ParsePyDateFormat(currentDatetimeStr), e.SnapshotDateFormat(), e.TimeZone)
			if maxDatetime == nil {
				maxDatetime = currentDatetime
				maxSnapshot = &snap
				continue
			}
			if currentDatetime.After(*maxDatetime) {
				maxDatetime = currentDatetime
				maxSnapshot = &snap
			}
		}

	}
	return &SnapshotInfo{
		maxSnapshot,
		maxDatetime,
		maxSnapshot.State == "SUCCESS",
	}, nil
}

// ExpiredIndex 返回过期的index列表
func (e *ESStorage) ExpiredIndex(ctx context.Context) ([]string, error) {
	client, err := e.GetESClient(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := client.GetAlias(ctx, fmt.Sprintf("*%s_*_*", e.IndexName()))
	if err != nil {
		return nil, err
	}
	defer resp.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var alias elasticsearch.AliasResp
	err = jsonx.Unmarshal(body, &alias)
	if err != nil {
		return nil, err
	}
	aliasInfo := e.GroupExpiredAlias(alias, e.Retention)
	var ret []string
	for expiredIndex, expiredInfo := range aliasInfo {
		if len(expiredInfo["not_expired_alias"]) != 0 {
			continue
		}
		err := func() error {
			resp, err := client.CountByIndex(ctx, []string{expiredIndex})
			if err != nil {
				return err
			}
			defer resp.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return err
			}
			var count elasticsearch.CountResp
			err = jsonx.Unmarshal(body, &count)
			if err != nil {
				return err
			}
			if count.Count != 0 {
				ret = append(ret, expiredIndex)
			}
			return nil
		}()
		if err != nil {
			return nil, err
		}

	}
	return ret, nil
}

// GroupExpiredAlias 将每个索引的别名进行分组，分为已过期和未过期
func (e *ESStorage) GroupExpiredAlias(alias elasticsearch.AliasResp, expiredDays int) map[string]map[string][]string {
	logger.Infof("table_id [%s] filtering expired alias before %v days.", e.TableID, expiredDays+models.ESAliasExpiredDelayDays)
	// 按照过期时间进行过期可能导致最早一天的数据查询缺失，让ES别名延迟1天过期，保证数据查询完整
	expiredDatetimePoint := e.Now().Add(-time.Duration(expiredDays+models.ESAliasExpiredDelayDays) * time.Hour * 24)
	var filterResult = make(map[string]map[string][]string)
	for indexName, aliasInfo := range alias {
		if strings.HasPrefix(indexName, e.RestoreIndexPrefix()) {
			continue
		}
		var expiredAlias []string
		var notExpiredAlias []string

		for aliasName := range aliasInfo.Aliases {
			logger.Infof("group expired alias going to process alias name [%s] ", aliasName)
			datetimeStr := e.GetAliasDatetimeStr(aliasName)
			if datetimeStr == "" {
				// 匹配不上时间字符串的情况，一般是因为用户自行创建了别名
				if cfg.StorageEsUpdateTaskRetainInvalidAlias {
					// 保留不合法的别名，将该别名视为未过期
					notExpiredAlias = append(notExpiredAlias, aliasName)
					logger.Infof(
						"table_id [%s] index [%s] got alias_name [%s] not match datetime str, retain it.",
						e.TableID, indexName, aliasName,
					)
				} else {
					expiredAlias = append(expiredAlias, aliasName)
					logger.Infof(
						"table_id [%s] index [%s] got alias_name [%s] not match datetime str, remove it.",
						e.TableID, indexName, aliasName,
					)
				}
				continue
			}
			indexDatetimeObj := timex.TimeStrToTime(datetimeStr, e.GetDateFormat(), e.TimeZone)
			if indexDatetimeObj == nil {
				logger.Errorf("table_id [%s] got index [%s] with datetime_str [%s] which is not match date_format [%s], something go wrong?",
					e.TableID, indexName, datetimeStr, e.GetDateFormat(),
				)
				continue
			}
			// 检查当前别名是否过期
			logger.Debugf("index [%s] alias [%s], datetime [%s], expired datetime [%s]", indexName, aliasName, indexDatetimeObj, expiredDatetimePoint)
			if indexDatetimeObj.After(expiredDatetimePoint) {
				logger.Debugf(
					"table_id [%s] got alias [%s] for index [%s] is not expired.", e.TableID, aliasName, indexName,
				)
				notExpiredAlias = append(notExpiredAlias, aliasName)
			} else {
				logger.Infof("table_id [%s] got alias [%s] for index [%s] is expired.", e.TableID, aliasName, indexName)
				expiredAlias = append(expiredAlias, aliasName)
			}
		}
		filterResult[indexName] = map[string][]string{"expired_alias": expiredAlias, "not_expired_alias": notExpiredAlias}
	}
	return filterResult
}

// GetAliasDatetimeStr 获取别名中的时间字符串
func (e *ESStorage) GetAliasDatetimeStr(name string) string {
	// 判断是否是需要的格式 write_xxx
	aliasWriteRe := e.WriteAliasRe()
	// xxx_read
	aliasReadRe := e.ReadAliasRe()
	// xxx_write
	oldWriteAliasRe := e.OldWriteAliasRe()

	// 匹配并获取时间字符串
	writeResult := aliasWriteRe.FindStringSubmatch(name)
	if len(writeResult) != 0 {
		return writeResult[1]
	}
	readResult := aliasReadRe.FindStringSubmatch(name)
	if len(readResult) != 0 {
		return readResult[1]
	}
	oldWriteResult := oldWriteAliasRe.FindStringSubmatch(name)
	if len(oldWriteResult) != 0 {
		return oldWriteResult[1]
	}
	return ""
}

// CreateSnapshotIndice 构造EsSnapshotIndice对象
func (e *ESStorage) CreateSnapshotIndice(ctx context.Context, indexName string, snapshotName string) (*EsSnapshotIndice, error) {
	client, err := e.GetESClient(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := client.CountByIndex(ctx, []string{indexName})
	if err != nil {
		return nil, err
	}
	defer resp.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var count elasticsearch.CountResp
	err = jsonx.Unmarshal(body, &count)
	if err != nil {
		return nil, err
	}

	respStat, err := client.IndexStat(ctx, indexName, []string{"store"})
	if err != nil {
		return nil, err
	}
	defer respStat.Close()
	all, err := io.ReadAll(respStat.Body)
	if err != nil {
		return nil, err
	}
	var indexStat elasticsearch.IndexStat
	err = jsonx.Unmarshal(all, &indexStat)
	if err != nil {
		return nil, err
	}

	esSnapshot, err := e.SnapshotObj()
	if err != nil {
		return nil, err
	}
	startTime, err := e.GetLastTimeContent(ctx, indexName, true)
	if err != nil {
		return nil, err
	}
	endTime, err := e.GetLastTimeContent(ctx, indexName, false)
	if err != nil {
		return nil, err
	}

	obj := EsSnapshotIndice{
		TableID:        e.TableID,
		SnapshotName:   snapshotName,
		ClusterID:      e.StorageClusterID,
		RepositoryName: esSnapshot.TargetSnapshotRepositoryName,
		IndexName:      indexName,
		DocCount:       count.Count,
		StoreSize:      indexStat.All.Primaries.Store.SizeInBytes,
		StartTime:      startTime,
		EndTime:        endTime,
	}
	return &obj, nil
}

// GetLastTimeContent 获取索引中最新/旧记录的时间
func (e *ESStorage) GetLastTimeContent(ctx context.Context, index string, orderAsc bool) (*time.Time, error) {
	client, err := e.GetESClient(ctx)
	if err != nil {
		return nil, err
	}
	order := "desc"
	if orderAsc {
		order = "asc"
	}
	queryBody := fmt.Sprintf(`{"size": 1, "sort": [{"time": {"order": "%s"}}]}`, order)
	resp, err := client.SearchWithBody(ctx, index, strings.NewReader(queryBody))
	if err != nil {
		return nil, err
	}
	defer resp.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var content LastTimeContentResp
	err = jsonx.Unmarshal(body, &content)
	if err != nil {
		return nil, err
	}

	newTimestamp, err := strconv.ParseInt(content.Hits.Hits[0].Source.Time, 10, 64)
	if err != nil {
		return nil, err
	}
	newTimeObj := time.UnixMilli(newTimestamp)
	return &newTimeObj, nil

}

// CleanIndexV2 清理过期的写入别名及index的操作，如果发现某个index已经没有写入别名，那么将会清理该index
func (e *ESStorage) CleanIndexV2(ctx context.Context) error {
	can, err := e.CanDelete(ctx)
	if err != nil {
		return err
	}
	if !can {
		return nil
	}
	client, err := e.GetESClient(ctx)
	if err != nil {
		return err
	}
	resp, err := client.GetAlias(ctx, fmt.Sprintf("*%s_*_*", e.IndexName()))
	if err != nil {
		return err
	}
	defer resp.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var aliasResp elasticsearch.AliasResp
	err = jsonx.Unmarshal(body, &aliasResp)
	if err != nil {
		return err
	}
	aliasInfo := e.GroupExpiredAlias(aliasResp, e.Retention)
	for indexName, alias := range aliasInfo {
		if strings.HasPrefix(indexName, e.RestoreIndexPrefix()) {
			continue
		}
		notExpiredAlias := alias["not_expired_alias"]
		expiredAlias := alias["expired_alias"]
		// 如果存在未过期的别名，对过期别名进行处理
		if len(notExpiredAlias) != 0 {
			// 如果存在已过期的别名，则将别名删除
			if len(expiredAlias) != 0 {
				logger.Infof(
					"table_id [%s] index [%s] delete_alias_list [%s] is not empty will delete the alias.", e.TableID, indexName, alias["expired_alias"],
				)
				if cfg.BypassSuffixPath != "" && !slicex.IsExistItem(cfg.SkipBypassTasks, "refresh_es_storage") {
					bodyStr, _ := jsonx.MarshalString(expiredAlias)
					logger.Info(diffutil.BuildLogStr("refresh_es_storage", diffutil.OperatorTypeAPIDelete, diffutil.NewStringBody(bodyStr), ""))
				} else {
					resp, err := client.DeleteAlias(ctx, []string{indexName}, expiredAlias)
					if err != nil {
						logger.Errorf("table_id [%s] index [%s] delete_alias_list [%s] error: %s", e.TableID, indexName, alias["expired_alias"], err)
						continue
					}
					resp.Close()
				}
				logger.Warnf("table_id [%s] index [%s] delete_alias_list [%s] is deleted.", e.TableID, indexName, alias["expired_alias"])
			}
			continue
		}
		// 如果已经不存在未过期的别名，则将索引删除
		// 等待所有别名过期删除索引，防止删除别名快照时，丢失数据
		logger.Infof("table_id [%s] has not alias need to keep, will delete the index [%s].", e.TableID, indexName)
		metrics.ESChangeCount(e.TableID, "DeleteIndex")
		if cfg.BypassSuffixPath != "" && !slicex.IsExistItem(cfg.SkipBypassTasks, "refresh_es_storage") {
			logger.Info(diffutil.BuildLogStr("refresh_es_storage", diffutil.OperatorTypeAPIDelete, diffutil.NewStringBody(indexName), ""))
		} else {
			resp, err := client.DeleteIndex(ctx, []string{indexName})
			if err != nil {
				logger.Warnf("table_id [%s] index [%s] delete failed, index maybe doing snapshot，%s", e.TableID, indexName, err)
				continue
			}
			resp.Close()
		}
		logger.Warnf("table_id [%s] index [%s] is deleted now.", e.TableID, indexName)
	}
	logger.Infof("table_id [%s] clean index is process done.", e.TableID)
	return nil
}

// HasSnapshotConf 判断是否存在快照配置
func (e *ESStorage) HasSnapshotConf() (bool, error) {
	dbSession := mysql.GetDBSession()
	count, err := NewEsSnapshotQuerySet(dbSession.DB).TableIDEq(e.TableID).Count()
	if err != nil {
		return false, err
	}
	return count != 0, nil
}

// CleanSnapshot 清理快照
func (e *ESStorage) CleanSnapshot(ctx context.Context) error {
	can, err := e.CanDeleteSnapshot()
	if err != nil {
		return err
	}
	if !can {
		return nil
	}
	snapshotObj, err := e.SnapshotObj()
	if err != nil {
		return err
	}
	expiredSnapshots, err := e.GetExpiredSnapshot(ctx, snapshotObj.SnapshotDays, snapshotObj.TargetSnapshotRepositoryName)
	if err != nil {
		return err
	}
	if len(expiredSnapshots) == 0 {
		return nil
	}
	logger.Infof("table_id [%s] need delete snapshot [%v]", e.TableID, expiredSnapshots)
	client, err := e.GetESClient(ctx)
	if err != nil {
		return err
	}
	dbSession := mysql.GetDBSession()
	for _, snapshot := range expiredSnapshots {
		err := dbSession.DB.Transaction(func(tx *gorm.DB) error {
			if cfg.BypassSuffixPath != "" && !slicex.IsExistItem(cfg.SkipBypassTasks, "refresh_es_storage") {
				logger.Info(diffutil.BuildLogStr("refresh_es_storage", diffutil.OperatorTypeDBCreate, diffutil.NewSqlBody(EsSnapshotIndice{}.TableName(), map[string]interface{}{
					EsSnapshotIndiceDBSchema.TableID.String():      e.TableID,
					EsSnapshotIndiceDBSchema.SnapshotName.String(): snapshot.Snapshot,
				}), ""))

				payloadStr, _ := jsonx.MarshalString(map[string]string{"repository": snapshot.Repository, "snapshot": snapshot.Snapshot})
				logger.Info(diffutil.BuildLogStr("refresh_es_storage", diffutil.OperatorTypeAPIDelete, diffutil.NewStringBody(payloadStr), ""))
			} else {
				err := tx.Where("table_id = ? and snapshot_name = ?", e.TableID, snapshot.Snapshot).Delete(&EsSnapshotIndice{}).Error
				if err != nil {
					return err
				}
				metrics.ESChangeCount(e.TableID, "DeleteSnapshot")
				resp, err := client.DeleteSnapshot(ctx, snapshot.Repository, snapshot.Snapshot)
				if err != nil {
					return err
				}
				defer resp.Close()
			}
			return nil
		})
		if err != nil {
			logger.Errorf("clean snapshot [%s] failed, %s", snapshot.Snapshot, err)
		}
	}
	logger.Infof("table_id [%s] has clean snapshot", e.TableID)
	return nil
}

// CanDeleteSnapshot 判断是否可以删除快照
func (e *ESStorage) CanDeleteSnapshot() (bool, error) {
	dbSession := mysql.GetDBSession()
	var esSnapshot EsSnapshot
	err := NewEsSnapshotQuerySet(dbSession.DB).TableIDEq(e.TableID).One(&esSnapshot)
	if err != nil {
		if gorm.IsRecordNotFoundError(err) {
			return false, nil
		}
		return false, err
	}
	if esSnapshot.SnapshotDays != 0 {
		return true, nil
	}
	return false, nil

}

// GetExpiredSnapshot 获取过期的快照列表
func (e *ESStorage) GetExpiredSnapshot(ctx context.Context, expiredDays int, snapshotRepositoryName string) ([]elasticsearch.Snapshot, error) {
	logger.Infof("table_id [%s] filter expired snapshot before %v days", e.TableID, expiredDays)
	expiredDatetimePoint := e.Now().Add(-time.Duration(expiredDays) * time.Hour * 24)
	client, err := e.GetESClient(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := client.GetSnapshot(ctx, snapshotRepositoryName, []string{e.SearchSnapshot()})
	if err != nil {
		if errors.Is(err, elasticsearch.NotFoundErr) {
			return nil, nil
		}
		return nil, err
	}
	defer resp.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var snapshotResp elasticsearch.SnapshotResp
	err = jsonx.Unmarshal(body, &snapshotResp)
	if err != nil {
		return nil, err
	}
	var expiredSnapshots []elasticsearch.Snapshot
	snapshotRe := e.SnapshotRe()
	for _, snapshot := range snapshotResp.Snapshots {
		reResult := snapshotRe.FindStringSubmatch(snapshot.Snapshot)
		if len(reResult) != 0 {
			snapshotDatetimeStr := reResult[1]
			snapshotDatetime := timex.TimeStrToTime(timex.ParsePyDateFormat(snapshotDatetimeStr), e.SnapshotDateFormat(), e.TimeZone)
			if expiredDatetimePoint.After(*snapshotDatetime) {
				expiredSnapshots = append(expiredSnapshots, snapshot)
			}
		}
	}
	return expiredSnapshots, nil
}

// ReallocateIndex 重新分配索引所在的节点
func (e *ESStorage) ReallocateIndex(ctx context.Context) error {
	if e.WarmPhaseDays <= 0 {
		logger.Infof("table_id [%s] warm_phase_days is not set, skip.", e.TableID)
		return nil
	}
	var warmPhaseSetting WarmPhaseSetting
	err := jsonx.UnmarshalString(e.WarmPhaseSettings, &warmPhaseSetting)
	if err != nil {
		return err
	}
	client, err := e.GetESClient(ctx)
	if err != nil {
		return err
	}

	resp, err := client.GetAlias(ctx, fmt.Sprintf("*%s_*_*", e.IndexName()))
	if err != nil {
		return err
	}
	defer resp.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var aliasResp elasticsearch.AliasResp
	err = jsonx.Unmarshal(body, &aliasResp)
	if err != nil {
		return err
	}
	aliasInfo := e.GroupExpiredAlias(aliasResp, e.WarmPhaseDays)
	// 如果存在未过期的别名，那说明这个索引仍在被写入，不能把它切换到冷节点
	var reallocateIndexList []string
	for indexName, alias := range aliasInfo {
		notExpiredAlias := alias["not_expired_alias"]
		if len(notExpiredAlias) == 0 {
			reallocateIndexList = append(reallocateIndexList, indexName)
		}
	}
	if len(reallocateIndexList) == 0 {
		logger.Infof("table_id [%s] no index should be allocated, skip.", e.TableID)
		return nil
	}
	indicesResp, err := client.GetIndices(reallocateIndexList)
	if err != nil {
		return err
	}
	defer indicesResp.Close()
	bodyResp, err := io.ReadAll(indicesResp.Body)
	var indicesConfig WarmPhaseConfigResp
	err = jsonx.Unmarshal(bodyResp, &indicesConfig)
	if err != nil {
		return err
	}
	var filterIndices []string
	for indexName, config := range indicesConfig {
		allocation := config.Settings.Index.Routing.Allocation
		allocationSetting, ok := allocation[warmPhaseSetting.AllocationType]
		if !ok {
			filterIndices = append(filterIndices, indexName)
			continue
		}
		value, ok := allocationSetting[warmPhaseSetting.AllocationAttrName]
		if !ok {
			filterIndices = append(filterIndices, indexName)
			continue
		}
		if value != warmPhaseSetting.AllocationAttrValue {
			filterIndices = append(filterIndices, indexName)
			continue
		}
	}
	if len(filterIndices) == 0 {
		logger.Infof("table_id [%s] no index should be allocated, skip.", e.TableID)
		return nil
	}

	logger.Infof(
		"table_id [%s] ready to reallocate with settings: days(%v), name(%s), value(%s), type(%s), for index_list: %s",
		e.TableID,
		e.WarmPhaseDays,
		warmPhaseSetting.AllocationAttrName,
		warmPhaseSetting.AllocationAttrValue,
		warmPhaseSetting.AllocationType,
		filterIndices,
	)

	setting := fmt.Sprintf(`{"index.routing.allocation.%s.%s": "%s"}`, warmPhaseSetting.AllocationType, warmPhaseSetting.AllocationAttrName, warmPhaseSetting.AllocationAttrValue)
	payloadStr, _ := jsonx.MarshalString(map[string]interface{}{"index": filterIndices, "body": setting})
	if cfg.BypassSuffixPath != "" && !slicex.IsExistItem(cfg.SkipBypassTasks, "refresh_es_storage") {
		logger.Info(diffutil.BuildLogStr("refresh_es_storage", diffutil.OperatorTypeAPIPut, diffutil.NewStringBody(payloadStr), ""))
	} else {
		putResp, err := client.PutSettings(ctx, strings.NewReader(setting), filterIndices)
		if err != nil {
			return err
		}
		defer putResp.Close()
	}
	return nil
}

func (e ESStorage) CreateEsIndex(ctx context.Context, isSyncDb bool) error {
	if isSyncDb {
		if err := e.CreateIndexAndAliases(ctx, e.SliceGap); err != nil {
			return err
		}
		logger.Infof("result_table [%s] has create es storage index", e.TableID)
	} else {
		// TODO create_es_storage_index 异步创建es索引
	}
	return nil
}

type IndexConfig struct {
	Settings map[string]interface{} `json:"settings"`
	Mappings map[string]interface{} `json:"mappings"`
}

type CurrentIndexInfo struct {
	IndexVersion string
	TimeObject   *time.Time
	Index        uint
	size         int64
}

type SnapshotInfo struct {
	Snapshot  *elasticsearch.Snapshot
	Datetime  *time.Time
	IsSuccess bool
}

// LastTimeContentResp 用于解析查询最早/晚一条es数据的时间
type LastTimeContentResp struct {
	Hits struct {
		Hits []struct {
			Source struct {
				Time string `json:"time"`
			} `json:"_source"`
			Sort []int64 `json:"sort"`
		} `json:"hits"`
	} `json:"hits"`
}

// WarmPhaseConfigResp 解析index的warm_phase_config配置
type WarmPhaseConfigResp map[string]struct {
	Settings struct {
		Index struct {
			Routing struct {
				Allocation map[string]map[string]string `json:"allocation"`
			} `json:"routing"`
		} `json:"index"`
	} `json:"settings"`
	Mappings map[string]interface{} `json:"mappings"`
}

// WarmPhaseSetting 索引数据分配配置
type WarmPhaseSetting struct {
	AllocationAttrName  string `json:"allocation_attr_name"`  // 切换路由的节点属性名称
	AllocationAttrValue string `json:"allocation_attr_value"` // 切换路由的节点属性值
	AllocationType      string `json:"allocation_type"`       // 属性匹配类型，可选 require, include, exclude 等
}
