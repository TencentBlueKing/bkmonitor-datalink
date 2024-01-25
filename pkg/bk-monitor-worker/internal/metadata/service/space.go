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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
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
		// 名称变动，需要更新
		if name != oldName {
			sp.SpaceName = name
			err := sp.Update(db, space.SpaceDBSchema.SpaceName)
			if err != nil {
				logger.Errorf("update bkcc space name [%s] to [%s] failed, %v", oldName, sp.SpaceName)
				continue
			}
			logger.Infof("update bkcc space name [%s] to [%s]", oldName, sp.SpaceName)
		}
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
	spaceIdSet := mapset.NewSet(mapx.GetMapKeys(bizIdNameMap)...)

	// 过滤已经创建空间的业务
	db := mysql.GetDBSession().DB
	var spaceList []space.Space
	if err := space.NewSpaceQuerySet(db).Select(space.SpaceDBSchema.SpaceId).SpaceTypeIdEq(models.SpaceTypeBKCC).All(&spaceList); err != nil {
		return errors.Wrap(err, "query bkcc space failed")
	}
	existSpaceIdSet := mapset.NewSet[string]()
	for _, sp := range spaceList {
		existSpaceIdSet.Add(sp.SpaceId)
	}

	diffSet := spaceIdSet.Difference(existSpaceIdSet)
	diffDelete := existSpaceIdSet.Difference(spaceIdSet)
	if diffSet.Cardinality() == 0 && diffDelete.Cardinality() == 0 {
		logger.Infof("bkcc space need not add or delete")
		return nil
	}
	// 针对删除的业务
	if diffDelete.Cardinality() != 0 && allowDelete {
		deleteSpaceIds := diffDelete.ToSlice()
		// 删除和数据源的关联
		count := float64(len(deleteSpaceIds))
		if cfg.BypassSuffixPath != "" {
			_ = metrics.MysqlCount(space.SpaceDataSource{}.TableName(), "RefreshBkccSpace_delete_SpaceDataSource", count)
			logger.Infof("[db_diff] delete SpaceDataSource with space_type_id [bkcc] space_id [%v]", deleteSpaceIds)
			_ = metrics.MysqlCount(space.SpaceResource{}.TableName(), "RefreshBkccSpace_delete_SpaceResource", count)
			logger.Infof("[db_diff] delete SpaceResource with space_type_id [bkcc] resource_id [%v]", deleteSpaceIds)
			_ = metrics.MysqlCount(space.Space{}.TableName(), "RefreshBkccSpace_delete_Space", count)
			logger.Infof("[db_diff] delete Space with space_type_id [bkcc] resource_id [%v]", deleteSpaceIds)
		} else {
			// 删除和数据源的关联
			_ = metrics.MysqlCount(space.SpaceDataSource{}.TableName(), "RefreshBkccSpace_delete_SpaceDataSource", count)
			if err := space.NewSpaceDataSourceQuerySet(db).SpaceTypeIdEq(models.SpaceTypeBKCC).SpaceIdIn(deleteSpaceIds...).Delete(); err != nil {
				return errors.Wrapf(err, "delete SpaceDataSource with space_type_id [bkcc] space_id [%v] failed", deleteSpaceIds)
			}
			// 标识关联空间不可用，这里主要是针对 bcs 资源
			var srList []space.SpaceResource
			var needUpdateSpaceIds []string
			srQs := space.NewSpaceResourceQuerySet(db).ResourceTypeEq(models.SpaceTypeBKCC).ResourceIdIn(deleteSpaceIds...)
			if err := srQs.All(&srList); err != nil {
				return errors.Wrapf(err, "query SpaceResource with resource_type [bkcc] resource_id [%v] failed", deleteSpaceIds)
			}
			for _, sr := range srList {
				needUpdateSpaceIds = append(needUpdateSpaceIds, sr.SpaceId)
			}
			// 删除关联资源
			_ = metrics.MysqlCount(space.SpaceResource{}.TableName(), "RefreshBkccSpace_delete_SpaceResource", count)
			if err := srQs.Delete(); err != nil {
				return errors.Wrapf(err, "delete SpaceResource with resource_type [bkcc] resource_id [%v] failed", deleteSpaceIds)
			}
			if len(needUpdateSpaceIds) != 0 {
				// 对应的 BKCI(BCS) 空间，标识为不可用
				_ = metrics.MysqlCount(space.Space{}.TableName(), "RefreshBkccSpace_update_Space_IsBcsValid", float64(len(needUpdateSpaceIds)))
				if err := space.NewSpaceQuerySet(db).SpaceTypeIdEq(models.SpaceTypeBKCI).SpaceIdIn(needUpdateSpaceIds...).GetUpdater().SetIsBcsValid(false).Update(); err != nil {
					return errors.Wrapf(err, "update space is_bcs_valid to [false] for space_type_id [bkci] space_id [%v] failed", needUpdateSpaceIds)
				}
			}
			// 删除对应的 BKCC 空间
			_ = metrics.MysqlCount(space.Space{}.TableName(), "RefreshBkccSpace_delete_Space", count)
			if err := space.NewSpaceQuerySet(db).SpaceTypeIdEq(models.SpaceTypeBKCC).SpaceIdIn(deleteSpaceIds...).Delete(); err != nil {
				return errors.Wrapf(err, "delete Space with space_type_id [bkcc] space_id [%v] failed", deleteSpaceIds)
			}
		}
	}

	// 针对添加的业务
	if diffSet.Cardinality() == 0 {
		return nil
	}
	var createdSpaces []interface{}
	for _, bizId := range diffSet.ToSlice() {
		bizName := bizIdNameMap[bizId]
		sp := space.Space{
			SpaceTypeId: models.SpaceTypeBKCC,
			SpaceId:     bizId,
			SpaceName:   bizName,
		}
		_ = metrics.MysqlCount(space.Space{}.TableName(), "RefreshBkccSpace_create_Space", 1)
		if cfg.BypassSuffixPath != "" {
			logger.Infof("[db_diff] create Space with space_type_id [%s] space_id [%s] space_name [%s]", models.SpaceTypeBKCC, bizId, bizName)
		} else {
			if err := sp.Create(db); err != nil {
				logger.Errorf("create Space with space_type_id [%s] space_id [%s] space_name [%s] failed, %v", models.SpaceTypeBKCC, bizId, bizName, err)
				continue
			}
		}
		createdSpaces = append(createdSpaces, fmt.Sprintf("%s__%s", models.SpaceTypeBKCC, bizId))
	}
	if len(createdSpaces) != 0 {
		// 追加业务空间到 vm 查询的白名单中, 并通知到 unifyquery
		rds := redis.GetInstance()
		if err := rds.SAdd(models.QueryVmSpaceUidListKey, createdSpaces...); err != nil {
			logger.Errorf("reids SAdd [%v] to channel [%s] failed, %v", createdSpaces, models.QueryVmSpaceUidListKey, err)
		}
		msg, err := jsonx.MarshalString(createdSpaces)
		if err != nil {
			return errors.Wrapf(err, "marshal space_id list [%v] failed", createdSpaces)
		}
		if err := rds.Publish(models.QueryVmSpaceUidChannelKey, msg); err != nil {
			return errors.Wrapf(err, "publish [%v] to [%v] failed", msg, models.QueryVmSpaceUidChannelKey)
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
		_ = metrics.MysqlCount(space.Space{}.TableName(), "SyncBcsSpace_update", 1)
		if cfg.BypassSuffixPath != "" {
			logger.Infof("[db_diff] update Space space_type [%s] space_id [%s] with space_code [%s] space_name [%s] is_bcs_valid [%v]", models.SpaceTypeBKCI, projectId, spaceCode, spaceName, true)
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
	_ = metrics.MysqlCount(sp.TableName(), "CreateBcsSpace_create_bkci", 1)
	if err := sp.Create(tx); err != nil {
		tx.Rollback()
		return errors.Wrapf(err, "create Space with space_type_id [%s] space_id [%s] space_name [%s] space_code [%s] is_bcs_valid [%v] fialed", models.SpaceTypeBKCI, projectCode, name, projectId, true)
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
	_ = metrics.MysqlCount(srForBkcc.TableName(), "CreateBcsSpace_create_bkcc", 1)
	if err := srForBkcc.Create(tx); err != nil {
		tx.Rollback()
		return errors.Wrapf(err, "create SpaceResource with space_type_id [%s] space_id [%s] resource_type [%s] resource_id [%v] failed", srForBkcc.SpaceTypeId, srForBkcc.SpaceId, srForBkcc.ResourceType, srForBkcc.ResourceId)
	}

	// 组装空间对应的资源
	// 获取存储在metadata中的集群数据
	var metadataClusterId []string
	var bcsClusterInfoList []bcs.BCSClusterInfo
	if err := bcs.NewBCSClusterInfoQuerySet(tx).Select(bcs.BCSClusterInfoDBSchema.ClusterID).StatusEq(models.BcsClusterStatusRunning).All(&bcsClusterInfoList); err != nil {
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
		if err := bcs.NewBCSClusterInfoQuerySet(tx).StatusEq(models.BcsClusterStatusRunning).ClusterIDEq(clusterId).One(&cluster); err != nil {
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
	_ = metrics.MysqlCount(srForBcs.TableName(), "CreateBcsSpace_create_bcs", 1)
	if err := srForBcs.Create(tx); err != nil {
		tx.Rollback()
		return errors.Wrapf(err, "create SpaceResource with space_type_id [%s] space_id [%s] resource_type [%s] resource_id [%v] failed", srForBcs.SpaceTypeId, srForBcs.SpaceId, srForBcs.ResourceType, srForBcs.ResourceId)
	}

	spaceDataSourceList, err := s.composeBcsSpaceDataSource(models.SpaceTypeBKCI, projectCode, dataIdList, projectClusterDataIdList, sharedClusterDataIdList)
	if err != nil {
		tx.Rollback()
		return errors.Wrapf(err, "composeBcsSpaceDataSource with space_type_id [%s] space_id [%s] data_id_list [%v] project_cluster_data_id_list [%v] shared_cluster_data_id_list [%v] failed", models.SpaceTypeBKCI, projectCode, dataIdList, projectClusterDataIdList, sharedClusterDataIdList)
	}
	for _, sds := range spaceDataSourceList {
		logger.Infof("[db_diff]create SpaceDataSource with space_type_id [%s] space_id [%s] bk_data_id [%v] from_authorization [%v]", sds.SpaceTypeId, sds.SpaceId, sds.BkDataId, sds.FromAuthorization)
		_ = metrics.MysqlCount(sds.TableName(), "CreateBcsSpace_create_sds", 1)
		if err := sds.Create(tx); err != nil {
			tx.Rollback()
			return errors.Wrapf(err, "create SpaceDataSource with space_type_id [%s] space_id [%s] bk_data_id [%v] from_authorization [%v] failed", sds.SpaceTypeId, sds.SpaceId, sds.BkDataId, sds.FromAuthorization)
		}
	}
	if cfg.BypassSuffixPath != "" {
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
			_ = metrics.MysqlCount(space.SpaceResource{}.TableName(), "RefreshBcsProjectBiz_create", 1)
			if cfg.BypassSuffixPath != "" {
				logger.Infof("[db_diff] create SpaceResource with space_type_id [%s] space_id [%s] resource_type [%s] resource_id [%v] dimension_values [%s]", sr.SpaceTypeId, sr.SpaceId, sr.ResourceType, sr.ResourceId, sr.DimensionValues)
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
		_ = metrics.MysqlCount(space.SpaceResource{}.TableName(), "RefreshBcsProjectBiz_update", 1)
		if cfg.BypassSuffixPath != "" {
			logger.Infof("[db_diff] update SpaceResource id [%v] with dimension_values [%v] resource_id [%v]", res.Id, dm, res.ResourceId)
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
