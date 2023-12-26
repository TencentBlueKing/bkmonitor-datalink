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
	"math/rand"
	"strconv"
	"strings"

	"github.com/jinzhu/gorm"
	"github.com/pkg/errors"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/bkdata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/bcs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/optionx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/slicex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// BkdataNSTimestampDataIdList ns 时间戳结果表列表
var BkdataNSTimestampDataIdList = []uint{1100006, 1100007, 1100015, 1100016}

// SecondEtlConfig Second级别etl config
var SecondEtlConfig = []string{models.ETLConfigTypeBkExporter, models.ETLConfigTypeBkStandard}

// VmUtils vm utils
type VmUtils struct{}

func NewVmUtils() VmUtils {
	return VmUtils{}
}

// AccessBkdata 根据类型接入计算平台
func (s VmUtils) AccessBkdata(bkBizId int, tableId string, bkDataId uint) error {
	// 仅针对接入 influxdb 类型
	// 查询空间信息
	db := mysql.GetDBSession().DB
	var spaceTypeId, spaceId string
	if bkBizId > 0 {
		spaceTypeId = "bkcc"
		spaceId = strconv.Itoa(bkBizId)
	} else {
		var sp space.Space
		if err := space.NewSpaceQuerySet(db).IdEq(-bkBizId).One(&sp); err != nil {
			// 0 业务没有空间信息，不需要查询或者创建空间及空间关联的 vm
			if !gorm.IsRecordNotFoundError(err) {
				return err
			}
		}
		spaceTypeId = sp.SpaceTypeId
		spaceId = sp.SpaceId
	}

	var spaceVmInfo space.SpaceVmInfo
	if spaceTypeId != "" && spaceId != "" {
		// 如果不在空间接入 vm 的记录中，则创建记录
		if err := space.NewSpaceVmInfoQuerySet(db).SpaceTypeEq(spaceTypeId).SpaceIDEq(spaceId).One(&spaceVmInfo); err != nil {
			if !gorm.IsRecordNotFoundError(err) {
				return err
			}
		}
		if spaceVmInfo.ID == 0 {
			var cluster storage.ClusterInfo
			if err := storage.NewClusterInfoQuerySet(db).ClusterTypeEq(models.StorageTypeVM).IsDefaultClusterEq(true).One(&cluster); err != nil {
				return errors.Wrapf(err, "cluster type [%s] not found default cluster", models.StorageTypeVM)
			}
			spaceVmInfo = space.SpaceVmInfo{
				SpaceType:       spaceTypeId,
				SpaceID:         spaceId,
				VMClusterID:     cluster.ClusterID,
				VMRetentionTime: models.VmRetentionTime,
			}
			if err := spaceVmInfo.Create(db); err != nil {
				return err
			}
		}
	}
	// 检查是否已经写入 kafka storage
	var record storage.AccessVMRecord
	if err := storage.NewAccessVMRecordQuerySet(db).ResultTableIdEq(tableId).One(&record); err == nil {
		// 如果已经存在，认为已经接入 vm，则直接返回
		return nil
	} else if !gorm.IsRecordNotFoundError(err) {
		return err
	}
	// 进行接入操作
	// 获取数据源类型、集群等信息
	dataTypeClusterMap, err := s.getDataTypeCluster(bkDataId)
	if err != nil {
		return err
	}
	dataType := dataTypeClusterMap["data_type"]
	bcsClusterId := dataTypeClusterMap["bcs_cluster_id"]
	// 获取 vm 集群名称
	vmCluster, err := s.getVmCluster(spaceTypeId, spaceId, spaceVmInfo.VMClusterID)
	if err != nil {
		return err
	}
	vmClusterName := vmCluster.ClusterName

	// 调用接口接入数据平台
	dataName, topicName := s.getBkbaseDataNameAndTopic(tableId)
	if err != nil {
		return err
	}
	timestampLen, err := s.getTimestampLen(bkDataId, "")
	if err != nil {
		return err
	}
	vmDataMap, err := s.AccessVmByKafka(tableId, dataName, vmClusterName, timestampLen)
	if err != nil {
		return fmt.Errorf("access vm error, %v", err)
	}
	// 如果接入返回为空，则直接返回
	resp := optionx.NewOptions(vmDataMap)
	// 创建 KafkaStorage 和 AccessVMRecord 记录
	clusterId, ok := resp.GetUint("cluster_id")
	if !ok {
		return fmt.Errorf("vm data can not get cluster_id")
	}
	vmDataId, ok := resp.GetUint("bk_data_id")
	if !ok {
		return fmt.Errorf("vm data can not get bk_data_id")
	}
	cleanRtId, ok := resp.GetString("clean_rt_id")
	if !ok {
		return fmt.Errorf("vm data can not get clean_rt_id")
	}
	kafkaStorageExist, ok := resp.GetBool("kafka_storage_exist")
	if ok && !kafkaStorageExist {
		if err := NewKafkaStorageSvc(nil).CreateTable(
			tableId,
			true,
			optionx.NewOptions(map[string]interface{}{
				"storageClusterId": clusterId,
				"topicName":        topicName,
				"useDefaultFormat": false,
			})); err != nil {
			logger.Errorf("create KafkaStorage error for access vm: %v", err)
		}
	}

	avmr := storage.AccessVMRecord{
		DataType:         dataType,
		ResultTableId:    tableId,
		BcsClusterId:     bcsClusterId,
		StorageClusterID: clusterId,
		VmClusterId:      vmCluster.ClusterID,
		BkBaseDataId:     vmDataId,
		VmResultTableId:  cleanRtId,
	}
	if err := avmr.Create(db); err != nil {
		logger.Errorf("create AccessVMRecord error for access vm: %v", err)
	}
	return nil
}

func (s VmUtils) getDataTypeCluster(dataId uint) (map[string]string, error) {
	var bcsCluster bcs.BCSClusterInfo
	if err := mysql.GetDBSession().DB.First(&bcsCluster, "K8sMetricDataID = ? or CustomMetricDataID = ?", dataId, dataId).Error; err != nil {
		if !gorm.IsRecordNotFoundError(err) {
			return nil, err
		}
	}
	var dataType, bcsClusterId string
	if bcsCluster.ClusterID == "" {
		dataType = models.VmDataTypeUserCustom
	} else {
		if bcsCluster.K8sMetricDataID == dataId {
			dataType = models.VmDataTypeBcsClusterK8s
			bcsClusterId = bcsCluster.ClusterID
		} else {
			dataType = models.VmDataTypeBcsClusterCustom
			bcsClusterId = bcsCluster.ClusterID
		}
	}

	return map[string]string{"data_type": dataType, "bcs_cluster_id": bcsClusterId}, nil
}

// 获取 vm 集群
func (s VmUtils) getVmCluster(spaceTypeId, spaceId string, vmClusterId uint) (*storage.ClusterInfo, error) {
	db := mysql.GetDBSession().DB
	// 如果 vm 集群ID存在，直接查询到对应的集群
	if vmClusterId != 0 {
		var cluster storage.ClusterInfo
		if err := storage.NewClusterInfoQuerySet(db).ClusterTypeEq(models.StorageTypeVM).ClusterIDEq(vmClusterId).One(&cluster); err != nil {
			return nil, errors.Wrapf(err, "query vm cluster [%v] failed", vmClusterId)
		}
		return &cluster, nil
	}

	// 如果 vm 集群ID不存在，查询空间是否已经接入过，如果已经接入过，则可以直接获取
	if spaceTypeId != "" && spaceId != "" {
		var spaceVmInfo space.SpaceVmInfo
		if err := space.NewSpaceVmInfoQuerySet(db).SpaceTypeEq(spaceTypeId).SpaceIDEq(spaceId).One(&spaceVmInfo); err != nil {
			if !gorm.IsRecordNotFoundError(err) {
				return nil, err
			}
		}
		if spaceVmInfo.ID == 0 {
			logger.Warnf("space_type [%s] space_id [%s] not access vm", spaceTypeId, spaceId)
		} else {
			var cluster storage.ClusterInfo
			if err := storage.NewClusterInfoQuerySet(db).ClusterIDEq(spaceVmInfo.VMClusterID).One(&cluster); err != nil {
				return nil, errors.Wrapf(err, "space_type [%s] space_id [%s] not found vm cluster", spaceTypeId, spaceId)
			}
			return &cluster, nil
		}
	}

	// 没有接入过，获取默认 VM 集群
	var cluster storage.ClusterInfo
	if err := storage.NewClusterInfoQuerySet(db).ClusterTypeEq(models.StorageTypeVM).IsDefaultClusterEq(true).One(&cluster); err != nil {
		return nil, errors.Wrap(err, "query default vm cluster failed")
	}
	return &cluster, nil
}

// 获取 bkbase 的结果表名称 data_name 和 topic_name
func (s VmUtils) getBkbaseDataNameAndTopic(tableId string) (string, string) {
	// 如果以 '__default__'结尾，则取前半部分
	if strings.HasSuffix(tableId, ".__default__") {
		tableId = strings.Split(tableId, ".__default__")[0]
	}
	dataName := strings.ReplaceAll(tableId, "-", "_")
	dataName = strings.ReplaceAll(dataName, ".", "_")
	dataName = strings.ReplaceAll(dataName, "__", "_")
	start := len(dataName) - 40
	if start < 0 {
		start = 0
	}
	dataName = dataName[start:]
	// 清洗结果表不能出现双下划线
	dataName = fmt.Sprintf("vm_%s", dataName)
	dataName = strings.ReplaceAll(dataName, "__", "_")
	topicName := fmt.Sprintf("%s%v", dataName, cfg.GlobalDefaultBkdataBizId)
	return dataName, topicName
}

// 通过 data id 或者 etl config 获取接入 vm 是清洗时间的长度
func (s VmUtils) getTimestampLen(dataId uint, etcConfig string) (int, error) {
	// 如果都不存在，则默认
	if dataId == 0 && etcConfig == "" {
		return models.TimeStampLenMillisecondLen, nil
	}
	if dataId != 0 {
		for _, id := range BkdataNSTimestampDataIdList {
			if dataId == id {
				return models.TimeStampLenNanosecondLen, nil
			}
		}
		var ds resulttable.DataSource
		if err := resulttable.NewDataSourceQuerySet(mysql.GetDBSession().DB).BkDataIdEq(dataId).One(&ds); err == nil {
			// 以实际传入的值为准
			if etcConfig == "" {
				etcConfig = ds.EtlConfig
			}
		} else if !gorm.IsRecordNotFoundError(err) {
			return 0, err
		}
	}
	if etcConfig != "" {
		for _, ec := range SecondEtlConfig {
			if ec == etcConfig {
				return models.TimeStampLenSecondLen, nil
			}
		}
	}
	return models.TimeStampLenMillisecondLen, nil
}

// AccessVmByKafka 通过 kafka 配置接入 vm
func (s VmUtils) AccessVmByKafka(tableId, rawDataName, vmClusterName string, timestampLen int) (map[string]interface{}, error) {
	db := mysql.GetDBSession().DB
	kafkaStorageExist := true
	var kafkaStorage storage.KafkaStorage
	if err := storage.NewKafkaStorageQuerySet(db).TableIDEq(tableId).One(&kafkaStorage); err != nil {
		if gorm.IsRecordNotFoundError(err) {
			logger.Infof("kafka storage for table_id [%s] not found", tableId)
			kafkaStorageExist = false
		} else {
			return nil, err
		}
	}
	// 不存在则直接创建
	if !kafkaStorageExist {
		storageClusterId, _, err := s.refineBkdataKafkaInfo()
		if err != nil {
			return nil, errors.Wrap(err, "refineBkdataKafkaInfo error")
		}
		bkDataId, cleanRtId, err := s.accessVm(rawDataName, vmClusterName, models.VmRetentionTime, timestampLen)
		if err != nil {
			return nil, errors.Wrap(err, "accessVm error")
		}
		return map[string]interface{}{"cluster_id": storageClusterId, "bk_data_id": bkDataId, "clean_rt_id": cleanRtId}, nil
	}
	// 创建清洗和入库 vm
	var bkBaseData storage.BkDataStorage
	if err := storage.NewBkDataStorageQuerySet(db).TableIDEq(tableId).One(&bkBaseData); err != nil {
		if !gorm.IsRecordNotFoundError(err) {
			return nil, err
		}
		bkBaseData.TableID = tableId
		if err := bkBaseData.Create(db); err != nil {
			return nil, err
		}
	}
	if bkBaseData.RawDataID == -1 {
		var rt resulttable.ResultTable
		if err := resulttable.NewResultTableQuerySet(db).TableIdEq(tableId).One(&rt); err != nil {
			return nil, err
		}
		if err := NewBkDataStorageSvc(&bkBaseData).CreateDatabusClean(&rt); err != nil {
			return nil, err
		}
	}
	// 重新读取一遍数据
	if err := storage.NewBkDataStorageQuerySet(db).TableIDEq(tableId).One(&bkBaseData); err != nil {
		return nil, err
	}
	rawDataName, _ = s.getBkbaseDataNameAndTopic(tableId)
	cleanData, err := NewBkDataAccessor(rawDataName, rawDataName, vmClusterName, "", "", 0, timestampLen).Clean()
	if err != nil {
		return nil, err
	}
	cleanData["bk_app_code"] = cfg.BkApiAppCode
	cleanData["bk_username"] = "admin"
	cleanData["bk_biz_id"] = cfg.GlobalDefaultBkdataBizId
	cleanData["raw_data_id"] = bkBaseData.RawDataID
	cleanData["clean_config_name"] = rawDataName
	cleanData["kafka_storage_exist"] = kafkaStorageExist
	jsonConfig, err := jsonx.MarshalString(cleanData["json_config"])
	if err != nil {
		return nil, err
	}
	cleanData["json_config"] = jsonConfig

	bkdataApi, err := api.GetBkdataApi()
	if err != nil {
		return nil, err
	}
	var resp define.APICommonMapResp
	if _, err := bkdataApi.DataBusCleans().SetBody(cleanData).SetResult(resp).Request(); err != nil {
		return nil, errors.Wrapf(err, "create data clean error, params [%#v]", cleanData)
	}
	bkbaseResultTableId := resp.Data["result_table_id"]
	// 启动
	if _, err := bkdataApi.StartDatabusCleans().SetBody(map[string]interface{}{
		"bk_app_code":     cfg.BkApiAppCode,
		"bk_username":     "admin",
		"result_table_id": bkbaseResultTableId,
		"storages":        []string{"kafka"},
	}).Request(); err != nil {
		return nil, errors.Wrapf(err, "create data clean error, bkbaseResultTableId [%v]", bkbaseResultTableId)
	}
	// 接入 vm
	storageParams, err := NewBkDataStorageWithDataID(bkBaseData.RawDataID, rawDataName, vmClusterName, "").Value()
	storageParams["bk_app_code"] = cfg.BkApiAppCode
	storageParams["bk_username"] = "admin"
	if err != nil {
		return nil, err
	}
	if _, err := bkdataApi.CreateDataStorages().SetBody(storageParams).Request(); err != nil {
		return nil, errors.Wrapf(err, "create data storages error, storageParams [%#v]", storageParams)
	}
	if bkBaseData.RawDataID <= 0 {
		return nil, fmt.Errorf("table_id [%s] BkDataStorage raw_data_id is still -1", tableId)
	}
	return map[string]interface{}{
		"clean_rt_id":         fmt.Sprintf("%v_%s", cfg.GlobalDefaultBkdataBizId, rawDataName),
		"bk_data_id":          uint(bkBaseData.RawDataID),
		"cluster_id":          kafkaStorage.StorageClusterID,
		"kafka_storage_exist": kafkaStorageExist,
	}, nil
}

// 获取接入计算平台时，使用的 kafka 信息
func (s VmUtils) refineBkdataKafkaInfo() (uint, string, error) {
	var kafkaClusterList []storage.ClusterInfo
	if err := storage.NewClusterInfoQuerySet(mysql.GetDBSession().DB).ClusterTypeEq(models.StorageTypeKafka).All(&kafkaClusterList); err != nil {
		return 0, "", err
	}
	kafkaDomainCLusterIdMap := make(map[string]uint)
	var kafkaDomainList []string
	for _, k := range kafkaClusterList {
		kafkaDomainCLusterIdMap[k.DomainName] = k.ClusterID
		kafkaDomainList = append(kafkaDomainList, k.DomainName)
	}
	// 通过集群平台获取可用的 kafka host
	bkdataApi, err := api.GetBkdataApi()
	if err != nil {
		return 0, "", err
	}
	var resp define.APICommonResp
	if _, err := bkdataApi.GetKafkaInfo().SetQueryParams(map[string]string{
		"bk_app_code": cfg.BkApiAppCode,
		"bk_username": "admin",
		"tag":         "bkmonitor_outer",
	}).SetResult(&resp).Request(); err != nil {
		return 0, "", err
	}
	bkdataKafkaDataList, ok := resp.Data.([]interface{})
	if !ok {
		return 0, "", fmt.Errorf("parse get_kafka_info response error, %#v", resp.Data)
	}
	if len(bkdataKafkaDataList) == 0 {
		return 0, "", errors.New("bkdata kafka data not found")
	}
	bkdataKafkaDataInterface := bkdataKafkaDataList[0]
	bkdataKafkaData, ok := bkdataKafkaDataInterface.(map[string]interface{})
	if !ok {
		return 0, "", fmt.Errorf("parse bkdata kafka data error, %#v", bkdataKafkaData)
	}
	kafkaData := optionx.NewOptions(bkdataKafkaData)
	bkdataKafkaHostStr, _ := kafkaData.GetString("ip_list")
	bkdataKafkaHostList := strings.Split(bkdataKafkaHostStr, ",")

	// 获取 metadata 和接口返回的交集，然后任取其中一个; 如果不存在，则直接报错
	existedHostList := slicex.StringSet2List(slicex.StringList2Set(kafkaDomainList).Intersect(slicex.StringList2Set(bkdataKafkaHostList)))
	if len(existedHostList) == 0 {
		return 0, "", fmt.Errorf("bkdata kafka host not registerd ClusterInfo, bkdata resp: %#v", resp.Data)
	}
	host := existedHostList[rand.Intn(len(existedHostList))]
	clusterId := kafkaDomainCLusterIdMap[host]
	logger.Infof("refine exist kafka, cluster_id [%v], host [%s]", clusterId, host)
	return clusterId, host, nil
}

// 接入 vm 流程
func (s VmUtils) accessVm(rawDataName, vmCluster, vmRetentionTime string, timestampLen int) (uint, string, error) {
	// 接入计算平台
	if vmRetentionTime == "" {
		vmRetentionTime = models.VmRetentionTime
	}
	accessor := NewBkDataAccessor(rawDataName, rawDataName, vmCluster, vmRetentionTime, "接入计算平台 vm", 0, timestampLen)
	data, err := accessor.create()
	if err != nil {
		return 0, "", err
	}
	// 解析返回，获取计算平台 dataid
	if len(data.CleanRtId) == 0 {
		return 0, "", fmt.Errorf("accessing bkdata data [%v] error, %v", data, err)
	}
	bkDataId := data.RawDataId
	cleanRtId := data.CleanRtId[0]
	return bkDataId, cleanRtId, nil
}

func NewBkDataAccessor(bkTableId, dataHubName, vmCluster, vmRetentionTime, desc string, bkBizId, timestampLen int) *BkDataAccessor {
	if vmRetentionTime == "" {
		vmRetentionTime = models.VmRetentionTime
	}
	if bkBizId == 0 {
		bkBizId = cfg.GlobalDefaultBkdataBizId
	}
	return &BkDataAccessor{
		BkTableId:       bkTableId,
		DataHubName:     dataHubName,
		BkBizId:         bkBizId,
		VmCluster:       vmCluster,
		VmRetentionTime: vmRetentionTime,
		Desc:            desc,
		TimestampLen:    timestampLen,
	}
}

type BkDataAccessor struct {
	BkTableId       string
	DataHubName     string
	BkBizId         int
	VmCluster       string
	VmRetentionTime string
	Desc            string
	TimestampLen    int
}

// 接入计算平台
func (a BkDataAccessor) create() (*bkdata.CreateDataHubData, error) {
	bkdataApi, err := api.GetBkdataApi()
	if err != nil {
		return nil, err
	}
	clean, err := a.Clean()
	if err != nil {
		return nil, err
	}
	s, err := a.Storage()
	if err != nil {
		return nil, err
	}

	var resp bkdata.CreateDataHubResp
	if _, err := bkdataApi.CreateDataHub().SetBody(map[string]interface{}{
		"bk_app_code": cfg.BkApiAppCode,
		"bk_username": "admin",
		"common": map[string]interface{}{
			"bk_biz_id":     a.BkBizId,
			"data_scenario": "custom",
		},
		"raw_data": map[string]interface{}{
			"raw_data_name":    a.DataHubName,
			"raw_data_alias":   a.DataHubName,
			"data_source_tags": []string{"server"},
			"description":      a.Desc,
			"data_scenario":    map[string]interface{}{},
		},
		"clean":   []interface{}{clean},
		"storage": s,
	}).SetResult(&resp).Request(); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

// Clean 清洗配置
func (a BkDataAccessor) Clean() (map[string]interface{}, error) {
	return NewBkDataClean(a.BkTableId, a.BkTableId, a.BkBizId, a.TimestampLen).Value()
}

// Storage 存储配置
func (a BkDataAccessor) Storage() ([]map[string]interface{}, error) {
	return NewBkDataStorage(a.BkTableId, a.VmCluster, a.VmRetentionTime).Value()
}

func NewBkDataClean(rawDataName, resultTableName string, bkBizId, timestampLen int) *BkDataClean {
	if timestampLen == 0 {
		timestampLen = models.TimeStampLenMillisecondLen
	}
	return &BkDataClean{
		RawDataName:     rawDataName,
		ResultTableName: resultTableName,
		BkBizId:         bkBizId,
		TimestampLen:    timestampLen,
	}
}

type BkDataClean struct {
	RawDataName     string
	ResultTableName string
	BkBizId         int
	TimestampLen    int
}

func (b BkDataClean) Value() (map[string]interface{}, error) {
	fields := []map[string]interface{}{
		{
			"field_name":   "time",
			"field_type":   "long",
			"field_alias":  "time",
			"is_dimension": false,
			"field_index":  1,
		},
		{
			"field_name":   "value",
			"field_type":   "double",
			"field_alias":  "value",
			"is_dimension": false,
			"field_index":  2,
		},
		{
			"field_name":   "metric",
			"field_type":   "string",
			"field_alias":  "metric",
			"is_dimension": false,
			"field_index":  3,
		},
		{
			"field_name":   "dimensions",
			"field_type":   "text",
			"field_alias":  "dimensions",
			"is_dimension": false,
			"field_index":  4,
		},
	}
	fieldsStr, err := jsonx.MarshalString(fields)
	if err != nil {
		return nil, err
	}

	params := map[string]string{
		"result_table_name":       b.RawDataName,
		"result_table_name_alias": b.ResultTableName,
		"fields":                  fieldsStr,
		"time_format":             models.TimeStampLenValeMap[b.TimestampLen],
		"timestamp_len":           strconv.Itoa(b.TimestampLen),
		"bk_biz_id":               strconv.Itoa(b.BkBizId),
	}
	configStr := b.Render(params)
	var config map[string]interface{}
	if err := jsonx.UnmarshalString(configStr, &config); err != nil {
		return nil, err
	}
	return config, nil
}

func (b BkDataClean) Render(params map[string]string) string {
	temp := `{
        "json_config": {
            "extract": {
                "type": "fun",
                "method": "from_json",
                "result": "json",
                "label": "label3d0181",
                "args": [],
                "next": {
                    "type": "branch",
                    "name": "",
                    "label": null,
                    "next": [
                        {
                            "type": "assign",
                            "subtype": "assign_json",
                            "label": "label86a168",
                            "assign": [
                                {
                                    "type": "text",
                                    "assign_to": "dimensions",
                                    "key": "dimensions"
                                }
                            ],
                            "next": null
                        },
                        {
                            "type": "access",
                            "subtype": "access_obj",
                            "label": "labelb7a4b1",
                            "key": "metrics",
                            "result": "metrics",
                            "default_type": "null",
                            "default_value": "",
                            "next": {
                                "type": "fun",
                                "label": "label1dd4f4",
                                "result": "item",
                                "args": [],
                                "method": "items",
                                "next": {
                                    "type": "assign",
                                    "subtype": "assign_obj",
                                    "label": "labelec6235",
                                    "assign": [
                                        {
                                            "type": "double",
                                            "assign_to": "value",
                                            "key": "value"
                                        },
                                        {
                                            "type": "string",
                                            "assign_to": "metric",
                                            "key": "key"
                                        }
                                    ],
                                    "next": null
                                }
                            }
                        },
                        {
                            "type": "assign",
                            "subtype": "assign_obj",
                            "label": "labelc08700",
                            "assign": [
                                {
                                    "type": "long",
                                    "assign_to": "time",
                                    "key": "time"
                                }
                            ],
                            "next": null
                        }
                    ]
                }
            },
            "conf": {
                "time_format": "{{time_format}}",
                "timezone": 8,
                "time_field_name": "time",
                "output_field_name": "timestamp",
                "timestamp_len": {{timestamp_len}},
                "encoding": "UTF-8"
            }
        },
        "result_table_name": "{{result_table_name}}",
        "result_table_name_alias": "{{result_table_name_alias}}",
        "processing_id": "{{bk_biz_id}}_{{result_table_name}}",
        "description": "tsdb",
        "fields": {{fields}}
    }`
	for k, v := range params {
		temp = strings.ReplaceAll(temp, fmt.Sprintf("{{%s}}", k), v)
	}
	return temp
}

func NewBkDataStorage(bkTableId, vmCluster, expires string) *BkDataStorage {
	if expires == "" {
		expires = models.VmRetentionTime
	}
	return &BkDataStorage{
		BkTableId: bkTableId,
		VmCluster: vmCluster,
		Expires:   expires,
	}
}

type BkDataStorage struct {
	BkTableId string
	VmCluster string
	Expires   string
}

func (b BkDataStorage) Value() ([]map[string]interface{}, error) {
	return []map[string]interface{}{
		{
			"result_table_name": b.BkTableId,
			"storage_type":      "vm",
			"expires":           b.Expires,
			"storage_cluster":   b.VmCluster,
		},
	}, nil
}

func NewBkDataStorageWithDataID(rawDataId int, resultTableName, vmCluster, expires string) *BkDataStorageWithDataID {
	if expires == "" {
		expires = models.VmRetentionTime
	}
	return &BkDataStorageWithDataID{
		RawDataId:       rawDataId,
		ResultTableName: resultTableName,
		VmCluster:       vmCluster,
		Expires:         expires,
		DataType:        "clean",
	}
}

type BkDataStorageWithDataID struct {
	RawDataId       int
	ResultTableName string
	VmCluster       string
	Expires         string
	DataType        string
}

func (b BkDataStorageWithDataID) Value() (map[string]interface{}, error) {
	fields := []map[string]interface{}{
		{
			"field_name":     "time",
			"field_type":     "long",
			"field_alias":    "time",
			"is_dimension":   false,
			"field_index":    1,
			"physical_field": "time",
		},
		{
			"field_name":     "value",
			"field_type":     "double",
			"field_alias":    "value",
			"is_dimension":   false,
			"field_index":    2,
			"physical_field": "value",
		},
		{
			"field_name":     "metric",
			"field_type":     "string",
			"field_alias":    "metric",
			"is_dimension":   false,
			"field_index":    3,
			"physical_field": "metric",
		},
		{
			"field_name":     "dimensions",
			"field_type":     "text",
			"field_alias":    "dimensions",
			"is_dimension":   false,
			"field_index":    4,
			"physical_field": "dimensions",
		},
	}
	return map[string]interface{}{
		"raw_data_id":             b.RawDataId,
		"data_type":               b.DataType,
		"result_table_name":       b.ResultTableName,
		"result_table_name_alias": b.ResultTableName,
		"storage_type":            "vm",
		"storage_cluster":         b.VmCluster,
		"expires":                 b.Expires,
		"fields":                  fields,
		"config":                  map[string]interface{}{"schemaless": true},
	}, nil
}
