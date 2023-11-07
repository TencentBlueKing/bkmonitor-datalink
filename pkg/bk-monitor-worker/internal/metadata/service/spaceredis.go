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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jinzhu/gorm"
	"github.com/pkg/errors"
	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/globalconfig"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/bcs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/customreport"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/dependentredis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/optionx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/slicex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	SpaceRedisKeyPath = "space.redis_key"
)

func init() {
	viper.SetDefault(SpaceRedisKeyPath, "bkmonitorv3:spaces")
}

var SkipDataIdListForBkcc = []uint{1110000}

// SpaceRedisSvc 空间Redis service
type SpaceRedisSvc struct {
	goroutineLimit int
}

func NewSpaceRedisSvc(goroutineLimit int) SpaceRedisSvc {
	return SpaceRedisSvc{goroutineLimit: goroutineLimit}
}

// PushRedisData 推送 redis 数据
func (SpaceRedisSvc) PushRedisData(spaceTypeId, spaceId, spaceCode, tableId string) error {
	if spaceTypeId == "" || spaceId == "" {
		return errors.New("params spaceTypeId or spaceId can not be empty")
	}

	switch spaceTypeId {
	case models.SpaceTypeBKCC:
		if err := NewSpacePusher().PushBkccTypeSpace(spaceId, tableId); err != nil {
			return err
		}
	case models.SpaceTypeBKCI:
		// 针对 bkci 分为蓝盾和容器服务，分别处理
		// 如果 spaceCode 存在，则添加 bcs 的处理
		if spaceCode != "" {
			if err := NewSpacePusher().PushBcsTypeSpace(spaceId, tableId, true, true); err != nil {
				return err
			}
		}
		if err := NewSpacePusher().PushBkciTypeSpace(spaceId, tableId); err != nil {
			return err
		}
	case models.SpaceTypeBKSAAS:
		if err := NewSpacePusher().PushBksaasTypeSpace(spaceId, tableId); err != nil {
			return err
		}
	default:
		return fmt.Errorf("space type [%s] not found", spaceTypeId)
	}
	logger.Infof("push [%s] type space [%s] space code [%s] table id [%s] finished", spaceTypeId, spaceId, spaceCode, tableId)
	return nil
}

// PushAndPublishAllSpace 推送redis数据并发布通知
func (s SpaceRedisSvc) PushAndPublishAllSpace(spaceTypeId, spaceId string, isPublish bool) error {
	// 根据参数过滤出要更新的空间信息
	var spaceList []space.Space
	qs := space.NewSpaceQuerySet(mysql.GetDBSession().DB)
	if spaceTypeId != "" {
		qs = qs.SpaceTypeIdEq(spaceTypeId)
	}
	if spaceId != "" {
		qs = qs.SpaceIdEq(spaceId)
	}
	if err := qs.All(&spaceList); err != nil {
		return err
	}
	wg := &sync.WaitGroup{}
	ch := make(chan bool, s.goroutineLimit)
	dataCh := make(chan string, len(spaceList))
	wg.Add(len(spaceList))
	// 推送相关空间
	for _, sp := range spaceList {
		ch <- true
		go func(sp space.Space, dataCh chan string, wg *sync.WaitGroup, ch chan bool) {
			defer func() {
				<-ch
				wg.Done()
			}()
			err := s.PushRedisData(sp.SpaceTypeId, sp.SpaceId, sp.SpaceCode, "")
			if err != nil {
				logger.Errorf("push redis data, space_type_id: %s, space_id: %s, space_code: %s failed, %s", sp.SpaceTypeId, sp.SpaceId, sp.SpaceCode, err)
				return
			}
			dataCh <- fmt.Sprintf("%s__%s", sp.SpaceTypeId, sp.SpaceId)
		}(sp, dataCh, wg, ch)
	}
	wg.Wait()
	close(dataCh)
	spaceUidList := make([]interface{}, 0)
	for spaceUid := range dataCh {
		spaceUidList = append(spaceUidList, spaceUid)
	}
	if len(spaceUidList) == 0 {
		logger.Warnf("no space need to publish")
		return nil
	}
	client, err := dependentredis.GetInstance(context.Background())
	if err != nil {
		return errors.Wrapf(err, "get redis client error, %v", err)
	}
	// 推送空间数据到 redis，用于创建时，推送失败或者没有推送的场景
	if err := client.SAdd(viper.GetString(SpaceRedisKeyPath), spaceUidList...); err != nil {
		return err
	}

	// 参数指定是否需要发布通知
	if isPublish {
		redisKey := viper.GetString(SpaceRedisKeyPath)
		for _, spaceUid := range spaceUidList {
			if err := client.Publish(redisKey, spaceUid); err != nil {
				return err
			}
		}
	}
	return nil
}

type SpacePusher struct {
	tableDataIdMap     map[string]uint
	tableIdTableMap    map[string]resulttable.ResultTable
	measurementTypeMap map[string]string
	tableIdList        []string
	resultTableList    []resulttable.ResultTable
	tableFieldMap      map[string][]string
	segmentOptionMap   map[string]bool
}

func NewSpacePusher() *SpacePusher {
	return &SpacePusher{}
}

func (s *SpacePusher) getSpaceDetailKey(spaceTypeId, spaceId string) string {
	return fmt.Sprintf("%s:%s__%s", viper.GetString(SpaceRedisKeyPath), spaceTypeId, spaceId)
}

// PushBkccTypeSpace 推送业务类型的空间
func (s *SpacePusher) PushBkccTypeSpace(spaceId, tableId string) error {
	spaceTypeId := models.SpaceTypeBKCC
	if err := s.getData(spaceTypeId, spaceId, tableId, nil); err != nil {
		return err
	}
	if err := s.composeAndPushBizData(spaceTypeId, spaceId, spaceTypeId, spaceId, nil, false); err != nil {
		return err
	}
	return nil
}

// PushBcsTypeSpace 推送容器类型空间
func (s *SpacePusher) PushBcsTypeSpace(spaceId, tableId string, pushBkccType, pushBcsType bool) error {
	spaceTypeId := models.SpaceTypeBKCI
	// 关联的业务，BCS 项目只关联一个业务
	if pushBkccType {
		if err := s.pushBizResourceForBcsType(spaceTypeId, spaceId, tableId); err != nil {
			return err
		}
	}
	if pushBcsType {
		if err := s.pushBcsResourceForBcsType(spaceTypeId, spaceId, tableId); err != nil {
			return err
		}
	}
	return nil
}

// PushBkciTypeSpace 推送蓝盾流水线资源的
func (s *SpacePusher) PushBkciTypeSpace(spaceId, tableId string) error {
	spaceTypeId := models.SpaceTypeBKCI
	// 排除集群的数据源，因为需要单独处理
	var excludeDataIdList []uint
	var clusters []bcs.BCSClusterInfo
	if err := bcs.NewBCSClusterInfoQuerySet(mysql.GetDBSession().DB).All(&clusters); err != nil {
		return err
	}
	for _, c := range clusters {
		excludeDataIdList = append(excludeDataIdList, c.K8sMetricDataID)
		excludeDataIdList = append(excludeDataIdList, c.CustomMetricDataID)
	}
	// 排除特定的数据源
	excludeDataIdList = append(excludeDataIdList, SkipDataIdListForBkcc...)

	options := optionx.NewOptions(map[string]interface{}{"excludeDataIdList": excludeDataIdList})
	if err := s.getData(spaceTypeId, spaceId, tableId, options); err != nil {
		return err
	}
	if err := s.composeAndPushBizData(spaceTypeId, spaceId, spaceTypeId, spaceId, []map[string]interface{}{{"projectId": spaceId}}, true); err != nil {
		return err
	}
	return nil
}

// PushBksaasTypeSpace 推送 bksaas 类型的空间信息
func (s *SpacePusher) PushBksaasTypeSpace(spaceId, tableId string) error {
	spaceTypeId := models.SpaceTypeBKSAAS
	if err := s.getData(spaceTypeId, spaceId, tableId, optionx.NewOptions(map[string]interface{}{"fromAuthorization": false})); err != nil {
		return err
	}
	if err := s.pushSpaceWithType(spaceTypeId, spaceId); err != nil {
		return err
	}
	return nil
}

func (s *SpacePusher) getData(spaceTypeId, spaceId, tableId string, options *optionx.Options) error {
	// 可选参数处理
	if options == nil {
		options = optionx.NewOptions(nil)
	}
	options.SetDefault("includePlatFormDataId", true)
	options.SetDefault("excludeDataIdList", make([]uint, 0))

	dataIdList := make([]uint, 0)
	if tableId == "" {
		var sdObjects []space.SpaceDataSource
		qs := space.NewSpaceDataSourceQuerySet(mysql.GetDBSession().DB).SpaceTypeIdEq(spaceTypeId).SpaceIdEq(spaceId)
		if fromAuthorization, ok := options.GetBool("fromAuthorization"); ok {
			qs = qs.FromAuthorizationEq(fromAuthorization)
		}
		if err := qs.All(&sdObjects); err != nil {
			return err
		}
		for _, sd := range sdObjects {
			dataIdList = append(dataIdList, sd.BkDataId)
		}

		// 获取空间级的 data id
		idList, err := s.getSpaceDataIdList(spaceTypeId)
		if err != nil {
			return err
		}
		dataIdList = append(dataIdList, idList...)

		// 获取全空间的 data id
		if includePlatFormDataId, _ := options.GetBool("includePlatFormDataId"); includePlatFormDataId {
			idList, err := s.getPlatformDataIdList()
			if err != nil {
				return err
			}
			dataIdList = append(dataIdList, idList...)
		}

		// 过滤掉部分data id，避免重复处理
		excludeDataIdList, _ := options.GetUintsSlice("excludeDataIdList")
		dataIdSet := slicex.UintList2Set(dataIdList)
		excludeDataIdSet := slicex.UintList2Set(excludeDataIdList)
		dataIdList = slicex.UintSet2List(dataIdSet.Difference(excludeDataIdSet))
	}
	if len(dataIdList) == 0 && tableId == "" {
		logger.Warnf("space_type_id [%s] space_id [%s] table_id [%s] not found dataid, skip", spaceTypeId, spaceId, tableId)
		return nil
	}
	var err error
	// 通过 data id 获取到结果表, tableId - bkDataId map
	s.tableDataIdMap, err = s.getResultTableDataIdMap(dataIdList, tableId)
	if err != nil {
		return err
	}

	for tb := range s.tableDataIdMap {
		// tableId list
		s.tableIdList = append(s.tableIdList, tb)
	}

	// resulttable list
	s.resultTableList, err = s.getResultTableList()
	if err != nil {
		return err
	}

	// tableId - resulttable map
	s.tableIdTableMap = make(map[string]resulttable.ResultTable)
	for _, t := range s.resultTableList {
		s.tableIdTableMap[t.TableId] = t
	}

	// 获取table的measurement type
	s.measurementTypeMap, err = s.getMeasurementTypeByTables()
	if err != nil {
		return err
	}

	// 获取结果表对应的属性
	s.tableFieldMap, err = s.getTableFieldByTableId()
	if err != nil {
		return err
	}
	s.segmentOptionMap, err = s.getSegmentedOptionByTableId()
	if err != nil {
		return err
	}
	return nil
}

func (s *SpacePusher) composeAndPushBizData(spaceTypeId, spaceId, ResourceType, ResourceId string, dimensionValues []map[string]interface{}, skipSystem bool) error {
	fieldValue, err := s.composeBizId(spaceTypeId, spaceId, ResourceType, ResourceId, dimensionValues, skipSystem)
	if err != nil {
		return err
	}
	if len(fieldValue) != 0 {
		client, err := dependentredis.GetInstance(context.Background())
		if err != nil {
			return errors.Wrapf(err, "get redis client error, %v", err)
		}
		redisKey := s.getSpaceDetailKey(spaceTypeId, spaceId)
		for field, value := range fieldValue {
			if err := client.HSet(redisKey, field, value); err != nil {
				return err
			}
			logger.Infof("push redis data, space: %s, field: %s, value: %s", redisKey, field, value)
		}
	}
	return nil
}

func (s *SpacePusher) composeBizId(spaceTypeId, spaceId, ResourceType, ResourceId string, dimensionValues []map[string]interface{}, skipSystem bool) (map[string]string, error) {
	fieldValue := make(map[string]string)
	for tableId, dataId := range s.tableDataIdMap {
		// 现阶段针对1001下 `system.` 或者 `dbm_system.` 开头的结果表不允许被覆盖
		if skipSystem && strings.HasPrefix(tableId, "system.") || strings.HasPrefix(tableId, "dbm_system") {
			continue
		}
		fields, ok := s.tableFieldMap[tableId]
		if !ok {
			fields = make([]string, 0)
		}
		if len(fields) == 0 {
			logger.Warnf("space_type [%s], space [%s], data_id [%v], table_id [%s] not found fields", spaceTypeId, spaceId, dataId, tableId)
		}
		if !strings.Contains(tableId, ".") {
			continue
		}
		measurementType := s.measurementTypeMap[tableId]
		// 兼容脏数据导致获取不到，如果不存在，则忽略
		if measurementType == "" {
			logger.Errorf("table_id [%s] not find measurement type", tableId)
			continue
		}
		filters := make([]map[string]interface{}, 0)
		isNeedAddFilter, err := s.isNeedAddFilter(measurementType, spaceTypeId, spaceId, dataId)
		if err != nil {
			return nil, err
		}
		if isNeedAddFilter {
			if len(dimensionValues) == 0 {
				dimensionValues = []map[string]interface{}{{"bk_biz_id": ResourceId}}
			}
			filters = dimensionValues
		}
		value, err := jsonx.MarshalString(map[string]interface{}{
			"type":             ResourceType,
			"field":            fields,
			"measurement_type": measurementType,
			"bk_data_id":       strconv.FormatUint(uint64(dataId), 10),
			"filters":          filters,
			"segmented_enable": s.segmentOptionMap[tableId],
			"data_label":       s.tableIdTableMap[tableId].DataLabel,
		})
		if err != nil {
			return nil, err
		}
		fieldValue[tableId] = value
	}
	return fieldValue, nil
}

// 推送容器关联的业务
func (s *SpacePusher) pushBizResourceForBcsType(spaceTypeId, spaceId, tableId string) error {
	resourceTypeId := models.SpaceTypeBKCC
	var sr space.SpaceResource
	if err := space.NewSpaceResourceQuerySet(mysql.GetDBSession().DB).SpaceTypeIdEq(spaceTypeId).SpaceIdEq(spaceId).ResourceIdEq(resourceTypeId).One(&sr); err != nil {
		if gorm.IsRecordNotFoundError(err) {
			logger.Errorf("space [%s__%s], resource [%s] not found", spaceTypeId, spaceId, resourceTypeId)
			return nil
		} else {
			return err
		}
	}
	var bizIdStr string
	if sr.ResourceId != nil {
		bizIdStr = *sr.ResourceId
	}
	// 过滤业务下的资源信息(仅含有归属于当前业务的数据源)
	options := optionx.NewOptions(map[string]interface{}{"fromAuthorization": false})
	if err := s.getData(resourceTypeId, bizIdStr, tableId, options); err != nil {
		return err
	}
	if err := s.composeAndPushBizData(spaceTypeId, spaceId, resourceTypeId, bizIdStr, nil, false); err != nil {
		return err
	}
	return nil
}

// 推送容器关联的容器资源
func (s *SpacePusher) pushBcsResourceForBcsType(spaceTypeId, spaceId, tableId string) error {
	resourceType := models.SpaceTypeBCS
	// 获取项目相关联的容器资源
	var sr space.SpaceResource
	if err := space.NewSpaceResourceQuerySet(mysql.GetDBSession().DB).SpaceTypeIdEq(spaceTypeId).SpaceIdEq(spaceId).ResourceTypeEq(resourceType).ResourceIdEq(spaceId).One(&sr); err != nil {
		if gorm.IsRecordNotFoundError(err) {
			return nil
		} else {
			return err
		}
	}
	// 获取项目下归属的 data id
	options := optionx.NewOptions(map[string]interface{}{"includePlatformDataId": false})
	if err := s.getData(spaceTypeId, spaceId, tableId, options); err != nil {
		return err
	}
	var resList []map[string]interface{}
	if err := jsonx.UnmarshalString(sr.DimensionValues, &resList); err != nil {
		return err
	}
	// 获取集群对应的data id
	// 共享集群的直接通过关联资源获取，减少对接口的依赖
	clusterIdTypeMap := make(map[string]interface{})
	dataIdClusterMap := make(map[uint]string)
	sharedClusterNsMap := make(map[string][]string)
	for _, res := range resList {
		resOptions := optionx.NewOptions(res)
		clusterId, ok := resOptions.GetString("cluster_id")
		if !ok {
			return fmt.Errorf("parse space resource dimension values failed, %v", res)
		}
		clusterType, ok := resOptions.GetString("cluster_type")
		if !ok {
			clusterType = models.BcsClusterTypeSingle
		}
		clusterIdTypeMap[clusterId] = clusterType
		clusterDataIdMap, err := s.getDataIdByCluster(clusterId)
		if err != nil {
			return err
		}
		for _, dataId := range clusterDataIdMap[clusterId] {
			dataIdClusterMap[dataId] = clusterId
		}
		namespace, _ := resOptions.GetStringSlice("namespace")
		if clusterType == models.BcsClusterTypeShared && len(namespace) != 0 {
			sharedClusterNsMap[clusterId] = namespace
		}
	}
	// 组装数据
	fieldValue := make(map[string]string)
	for tableId, dataId := range s.tableDataIdMap {
		// 过滤掉格式不符合预期的结果表
		if !strings.Contains(tableId, ".") {
			continue
		}
		fields, ok := s.tableFieldMap[tableId]
		if !ok {
			fields = make([]string, 0)
		}
		clusterId := dataIdClusterMap[dataId]
		if clusterId == "" {
			continue
		}
		clusterType, ok := clusterIdTypeMap[clusterId]
		if !ok {
			clusterType = models.BcsClusterTypeSingle
		}
		filterData := make([]map[string]interface{}, 0)
		// 如果为独立集群，则仅需要集群 ID
		// 如果为共享集群
		// - 命名空间为空，则忽略
		// - 命名空间不为空，则添加集群和命名空间
		if clusterType == models.BcsClusterTypeSingle {
			filterData = append(filterData, map[string]interface{}{"bcs_cluster_id": clusterId, "namespace": nil})
		} else if clusterType == models.BcsClusterTypeShared {
			nsList, ok := sharedClusterNsMap[clusterId]
			if ok {
				for _, ns := range nsList {
					filterData = append(filterData, map[string]interface{}{"bcs_cluster_id": clusterId, "namespace": ns})
				}
			}
		}

		measurementType := s.measurementTypeMap[tableId]
		// 兼容脏数据导致获取不到，如果不存在，则忽略
		if measurementType == "" {
			logger.Errorf("table_id [%s] not find measurement type", tableId)
			continue
		}
		value, err := jsonx.MarshalString(map[string]interface{}{
			"type":             resourceType,
			"field":            fields,
			"measurement_type": measurementType,
			"bk_data_id":       strconv.FormatUint(uint64(dataId), 10),
			"filters":          filterData,
			"segmented_enable": s.segmentOptionMap[tableId],
			"data_label":       s.tableIdTableMap[tableId].DataLabel,
		})
		if err != nil {
			return err
		}
		fieldValue[tableId] = value
	}
	// 推送数据
	if len(fieldValue) != 0 {
		client, err := dependentredis.GetInstance(context.Background())
		if err != nil {
			return errors.Wrapf(err, "get redis client error, %v", err)
		}
		redisKey := s.getSpaceDetailKey(spaceTypeId, spaceId)
		for field, value := range fieldValue {
			if err := client.HSet(redisKey, field, value); err != nil {
				return err
			}
			logger.Infof("push redis data, space: %s, field: %s, value: %s", redisKey, field, value)
		}
	}
	return nil
}

func (s *SpacePusher) pushSpaceWithType(spaceTypeId, spaceId string) error {
	fieldValue := make(map[string]string)
	// 组装需要的数据
	resourceType := spaceTypeId
	for tableId, dataId := range s.tableDataIdMap {
		// 过滤掉格式不符合预期的结果表
		if !strings.Contains(tableId, ".") {
			continue
		}
		fields, ok := s.tableFieldMap[tableId]
		if !ok {
			fields = make([]string, 0)
		}
		measurementType := s.measurementTypeMap[tableId]
		// 兼容脏数据导致获取不到，如果不存在，则忽略
		if measurementType == "" {
			logger.Errorf("table_id [%s] not find measurement type", tableId)
			continue
		}
		value, err := jsonx.MarshalString(map[string]interface{}{
			"type":             resourceType,
			"field":            fields,
			"measurement_type": measurementType,
			"bk_data_id":       strconv.FormatUint(uint64(dataId), 10),
			"filters":          make([]map[string]string, 0),
			"segmented_enable": s.segmentOptionMap[tableId],
			"data_label":       s.tableIdTableMap[tableId].DataLabel,
		})
		if err != nil {
			return err
		}
		fieldValue[tableId] = value
	}
	// 推送数据
	if len(fieldValue) != 0 {
		client, err := dependentredis.GetInstance(context.Background())
		if err != nil {
			return errors.Wrapf(err, "get redis client error, %v", err)
		}
		redisKey := s.getSpaceDetailKey(spaceTypeId, spaceId)
		for field, value := range fieldValue {
			if err := client.HSet(redisKey, field, value); err != nil {
				return err
			}
			logger.Infof("push redis data, space: %s, field: %s, value: %s", redisKey, field, value)
		}
	}
	return nil
}

// 获取空间级 data id, 允许相同空间类型下的空间访问
func (s *SpacePusher) getSpaceDataIdList(spaceTypeId string) ([]uint, error) {
	var bkDataIdList []uint
	var dsList []resulttable.DataSource
	if err := resulttable.NewDataSourceQuerySet(mysql.GetDBSession().DB).IsPlatformDataIdEq(true).SpaceTypeIdEq(spaceTypeId).All(&dsList); err != nil {
		return nil, err
	}
	for _, ds := range dsList {
		bkDataIdList = append(bkDataIdList, ds.BkDataId)
	}
	return bkDataIdList, nil
}

// 获取平台级 data id
func (s *SpacePusher) getPlatformDataIdList() ([]uint, error) {
	var bkDataIdList []uint
	var dsList []resulttable.DataSource
	if err := resulttable.NewDataSourceQuerySet(mysql.GetDBSession().DB).IsPlatformDataIdEq(true).SpaceTypeIdEq(models.SpaceTypeAll).All(&dsList); err != nil {
		return nil, err
	}
	for _, ds := range dsList {
		bkDataIdList = append(bkDataIdList, ds.BkDataId)
	}
	return bkDataIdList, nil
}

// 通过 data id 获取结果表映射
func (s *SpacePusher) getResultTableDataIdMap(dataIdList []uint, tableId string) (map[string]uint, error) {
	tableDataIdMap := make(map[string]uint)
	var dsrtList []resulttable.DataSourceResultTable
	qs := resulttable.NewDataSourceResultTableQuerySet(mysql.GetDBSession().DB)
	if len(dataIdList) != 0 {
		qs = qs.BkDataIdIn(dataIdList...)
	}
	if tableId != "" {
		qs = qs.TableIdEq(tableId)
	}
	if err := qs.All(&dsrtList); err != nil {
		return nil, err
	}
	for _, ds := range dsrtList {
		tableDataIdMap[ds.TableId] = ds.BkDataId
	}
	return tableDataIdMap, nil
}

func (s *SpacePusher) getResultTableList() ([]resulttable.ResultTable, error) {
	var rtList []resulttable.ResultTable
	if len(s.tableIdList) == 0 {
		return rtList, nil
	}
	if err := resulttable.NewResultTableQuerySet(mysql.GetDBSession().DB).TableIdIn(s.tableIdList...).All(&rtList); err != nil {
		return nil, err
	}
	return rtList, nil
}

// 通过结果表, 获取对应的 option 配置, 通过 option 转到到 measurement 类型
func (s *SpacePusher) getMeasurementTypeByTables() (map[string]string, error) {
	if len(s.tableIdList) == 0 {
		return make(map[string]string), nil
	}
	// 过滤对应关系，用以进行判断单指标单表、多指标单表
	var rtoList []resulttable.ResultTableOption
	if err := resulttable.NewResultTableOptionQuerySet(mysql.GetDBSession().DB).TableIDIn(s.tableIdList...).NameEq(models.OptionIsSplitMeasurement).All(&rtoList); err != nil {
		return nil, err
	}
	rtoMap := make(map[string]bool)
	for _, rto := range rtoList {
		var value bool
		if err := jsonx.UnmarshalString(rto.Value, &value); err != nil {
			return nil, err
		}
		rtoMap[rto.TableID] = value
	}

	var bkDataIdList []uint
	for _, bkDataId := range s.tableDataIdMap {
		bkDataIdList = append(bkDataIdList, bkDataId)
	}
	dataIdEtlMap := make(map[uint]string)
	var dsList []resulttable.DataSource
	if len(bkDataIdList) != 0 {
		if err := resulttable.NewDataSourceQuerySet(mysql.GetDBSession().DB).BkDataIdIn(bkDataIdList...).All(&dsList); err != nil {
			return nil, err
		}
	}
	for _, ds := range dsList {
		dataIdEtlMap[ds.BkDataId] = ds.EtlConfig
	}

	measurementTypeMap := make(map[string]string)
	for _, table := range s.resultTableList {
		bkDataId := s.tableDataIdMap[table.TableId]
		etlConfig := dataIdEtlMap[bkDataId]
		// 获取是否禁用指标切分模式
		isDisableMetricCutter, err := NewResultTableSvc(nil).IsDisableMetricCutter(table.TableId)
		if err != nil {
			return nil, err
		}
		measurementTypeMap[table.TableId] = s.getMeasurementType(table.SchemaType, rtoMap[table.TableId], isDisableMetricCutter, etlConfig)
	}
	return measurementTypeMap, nil
}

// 获取表类型
func (s *SpacePusher) getMeasurementType(schemaType string, isSplitMeasurement, isDisableMetricCutter bool, etlConfig string) string {
	// - 当 schema_type 为 fixed 时，为多指标单表
	if schemaType == models.ResultTableSchemaTypeFixed {
		return models.MeasurementTypeBkTraditional
	}
	// - 当 schema_type 为 free 时，
	if schemaType == models.ResultTableSchemaTypeFree {
		// - 如果 is_split_measurement 为 True, 则为单指标单表
		if isSplitMeasurement {
			return models.MeasurementTypeBkSplit
		}
		// - is_split_measurement 为 False
		// - 如果etl_config 不为`bk_standard_v2_time_series`
		if etlConfig != models.ETLConfigTypeBkStandardV2TimeSeries {
			return models.MeasurementTypeBkExporter
		}
		// - etl_config 为`bk_standard_v2_time_series`，
		// - 如果 is_disable_metric_cutter 为 False，则为固定 metric_name，metric_value
		if !isDisableMetricCutter {
			return models.MeasurementTypeBkExporter
		}
		// - 否则为自定义多指标单表
		return models.MeasurementTypeBkStandardV2TimeSeries

	}
	return models.MeasurementTypeBkTraditional
}

// 获取结果表属性
func (s *SpacePusher) getTableFieldByTableId() (map[string][]string, error) {
	// 针对黑白名单，如果不是自动发现，则不采用最后更新时间过滤
	var rtoList []resulttable.ResultTableOption
	if len(s.tableIdList) != 0 {
		if err := resulttable.NewResultTableOptionQuerySet(mysql.GetDBSession().DB).TableIDIn(s.tableIdList...).NameEq(models.OptionEnableFieldBlackList).ValueEq("false").All(&rtoList); err != nil {
			return nil, err
		}
	}
	var whiteTableIdList []string
	for _, o := range rtoList {
		whiteTableIdList = append(whiteTableIdList, o.TableID)
	}
	tableIdList := slicex.StringSet2List(slicex.StringList2Set(s.tableIdList).Difference(slicex.StringList2Set(whiteTableIdList)))
	if len(tableIdList) == 0 {
		return make(map[string][]string), nil
	}
	// 针对自定义时序，过滤掉历史废弃的指标，时间在`TIME_SERIES_METRIC_EXPIRED_DAYS`的为有效数据, 其它类型直接获取所有指标和维度
	// 获取时序的结果表
	var tsGroupList []customreport.TimeSeriesGroup
	if err := customreport.NewTimeSeriesGroupQuerySet(mysql.GetDBSession().DB).TableIDIn(tableIdList...).All(&tsGroupList); err != nil {
		return nil, err
	}
	// 获取时序的结果表及关联的group id
	var tsGroupIdList []uint
	tsGroupIdTableIdMap := make(map[uint]string)
	var tsGroupTableId []string
	for _, group := range tsGroupList {
		tsGroupIdList = append(tsGroupIdList, group.TimeSeriesGroupID)
		tsGroupIdTableIdMap[group.TimeSeriesGroupID] = group.TableID
		tsGroupTableId = append(tsGroupTableId, group.TableID)
	}
	// 获取其它非自定义时序的结果表
	otherTableIdList := slicex.StringSet2List(slicex.StringList2Set(tableIdList).Difference(slicex.StringList2Set(tsGroupTableId)))
	// 通过结果表属性过滤相应数据
	otherTableIdList = slicex.StringSet2List(slicex.StringList2Set(otherTableIdList).Union(slicex.StringList2Set(whiteTableIdList)))
	// 针对自定义时序，按照时间过滤数据
	beginTime := time.Now().UTC().AddDate(0, 0, -viper.GetInt(globalconfig.TimeSeriesMetricExpiredDaysPath))
	var tsmList []customreport.TimeSeriesMetric
	if len(tsGroupIdList) != 0 {
		if err := customreport.NewTimeSeriesMetricQuerySet(mysql.GetDBSession().DB).GroupIDIn(tsGroupIdList...).LastModifyTimeGte(beginTime).All(&tsmList); err != nil {
			return nil, err
		}
	}
	// 组装结果表及对应的metric
	var tableFieldList []map[string]string
	for _, metric := range tsmList {
		tableFieldList = append(tableFieldList, map[string]string{
			"table_id":   tsGroupIdTableIdMap[metric.GroupID],
			"field_name": metric.FieldName,
		})
	}
	// 其它则获取所有指标数据
	var otherRTFList []resulttable.ResultTableField
	if len(otherTableIdList) != 0 {
		if err := resulttable.NewResultTableFieldQuerySet(mysql.GetDBSession().DB).TableIDIn(otherTableIdList...).TagEq(models.ResultTableFieldTagMetric).All(&otherRTFList); err != nil {
			return nil, err
		}
	}
	for _, field := range otherRTFList {
		tableFieldList = append(tableFieldList, map[string]string{
			"table_id":   field.TableID,
			"field_name": field.FieldName,
		})
	}

	tableFieldDict := make(map[string][]string)
	for _, tf := range tableFieldList {
		tableId := tf["table_id"]
		fieldName := tf["field_name"]
		if ls, ok := tableFieldDict[tableId]; ok {
			tableFieldDict[tableId] = append(ls, fieldName)
		} else {
			tableFieldDict[tableId] = []string{fieldName}
		}
	}
	return tableFieldDict, nil
}

// 通过结果表获取结果表下的分段处理是否开启
func (s *SpacePusher) getSegmentedOptionByTableId() (map[string]bool, error) {
	if len(s.tableIdList) == 0 {
		return make(map[string]bool), nil
	}
	var rtoList []resulttable.ResultTableOption
	if err := resulttable.NewResultTableOptionQuerySet(mysql.GetDBSession().DB).NameEq(models.OptionSegmentedQueryEnable).TableIDIn(s.tableIdList...).All(&rtoList); err != nil {
		return nil, err
	}
	fieldMap := make(map[string]bool)
	for _, option := range rtoList {
		var value bool
		if err := jsonx.UnmarshalString(option.Value, &value); err != nil {
			return nil, err
		}
		fieldMap[option.TableID] = value
	}
	return fieldMap, nil
}

// 判断是否需要添加filter
func (s *SpacePusher) isNeedAddFilter(measurementType, spaceTypeId, spaceId string, dataId uint) (bool, error) {
	// 防止脏数据导致查询不到异常抛出的情况
	var ds resulttable.DataSource
	if err := resulttable.NewDataSourceQuerySet(mysql.GetDBSession().DB).BkDataIdEq(dataId).One(&ds); err != nil {
		if gorm.IsRecordNotFoundError(err) {
			logger.Errorf("query datasource [%v] error, %v", dataId, err)
			return true, nil
		} else {
			return true, err
		}
	}
	// 为防止查询范围放大，先功能开关控制，针对归属到具体空间的数据源，不需要添加过滤条件
	if !viper.GetBool(globalconfig.IsRestrictDsBelongSpacePath) && (ds.SpaceTypeId == fmt.Sprintf("%s__%s", spaceTypeId, spaceId)) {
		return false, nil
	}
	// 如果不是自定义时序或exporter，则不需要关注类似的情况，必须增加过滤条件
	tsMeasurementTypes := []string{models.MeasurementTypeBkSplit, models.MeasurementTypeBkStandardV2TimeSeries, models.MeasurementTypeBkExporter}
	if ds.EtlConfig != models.ETLConfigTypeBkStandardV2TimeSeries {
		var exist bool
		for _, tp := range tsMeasurementTypes {
			if tp == measurementType {
				exist = true
				break
			}
		}
		if !exist {
			return true, nil
		}
	}
	// 对自定义插件的处理，兼容黑白名单对类型的更改
	// 黑名单时，会更改为单指标单表
	if measurementType == models.MeasurementTypeBkExporter || (ds.EtlConfig == models.ETLConfigTypeBkExporter && measurementType == models.MeasurementTypeBkSplit) {
		// 如果space_id与data_id所属空间UID相同，则不需要过滤
		if ds.SpaceUid == fmt.Sprintf("%s__%s", spaceTypeId, spaceId) {
			return false, nil
		}
		return true, nil
	}

	var sds space.SpaceDataSource
	if err := space.NewSpaceDataSourceQuerySet(mysql.GetDBSession().DB).SpaceTypeIdEq(spaceTypeId).SpaceIdEq(spaceId).BkDataIdEq(dataId).One(&sds); err != nil {
		if gorm.IsRecordNotFoundError(err) {
			logger.Errorf("SpaceDataSource space [%s__%s], data_id [%v] not found", spaceTypeId, spaceId, dataId)
			return true, nil
		} else {
			return true, err
		}
	}

	// 可以执行到以下代码，必然是自定义时序的数据源
	// 1. 非公共的(全空间或指定空间类型)自定义时序，查询时，不需要任何查询条件
	if !ds.IsPlatformDataId {
		return false, nil
	}
	// 可以执行 到以下代码，必然是自定义时序，且是公共平台数据源
	// 2. 公共自定义时序，如果属于当前space，不需要添加过滤条件
	if sds.SpaceId == spaceId {
		return false, nil
	}
	// 3. 此时，必然是自定义时序，且是公共的平台数据源，同时非该当前空间下，需要添加过滤条件
	return true, nil
}

func (s *SpacePusher) getDataIdByCluster(clusterId string) (map[string][]uint, error) {
	qs := bcs.NewBCSClusterInfoQuerySet(mysql.GetDBSession().DB).StatusEq(models.BcsClusterStatusRunning)
	if clusterId != "" {
		qs = qs.ClusterIDEq(clusterId)
	}
	var clusterList []bcs.BCSClusterInfo
	if err := qs.All(&clusterList); err != nil {
		return nil, err
	}
	// 组装格式为: {cluster_id: set([data_id1, data_id2])}
	clusterDataIdMap := make(map[string][]uint)
	for _, cluster := range clusterList {
		ls, ok := clusterDataIdMap[clusterId]
		if ok {
			clusterDataIdMap[clusterId] = append(ls, cluster.K8sMetricDataID, cluster.CustomMetricDataID)
		} else {
			clusterDataIdMap[clusterId] = []uint{cluster.K8sMetricDataID, cluster.CustomMetricDataID}
		}
	}
	for k, v := range clusterDataIdMap {
		clusterDataIdMap[k] = slicex.UintSet2List(slicex.UintList2Set(v))
	}
	return clusterDataIdMap, nil
}
