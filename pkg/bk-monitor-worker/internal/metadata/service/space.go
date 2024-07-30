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
	"fmt"
	"strconv"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/pkg/errors"
	"github.com/samber/lo"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/apiservice"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/bcs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/diffutil"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mapx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/slicex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// SpaceSvc space service
type SpaceSvc struct {
	*space.Space
}

func NewSpaceSvc(obj *space.Space) SpaceSvc {
	return SpaceSvc{
		Space: obj,
	}
}

// RefreshBkccSpaceName 刷新 bkcc 类型空间名称
func (s *SpaceSvc) RefreshBkccSpaceName() error {
	// 获取bkcc业务cmdb数据信息
	bizIdNameMap, err := s.getBkccBizIdNameMap()
	if err != nil {
		return errors.Wrap(err, "getBkccBizIdNameMap failed")
	}
	// 更新数据库中记录
	db := mysql.GetDBSession().DB
	var spaceList []space.Space
	if err := space.NewSpaceQuerySet(db).SpaceTypeIdEq(models.SpaceTypeBKCC).All(&spaceList); err != nil {
		return errors.Wrap(err, "query bkcc space failed")
	}
	for _, sp := range spaceList {
		oldName := sp.SpaceName
		name, ok := bizIdNameMap[sp.SpaceId]
		// 不存在则跳过
		if !ok {
			continue
		}
		if name == oldName {
			continue
		}
		// 名称变动，需要更新和更新时间
		metrics.MysqlCount(sp.TableName(), "RefreshBkccSpaceName_update", 1)
		sp.SpaceName = name
		sp.UpdateTime = time.Now()
		if err := sp.Update(db, space.SpaceDBSchema.SpaceName, space.SpaceDBSchema.UpdateTime); err != nil {
			logger.Errorf("update bkcc space name [%s] to [%s] failed, %v", oldName, sp.SpaceName, err)
			continue
		}
		logger.Infof("update bkcc space name [%s] to [%s]", oldName, sp.SpaceName)
	}
	return nil
}

// RefreshBkccSpace 同步 bkcc 的业务，自动创建对应的空间
func (s *SpaceSvc) RefreshBkccSpace(allowDelete bool) error {
	// 获取bkcc业务cmdb数据信息
	bizIdNameMap, err := s.getBkccBizIdNameMap()
	if err != nil {
		return errors.Wrap(err, "getBkccBizIdNameMap failed")
	}
	if len(bizIdNameMap) == 0 {
		logger.Errorf("query bkcc biz info is empty")
		return nil
	}
	// 提取 bkcc 中业务列表
	bizIdList := mapx.GetMapKeys(bizIdNameMap)

	// 获取已经存在的业务列表
	db := mysql.GetDBSession().DB
	var spaceList []space.Space
	if err := space.NewSpaceQuerySet(db).Select(space.SpaceDBSchema.SpaceId).SpaceTypeIdEq(models.SpaceTypeBKCC).All(&spaceList); err != nil {
		logger.Errorf("query bkcc space failed, error: %s", err)
		return errors.Wrap(err, "query bkcc space failed")
	}
	var existSpaceIdList []string
	for _, sp := range spaceList {
		existSpaceIdList = append(existSpaceIdList, sp.SpaceId)
	}

	// 比对
	needCreateBizIdList, needDeleteBizIdList := lo.Difference(bizIdList, existSpaceIdList)

	// 如果有删除的数据，则记录
	if len(needDeleteBizIdList) != 0 && allowDelete {
		logger.Info("some biz need to delete, biz_id_list: %v", needDeleteBizIdList)
	}

	// 如果需要创建的业务为空，并且不需要删除，则直接返回
	if len(needCreateBizIdList) == 0 && !allowDelete {
		logger.Info("not biz need to create")
		return nil
	}
	// 创建数据
	for _, bizId := range needCreateBizIdList {
		// 获取业务名称
		bizName := bizIdNameMap[bizId]
		sp := space.Space{
			SpaceTypeId: models.SpaceTypeBKCC,
			SpaceId:     bizId,
			SpaceName:   bizName,
		}
		if err := sp.Create(db); err != nil {
			logger.Errorf("create Space with space_type_id [%s] space_id [%s] space_name [%s] failed, %s", models.SpaceTypeBKCC, bizId, bizName, err)
			continue
		}
	}
	// 如果允许删除，并且比较的删除数据的不为空，则更新业务对应的名称
	// 更新规则: "(已归档_{datetime.now().strftime('%Y%m%d')})"
	if len(needDeleteBizIdList) != 0 && allowDelete {
		for _, bizId := range needDeleteBizIdList {
			// 按照规则，更新名称
			// 查询记录
			qs := space.NewSpaceQuerySet(db).SpaceTypeIdEq(models.SpaceTypeBKCC).SpaceIdEq(bizId)
			var ds space.Space
			if err := qs.One(&ds); err != nil {
				logger.Errorf("query space not error, space_type [%s] space_id [%s], %s", models.SpaceTypeBKCC, bizId, err)
				continue
			}
			// 更新空间名称
			newName := fmt.Sprintf("%s(已归档_%s)", ds.SpaceName, time.Now().Format("20060102"))
			if err := qs.GetUpdater().SetSpaceName(newName).Update(); err != nil {
				logger.Errorf("space_type [%s] space_id [%s] update space_name [%s] to [%s] failed, %s", ds.SpaceName, newName, err)
			}
		}
	}

	return nil
}

// 获取bkcc业务cmdb数据信息
func (*SpaceSvc) getBkccBizIdNameMap() (map[string]string, error) {
	cmdbApi, err := api.GetCmdbApi()
	if err != nil {
		return nil, errors.Wrap(err, "get cmdb api failed")
	}
	var bizResp cmdb.SearchBusinessResp
	if _, err := cmdbApi.SearchBusiness().SetResult(&bizResp).Request(); err != nil {
		return nil, errors.Wrap(err, "search business failed")
	}
	bizIdNameMap := make(map[string]string)
	for _, info := range bizResp.Data.Info {
		// 过滤出bkcc业务
		if info.BkBizId > 0 {
			bizIdStr := strconv.Itoa(info.BkBizId)
			bizIdNameMap[bizIdStr] = info.BkBizName
		}
	}
	return bizIdNameMap, nil
}

// SyncBcsSpace 同步 BCS 项目空间数据
func (s *SpaceSvc) SyncBcsSpace() error {
	projects, err := s.GetValidBcsProjects()
	if err != nil {
		return errors.Wrap(err, "getValidBcsProjects failed")
	}
	projectIdMap := make(map[string]map[string]string)
	projectIdSet := mapset.NewSet[string]()
	for _, p := range projects {
		projectIdMap[p["projectCode"]] = p
		projectIdSet.Add(p["projectCode"])
	}

	db := mysql.GetDBSession().DB
	var spaceList []space.Space
	if err := space.NewSpaceQuerySet(db).Select(space.SpaceDBSchema.SpaceId, space.SpaceDBSchema.SpaceCode).SpaceTypeIdEq(models.SpaceTypeBKCI).All(&spaceList); err != nil {
		return errors.Wrapf(err, "query Space with space_type_id [%s] failed", models.SpaceTypeBKCI)
	}
	spaceIdSet := mapset.NewSet[string]()
	existProjectIdSet := mapset.NewSet[string]()
	for _, sp := range spaceList {
		spaceIdSet.Add(sp.SpaceId)
		if sp.SpaceCode == "" {
			existProjectIdSet.Add(sp.SpaceId)
		}
	}
	// 判断需要更新的项目
	var updateProjects []string
	for id := range projectIdMap {
		if existProjectIdSet.Contains(id) {
			updateProjects = append(updateProjects, id)
		}
	}
	// 判断是否有新项目增加
	diff := projectIdSet.Difference(spaceIdSet)
	if diff.Cardinality() == 0 && len(updateProjects) == 0 {
		logger.Infof("bcs space not need add or update!")
		return nil
	}

	// 更新 space_code
	for _, projectId := range updateProjects {
		spaceCode := projectIdMap[projectId]["projectId"]
		spaceName := projectIdMap[projectId]["name"]
		metrics.MysqlCount(space.Space{}.TableName(), "SyncBcsSpace_update", 1)
		if cfg.BypassSuffixPath != "" && !slicex.IsExistItem(cfg.SkipBypassTasks, "sync_bcs_space") {
			logger.Info(diffutil.BuildLogStr("sync_bcs_space", diffutil.OperatorTypeDBUpdate, diffutil.NewSqlBody(space.Space{}.TableName(), map[string]interface{}{
				space.SpaceDBSchema.SpaceTypeId.String(): models.SpaceTypeBKCI,
				space.SpaceDBSchema.SpaceId.String():     projectId,
				space.SpaceDBSchema.SpaceCode.String():   spaceCode,
				space.SpaceDBSchema.SpaceName.String():   spaceName,
				space.SpaceDBSchema.IsBcsValid.String():  true,
			}), ""))
		} else {
			if err := space.NewSpaceQuerySet(db).SpaceTypeIdEq(models.SpaceTypeBKCI).SpaceIdEq(projectId).GetUpdater().SetSpaceCode(spaceCode).SetSpaceName(spaceName).SetIsBcsValid(true).SetUpdateTime(time.Now()).Update(); err != nil {
				return errors.Wrapf(err, "update Space space_type [%s] space_id [%s] with space_code [%s] space_name [%s] is_bcs_valid [%v] failed", models.SpaceTypeBKCI, projectId, spaceCode, spaceName, true)
			}
		}
	}
	// 创建空间、空间资源、使用业务的 data id
	for projectId := range diff.Iter() {
		project := projectIdMap[projectId]
		if err := s.CreateBcsSpace(project); err != nil {
			logger.Errorf("create bcs space [%v] failed, %v", project, err)
			continue
		}
		logger.Infof("create bcs space [%v] success", project)
	}
	return nil
}

// CreateBcsSpace 创建容器对应的空间信息，需要检查业务下的 ID 及关联资源(业务和集群及命名空间)
func (s *SpaceSvc) CreateBcsSpace(project map[string]string) error {
	db := mysql.GetDBSession().DB
	tx := db.Begin()
	projectCode := project["projectCode"]
	name := project["name"]
	projectId := project["projectId"]
	bkBizId := project["bkBizId"]
	sp := space.Space{
		SpaceTypeId: models.SpaceTypeBKCI,
		SpaceId:     projectCode,
		SpaceName:   name,
		SpaceCode:   projectId,
		IsBcsValid:  true,
	}
	logger.Infof("[db_diff]create Space with space_type_id [%s] space_id [%s] space_name [%s] space_code [%s] is_bcs_valid [%v]", models.SpaceTypeBKCI, projectCode, name, projectId, true)
	metrics.MysqlCount(sp.TableName(), "CreateBcsSpace_create_bkci", 1)
	if cfg.BypassSuffixPath != "" && !slicex.IsExistItem(cfg.SkipBypassTasks, "sync_bcs_space") {
		logger.Info(diffutil.BuildLogStr("sync_bcs_space", diffutil.OperatorTypeDBCreate, diffutil.NewSqlBody(sp.TableName(), map[string]interface{}{
			space.SpaceDBSchema.SpaceTypeId.String(): sp.SpaceTypeId,
			space.SpaceDBSchema.SpaceId.String():     sp.SpaceId,
			space.SpaceDBSchema.SpaceName.String():   sp.SpaceName,
			space.SpaceDBSchema.SpaceCode.String():   sp.SpaceCode,
			space.SpaceDBSchema.IsBcsValid.String():  sp.IsBcsValid,
			space.SpaceDBSchema.Status.String():      "normal",
			space.SpaceDBSchema.TimeZone.String():    "Asia/Shanghai",
			space.SpaceDBSchema.Language.String():    "zh-hans",
		}), ""))
	} else {
		if err := sp.Create(tx); err != nil {
			tx.Rollback()
			return errors.Wrapf(err, "create Space with space_type_id [%s] space_id [%s] space_name [%s] space_code [%s] is_bcs_valid [%v] fialed", models.SpaceTypeBKCI, projectCode, name, projectId, true)
		}
	}
	// 获取业务下的 data id, 然后授权给项目使用
	var dataIdList []uint
	var sdsList []space.SpaceDataSource
	if err := space.NewSpaceDataSourceQuerySet(tx).Select(space.SpaceDataSourceDBSchema.BkDataId).SpaceTypeIdEq(models.SpaceTypeBKCC).SpaceIdEq(bkBizId).FromAuthorizationEq(false).All(&sdsList); err != nil {
		tx.Rollback()
		return errors.Wrapf(err, "query SpaceDataSource with space_type_id [%s] space_id [%s] from_authorization [%v] failed", models.SpaceTypeBKCC, bkBizId, false)
	}
	for _, sds := range sdsList {
		dataIdList = append(dataIdList, sds.BkDataId)
	}
	// 添加 bkcc 空间级 data id
	var dsList []resulttable.DataSource
	if err := resulttable.NewDataSourceQuerySet(tx).Select(resulttable.DataSourceDBSchema.BkDataId).IsPlatformDataIdEq(true).SpaceTypeIdEq(models.SpaceTypeBKCC).All(&dsList); err != nil {
		tx.Rollback()
		return errors.Wrapf(err, "query DataSource with is_paltform_data_id [%v], space_type_id [%s] failed", true, models.SpaceTypeBKCC)
	}
	for _, ds := range dsList {
		dataIdList = append(dataIdList, ds.BkDataId)
	}

	// 添加项目关联的业务资源
	dmForBkcc := []map[string]interface{}{{"bk_biz_id": bkBizId}}
	srForBkcc := space.SpaceResource{
		SpaceTypeId:  models.SpaceTypeBKCI,
		SpaceId:      projectCode,
		ResourceType: models.SpaceTypeBKCC,
		ResourceId:   &bkBizId,
		BaseModel:    models.BaseModel{},
	}
	if err := srForBkcc.SetDimensionValues(dmForBkcc); err != nil {
		tx.Rollback()
		return errors.Wrapf(err, "set dimention_values [%v] for SpaceResource failed", dmForBkcc)
	}
	logger.Infof("[db_diff]create SpaceResource with space_type_id [%s] space_id [%s] resource_type [%s] resource_id [%v]", srForBkcc.SpaceTypeId, srForBkcc.SpaceId, srForBkcc.ResourceType, srForBkcc.ResourceId)
	metrics.MysqlCount(srForBkcc.TableName(), "CreateBcsSpace_create_bkcc", 1)
	if cfg.BypassSuffixPath != "" && !slicex.IsExistItem(cfg.SkipBypassTasks, "sync_bcs_space") {
		logger.Info(diffutil.BuildLogStr("sync_bcs_space", diffutil.OperatorTypeDBCreate, diffutil.NewSqlBody(srForBkcc.TableName(), map[string]interface{}{
			space.SpaceResourceDBSchema.SpaceTypeId.String():     srForBkcc.SpaceTypeId,
			space.SpaceResourceDBSchema.SpaceId.String():         srForBkcc.SpaceId,
			space.SpaceResourceDBSchema.ResourceType.String():    srForBkcc.ResourceType,
			space.SpaceResourceDBSchema.ResourceId.String():      srForBkcc.ResourceId,
			space.SpaceResourceDBSchema.DimensionValues.String(): srForBkcc.DimensionValues,
		}), ""))
	} else {
		if err := srForBkcc.Create(tx); err != nil {
			tx.Rollback()
			return errors.Wrapf(err, "create SpaceResource with space_type_id [%s] space_id [%s] resource_type [%s] resource_id [%v] failed", srForBkcc.SpaceTypeId, srForBkcc.SpaceId, srForBkcc.ResourceType, srForBkcc.ResourceId)
		}
	}
	// 组装空间对应的资源
	// 获取存储在metadata中的集群数据
	var metadataClusterId []string
	var bcsClusterInfoList []bcs.BCSClusterInfo
	if err := bcs.NewBCSClusterInfoQuerySet(tx).Select(bcs.BCSClusterInfoDBSchema.ClusterID).StatusNotIn(models.BcsClusterStatusDeleted, models.BcsRawClusterStatusDeleted).All(&bcsClusterInfoList); err != nil {
		tx.Rollback()
		return errors.Wrap(err, "query BCSClusterInfo failed")
	}
	for _, c := range bcsClusterInfoList {
		metadataClusterId = append(metadataClusterId, c.ClusterID)
	}

	// 查询项目下集群，防止出现查询所有集群超时问题
	clusterList, err := apiservice.BcsClusterManager.GetProjectClusters(projectId, false)
	if err != nil {
		tx.Rollback()
		return errors.Wrapf(err, "GetProjectClusters project_id [%s] failed", projectId)
	}

	// 组装空间和 data id 的关系数据，同步过来的是业务下的 data id，相当于授权给容器项目使用
	var sharedClusterDataIdList, projectClusterDataIdList []uint
	// 关联项目下的集群, 针对共享集群，需要获取对应的空间信息
	var projectClusterNsList []map[string]interface{}
	for _, c := range clusterList {
		clusterId, ok := c["clusterId"].(string)
		if !ok || !slicex.IsExistItem(metadataClusterId, clusterId) {
			continue
		}
		var clusterDataIdList []uint
		var cluster bcs.BCSClusterInfo
		if err := bcs.NewBCSClusterInfoQuerySet(tx).StatusNotIn(models.BcsClusterStatusDeleted, models.BcsRawClusterStatusDeleted).ClusterIDEq(clusterId).One(&cluster); err != nil {
			tx.Rollback()
			return errors.Wrapf(err, "query BCSClusterInfo with cluster_id [%s] status [%s] failed", clusterId, models.BcsClusterStatusRunning)
		}
		clusterDataIdList = append(clusterDataIdList, cluster.K8sMetricDataID, cluster.CustomMetricDataID)
		isShared, _ := c["isShared"].(bool)
		if isShared {
			sharedClusterDataIdList = append(sharedClusterDataIdList, clusterDataIdList...)
			namespaceInfos, err := apiservice.Bcs.FetchSharedClusterNamespaces(clusterId, projectCode)
			if err != nil {
				tx.Rollback()
				return errors.Wrapf(err, "FetchSharedClusterNamespaces with cluster_id [%s] project_code [%s] failed", clusterId, projectCode)
			}
			var nsList []string
			for _, info := range namespaceInfos {
				nsList = append(nsList, info["namespace"])
			}
			projectClusterNsList = append(projectClusterNsList, map[string]interface{}{"cluster_id": clusterId, "namespace": nsList, "cluster_type": models.BcsClusterTypeShared})
		} else {
			projectClusterDataIdList = append(projectClusterDataIdList, clusterDataIdList...)
			projectClusterNsList = append(projectClusterNsList, map[string]interface{}{"cluster_id": clusterId, "namespace": nil, "cluster_type": models.BcsClusterTypeSingle})
		}
	}

	srForBcs := space.SpaceResource{
		SpaceTypeId:  models.SpaceTypeBKCI,
		SpaceId:      projectCode,
		ResourceType: models.SpaceTypeBCS,
		ResourceId:   &projectCode,
	}
	if err := srForBcs.SetDimensionValues(projectClusterNsList); err != nil {
		tx.Rollback()
		return errors.Wrapf(err, "set dimention_values [%v] for SpaceResource failed", projectClusterNsList)
	}
	logger.Infof("[db_diff]create SpaceResource with space_type_id [%s] space_id [%s] resource_type [%s] resource_id [%v]", srForBcs.SpaceTypeId, srForBcs.SpaceId, srForBcs.ResourceType, srForBcs.ResourceId)
	metrics.MysqlCount(srForBcs.TableName(), "CreateBcsSpace_create_bcs", 1)
	if cfg.BypassSuffixPath != "" && !slicex.IsExistItem(cfg.SkipBypassTasks, "sync_bcs_space") {
		logger.Info(diffutil.BuildLogStr("sync_bcs_space", diffutil.OperatorTypeDBCreate, diffutil.NewSqlBody(srForBcs.TableName(), map[string]interface{}{
			space.SpaceResourceDBSchema.SpaceTypeId.String():     srForBcs.SpaceTypeId,
			space.SpaceResourceDBSchema.SpaceId.String():         srForBcs.SpaceId,
			space.SpaceResourceDBSchema.ResourceType.String():    srForBcs.ResourceType,
			space.SpaceResourceDBSchema.ResourceId.String():      srForBcs.ResourceId,
			space.SpaceResourceDBSchema.DimensionValues.String(): srForBcs.DimensionValues,
		}), ""))
	} else {
		if err := srForBcs.Create(tx); err != nil {
			tx.Rollback()
			return errors.Wrapf(err, "create SpaceResource with space_type_id [%s] space_id [%s] resource_type [%s] resource_id [%v] failed", srForBcs.SpaceTypeId, srForBcs.SpaceId, srForBcs.ResourceType, srForBcs.ResourceId)
		}
	}
	spaceDataSourceList, err := s.composeBcsSpaceDataSource(models.SpaceTypeBKCI, projectCode, dataIdList, projectClusterDataIdList, sharedClusterDataIdList)
	if err != nil {
		tx.Rollback()
		return errors.Wrapf(err, "composeBcsSpaceDataSource with space_type_id [%s] space_id [%s] data_id_list [%v] project_cluster_data_id_list [%v] shared_cluster_data_id_list [%v] failed", models.SpaceTypeBKCI, projectCode, dataIdList, projectClusterDataIdList, sharedClusterDataIdList)
	}
	for _, sds := range spaceDataSourceList {
		logger.Infof("[db_diff]create SpaceDataSource with space_type_id [%s] space_id [%s] bk_data_id [%v] from_authorization [%v]", sds.SpaceTypeId, sds.SpaceId, sds.BkDataId, sds.FromAuthorization)
		metrics.MysqlCount(sds.TableName(), "CreateBcsSpace_create_sds", 1)
		if cfg.BypassSuffixPath != "" && !slicex.IsExistItem(cfg.SkipBypassTasks, "sync_bcs_space") {
			logger.Info(diffutil.BuildLogStr("sync_bcs_space", diffutil.OperatorTypeDBCreate, diffutil.NewSqlBody(sds.TableName(), map[string]interface{}{
				space.SpaceDataSourceDBSchema.SpaceTypeId.String():       sds.SpaceTypeId,
				space.SpaceDataSourceDBSchema.SpaceId.String():           sds.SpaceId,
				space.SpaceDataSourceDBSchema.BkDataId.String():          sds.BkDataId,
				space.SpaceDataSourceDBSchema.FromAuthorization.String(): sds.FromAuthorization,
			}), ""))
		} else {
			if err := sds.Create(tx); err != nil {
				tx.Rollback()
				return errors.Wrapf(err, "create SpaceDataSource with space_type_id [%s] space_id [%s] bk_data_id [%v] from_authorization [%v] failed", sds.SpaceTypeId, sds.SpaceId, sds.BkDataId, sds.FromAuthorization)
			}
		}
	}
	if cfg.BypassSuffixPath != "" && !slicex.IsExistItem(cfg.SkipBypassTasks, "sync_bcs_space") {
		tx.Rollback()
	} else {
		tx.Commit()
	}
	return nil
}

func (*SpaceSvc) composeBcsSpaceDataSource(spaceTypeId, spaceId string, dataIdList, projectClusterDataIdList, sharedClusterDataIdList []uint) ([]*space.SpaceDataSource, error) {
	// 处理重复的数据
	projectClusterDataIdSet := mapset.NewSet(projectClusterDataIdList...)
	sharedClusterDataIdSet := mapset.NewSet(sharedClusterDataIdList...)
	dataIdSet := mapset.NewSet(dataIdList...)
	clusterDataIdSet := projectClusterDataIdSet.Union(sharedClusterDataIdSet)

	db := mysql.GetDBSession().DB
	// 获取db中的dataid
	filterDataIdSet := mapset.NewSet[uint]()
	var dsList []resulttable.DataSource
	if clusterDataIdSet.Cardinality() != 0 {
		dataids := clusterDataIdSet.ToSlice()
		if err := resulttable.NewDataSourceQuerySet(db).Select(resulttable.DataSourceDBSchema.BkDataId).EtlConfigIn(models.SpaceDataSourceETLList...).BkDataIdIn(dataids...).All(&dsList); err != nil {
			return nil, errors.Wrapf(err, "query DataSource with etl_config [%v] bk_data_id [%v] failed", models.SpaceDataSourceETLList, dataids)
		}
	}
	for _, ds := range dsList {
		filterDataIdSet.Add(ds.BkDataId)
	}
	var data []*space.SpaceDataSource
	diffDataIdSet := dataIdSet.Difference(clusterDataIdSet)
	for id := range diffDataIdSet.Iter() {
		if !filterDataIdSet.Contains(id) {
			continue
		}
		data = append(data, &space.SpaceDataSource{
			SpaceTypeId:       spaceTypeId,
			SpaceId:           spaceId,
			BkDataId:          id,
			FromAuthorization: true,
		})
	}

	for id := range clusterDataIdSet.Iter() {
		if !filterDataIdSet.Contains(id) {
			continue
		}
		data = append(data, &space.SpaceDataSource{
			SpaceTypeId:       spaceTypeId,
			SpaceId:           spaceId,
			BkDataId:          id,
			FromAuthorization: false,
		})
	}
	return data, nil
}

// RefreshBcsProjectBiz 检测 bcs 项目绑定的业务的变化
func (s *SpaceSvc) RefreshBcsProjectBiz() error {
	// 检测所有 bcs 项目
	projects, err := s.GetValidBcsProjects()
	if err != nil {
		return errors.Wrap(err, "getValidBcsProjects failed")
	}
	projectIdBizIdMap := make(map[string]string)
	for _, p := range projects {
		projectIdBizIdMap[p["projectCode"]] = p["bkBizId"]
	}
	db := mysql.GetDBSession().DB
	var srList []space.SpaceResource
	if err := space.NewSpaceResourceQuerySet(db).SpaceTypeIdEq(models.SpaceTypeBKCI).ResourceTypeEq(models.SpaceTypeBKCC).All(&srList); err != nil {
		return errors.Wrap(err, "query SpaceResource with space_type_id [bkci] resource_type [bkcc] failed")
	}
	spaceIdResourceMap := make(map[string]*space.SpaceResource)
	for _, r := range srList {
		spaceIdResourceMap[r.SpaceId] = &r
	}

	var updateSpaceIdList []string
	var createSpaceIdList []string

	var spaceList []space.Space
	if err := space.NewSpaceQuerySet(db).SpaceTypeIdEq(models.SpaceTypeBKCI).SpaceCodeNe("").All(&spaceList); err != nil {
		return errors.Wrap(err, "query Space with space_type_id [bkci] failed")
	}
	for _, sp := range spaceList {
		// 获取project对应的业务信息
		bizId, ok := projectIdBizIdMap[sp.SpaceId]
		if !ok {
			// 获取不到则跳过
			continue
		}
		// 构造dimension_values
		dm := []map[string]interface{}{{"bk_biz_id": bizId}}
		res, ok := spaceIdResourceMap[sp.SpaceId]
		if !ok {
			// SpaceResource不存在则创建
			sr := space.SpaceResource{
				SpaceTypeId:  models.SpaceTypeBKCI,
				SpaceId:      sp.SpaceId,
				ResourceType: models.SpaceTypeBKCC,
				ResourceId:   &bizId,
			}
			if err := sr.SetDimensionValues(dm); err != nil {
				logger.Errorf("set dimension_values [%v] for SpaceResource failed, %v", dm, err)
				continue
			}
			metrics.MysqlCount(space.SpaceResource{}.TableName(), "RefreshBcsProjectBiz_create", 1)
			if cfg.BypassSuffixPath != "" && !slicex.IsExistItem(cfg.SkipBypassTasks, "refresh_bcs_project_biz") {
				logger.Info(diffutil.BuildLogStr("refresh_bcs_project_biz", diffutil.OperatorTypeDBCreate, diffutil.NewSqlBody(sr.TableName(), map[string]interface{}{
					space.SpaceResourceDBSchema.SpaceTypeId.String():     sr.SpaceTypeId,
					space.SpaceResourceDBSchema.SpaceId.String():         sr.SpaceId,
					space.SpaceResourceDBSchema.ResourceType.String():    sr.ResourceType,
					space.SpaceResourceDBSchema.ResourceId.String():      sr.ResourceId,
					space.SpaceResourceDBSchema.DimensionValues.String(): sr.DimensionValues,
				}), ""))
			} else {
				if err := sr.Create(db); err != nil {
					logger.Errorf("create SpaceResource with space_type_id [%s] space_id [%s] resource_type [%s] resource_id [%v] dimension_values [%s] failed, %v", sr.SpaceTypeId, sr.SpaceId, sr.ResourceType, sr.ResourceId, sr.DimensionValues, err)
					continue
				}
			}
			createSpaceIdList = append(createSpaceIdList, sp.SpaceId)
			continue
		}
		// ResourceId与业务id一致则跳过
		if res.ResourceId != nil && *res.ResourceId == bizId {
			continue
		}
		// 更新ResourceId和dimension_values
		res.ResourceId = &bizId
		if err := res.SetDimensionValues(dm); err != nil {
			logger.Errorf("set dimension_values [%v] for SpaceResource failed, %v", dm, err)
			continue
		}
		metrics.MysqlCount(space.SpaceResource{}.TableName(), "RefreshBcsProjectBiz_update", 1)
		if cfg.BypassSuffixPath != "" && !slicex.IsExistItem(cfg.SkipBypassTasks, "refresh_bcs_project_biz") {
			logger.Info(diffutil.BuildLogStr("refresh_bcs_project_biz", diffutil.OperatorTypeDBUpdate, diffutil.NewSqlBody(res.TableName(), map[string]interface{}{
				space.SpaceResourceDBSchema.Id.String():              res.Id,
				space.SpaceResourceDBSchema.ResourceId.String():      res.ResourceId,
				space.SpaceResourceDBSchema.DimensionValues.String(): res.DimensionValues,
			}), ""))
		} else {
			if err := res.Update(db, space.SpaceResourceDBSchema.ResourceId, space.SpaceResourceDBSchema.DimensionValues, space.SpaceResourceDBSchema.UpdateTime); err != nil {
				logger.Errorf("update SpaceResource id [%v] with dimension_values [%v] resource_id [%v] failed, %v", res.Id, dm, res.ResourceId, err)
				continue
			}
		}
		updateSpaceIdList = append(updateSpaceIdList, sp.SpaceId)
	}
	logger.Infof("bcs space resource created %v, updated %v", createSpaceIdList, updateSpaceIdList)
	return nil
}

// GetValidBcsProjects 获取可用的 BKCI(BCS) 项目空间
func (s *SpaceSvc) GetValidBcsProjects() ([]map[string]string, error) {
	bizIdNameMap, err := s.getBkccBizIdNameMap()
	if err != nil {
		return nil, errors.Wrap(err, "getBkccBizIdNameMap failed")
	}
	bizIdList := mapx.GetMapKeys(bizIdNameMap)
	// 返回有效的项目记录
	projectList, err := apiservice.BcsProject.BatchGetProjects("k8s")
	if err != nil {
		return nil, errors.Wrap(err, "BatchGetProjects failed")
	}
	var projects []map[string]string
	for _, p := range projectList {
		if p["bkBizId"] == "0" || !slicex.IsExistItem(bizIdList, p["bkBizId"]) {
			continue
		}
		projects = append(projects, p)
	}
	return projects, nil
}

// RefreshBkciSpaceName 刷新 bkci 类型空间名称
func (s *SpaceSvc) RefreshBkciSpaceName() error {
	projects, err := apiservice.BcsProject.BatchGetProjects("k8s")
	if err != nil {
		return errors.Wrap(err, "GetBkciProjects failed")
	}
	if len(projects) == 0 {
		return nil
	}
	projectCodeNameMap := make(map[string]string)
	for _, p := range projects {
		projectCodeNameMap[p["projectCode"]] = p["name"]
	}

	// 更新数据库中记录
	db := mysql.GetDBSession().DB
	var spaceList []space.Space
	if err := space.NewSpaceQuerySet(db).SpaceTypeIdEq(models.SpaceTypeBKCI).All(&spaceList); err != nil {
		return errors.Wrap(err, "query bkci space failed")
	}
	for _, sp := range spaceList {
		oldName := sp.SpaceName
		name, ok := projectCodeNameMap[sp.SpaceId]
		// 不存在则跳过
		if !ok {
			logger.Errorf("space not found from bkci api, space_id [%s] space_name [%s]", sp.SpaceId, sp.SpaceName)
			continue
		}
		if name == oldName {
			continue
		}
		// 名称变动，需要更新
		sp.SpaceName = name
		sp.UpdateTime = time.Now()
		metrics.MysqlCount(sp.TableName(), "RefreshBkciSpaceName_update", 1)
		if cfg.BypassSuffixPath != "" && !slicex.IsExistItem(cfg.SkipBypassTasks, "refresh_bkci_space_name") {
			logger.Info(diffutil.BuildLogStr("refresh_bkci_space_name", diffutil.OperatorTypeDBUpdate, diffutil.NewSqlBody(sp.TableName(), map[string]interface{}{
				space.SpaceDBSchema.Id.String():        sp.Id,
				space.SpaceDBSchema.SpaceName.String(): sp.SpaceName,
			}), ""))
		} else {
			if err := sp.Update(db, space.SpaceDBSchema.SpaceName, space.SpaceDBSchema.UpdateTime); err != nil {
				logger.Errorf("update bkci space_name [%s] to [%s] failed, %v", oldName, sp.SpaceName, err)
				continue
			}
			logger.Infof("update bkci space name [%s] to [%s]", oldName, sp.SpaceName)
		}
	}
	return nil
}
