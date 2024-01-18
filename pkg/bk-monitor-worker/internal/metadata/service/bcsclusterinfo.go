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
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/pkg/errors"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/bcsclustermanager"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/apiservice"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/bcs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/customreport"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mapx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/optionx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/slicex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var bcsDatasourceRegisterInfo = map[string]*DatasourceRegister{
	models.BcsDataTypeK8sMetric: {
		EtlConfig:         "bk_standard_v2_time_series",
		ReportClassName:   "TimeSeriesGroup",
		DatasourceName:    "K8sMetricDataID",
		IsSpitMeasurement: true,
		IsSystem:          true,
		Usage:             "metric",
	},
	models.BcsDataTypeCustomMetric: {
		EtlConfig:         "bk_standard_v2_time_series",
		ReportClassName:   "TimeSeriesGroup",
		DatasourceName:    "CustomMetricDataID",
		IsSpitMeasurement: true,
		IsSystem:          false,
		Usage:             "metric",
	},
	models.BcsDataTypeK8sEvent: {
		EtlConfig:       "bk_standard_v2_event",
		ReportClassName: "EventGroup",
		DatasourceName:  "K8sEventDataID",
		IsSystem:        true,
		Usage:           "event",
	},
}

// BcsClusterInfoSvc bcs cluster info service
type BcsClusterInfoSvc struct {
	*bcs.BCSClusterInfo
}

// NewBcsClusterInfoSvc new BcsClusterInfoSvc
func NewBcsClusterInfoSvc(obj *bcs.BCSClusterInfo) BcsClusterInfoSvc {
	return BcsClusterInfoSvc{
		BCSClusterInfo: obj,
	}
}

// FetchK8sClusterList 获取k8s集群信息
func (b BcsClusterInfoSvc) FetchK8sClusterList() ([]BcsClusterInfo, error) {
	managerApi, err := api.GetBcsClusterManagerApi()
	if err != nil {
		return nil, err
	}
	var resp bcsclustermanager.FetchClustersResp
	_, err = managerApi.FetchClusters().SetResult(&resp).Request()
	if err != nil {
		return nil, err
	}
	var clusterList []BcsClusterInfo
	for _, clusterMap := range resp.Data {
		cluster := optionx.NewOptions(clusterMap)
		clusterId, ok := cluster.GetString("clusterID")
		if !ok {
			logger.Warnf("get clusterID failed, %#v", clusterMap)
			continue
		}
		businessID, ok := cluster.GetString("businessID")
		if !ok {
			logger.Warnf("get businessID failed, %#v", clusterMap)
			continue
		}
		// 根据灰度配置只同步指定集群ID的集群
		if !b.IsClusterIdInGray(clusterId) {
			continue
		}
		// 忽略重复的集群ID，共享集群有重复的集群ID
		var exist bool
		for _, c := range clusterList {
			if c.ClusterId == clusterId {
				exist = true
				break
			}
		}
		if exist {
			continue
		}
		clusterName, ok := cluster.GetString("clusterName")
		if !ok {
			return nil, errors.New("can not get clusterName")
		}
		projectID, ok := cluster.GetString("projectID")
		if !ok {
			return nil, errors.New("can not get projectID")
		}
		createTime, ok := cluster.GetString("createTime")
		if !ok {
			return nil, errors.New("can not get createTime")
		}
		updateTime, ok := cluster.GetString("updateTime")
		if !ok {
			return nil, errors.New("can not get updateTime")
		}
		status, ok := cluster.GetString("status")
		if !ok {
			return nil, errors.New("can not get status")
		}
		environment, ok := cluster.GetString("environment")
		if !ok {
			return nil, errors.New("can not get environment")
		}

		clusterList = append(clusterList, BcsClusterInfo{
			BkBizId:      businessID,
			ClusterId:    clusterId,
			BcsClusterId: clusterId,
			Id:           clusterId,
			Name:         clusterName,
			ProjectId:    projectID,
			ProjectName:  "",
			CreatedAt:    createTime,
			UpdatedAt:    updateTime,
			Status:       status,
			Environment:  environment,
		})
	}

	return clusterList, nil
}

// IsClusterIdInGray 判断cluster id是否在灰度配置中
func (BcsClusterInfoSvc) IsClusterIdInGray(clusterId string) bool {
	// 未启用灰度配置，全返回true
	if !cfg.BcsEnableBcsGray {
		return true
	}
	grayBcsClusterList := cfg.BcsGrayClusterIdList

	for _, id := range grayBcsClusterList {
		if id == clusterId {
			return true
		}
	}
	return false
}

// UpdateBcsClusterCloudIdConfig 补齐云区域ID
func (b BcsClusterInfoSvc) UpdateBcsClusterCloudIdConfig() error {
	if b.BCSClusterInfo == nil {
		return errors.New("BCSClusterInfo obj can not be nil")
	}
	// 非running状态和已有云区域id则不处理
	if b.Status != models.BcsClusterStatusRunning || b.BkCloudId != nil {
		return nil
	}

	// 从BCS获取集群的节点IP信息
	apiNodes, err := b.FetchK8sNodeListByCluster(b.ClusterID)
	if err != nil {
		return err
	}
	var ipSplits = make([][]string, 0)
	for _, node := range apiNodes {
		if node.NodeIp == "" {
			continue
		}
		splitsSize := len(ipSplits)
		if splitsSize != 0 && len(ipSplits[splitsSize-1]) < 100 {
			ipSplits[splitsSize-1] = append(ipSplits[splitsSize-1], node.NodeIp)
		} else {
			ipSplits = append(ipSplits, []string{node.NodeIp})
		}
	}
	var ipMap = make(map[string]int)
	for _, ips := range ipSplits {
		var params []GetHostByIpParams
		for _, ip := range ips {
			params = append(params, GetHostByIpParams{
				Ip:        ip,
				BkCloudId: -1,
			})
		}
		hostInfo, err := b.getHostByIp(params, b.BkBizId)
		if err != nil {
			return err
		}
		for _, info := range hostInfo {
			if info.Host.BkHostInnerip != "" {
				ip := strings.Split(info.Host.BkHostInnerip, ",")[0]
				ipMap[ip] = info.Host.BkCloudId
			}
			if info.Host.BkHostInneripV6 != "" {
				ip := strings.Split(info.Host.BkHostInneripV6, ",")[0]
				ipMap[ip] = info.Host.BkCloudId
			}
		}
	}

	cloudCount := make(map[int]int)
	for _, node := range apiNodes {
		bkCloudId, ok := ipMap[node.NodeIp]
		if !ok {
			continue
		}
		cloudCount[bkCloudId] = cloudCount[bkCloudId] + 1
	}
	maxCountCloudId := 0
	maxCount := 0
	for cloudId, count := range cloudCount {
		if count > maxCount {
			maxCountCloudId = cloudId
			maxCount = count
		}
	}
	b.BkCloudId = &maxCountCloudId
	return b.Update(mysql.GetDBSession().DB, bcs.BCSClusterInfoDBSchema.BkCloudId)
}

// FetchK8sNodeListByCluster 从BCS获取集群的节点信息
func (b BcsClusterInfoSvc) FetchK8sNodeListByCluster(bcsClusterId string) ([]K8sNodeInfo, error) {
	nodeField := strings.Join([]string{
		"data.metadata.name",
		"data.metadata.resourceVersion",
		"data.metadata.creationTimestamp",
		"data.metadata.labels",
		"data.spec.unschedulable",
		"data.spec.taints",
		"data.status.addresses",
		"data.status.conditions",
	}, ",")
	endpointField := strings.Join([]string{
		"data.metadata.name",
		"data.subsets",
	}, ",")

	nodes, err := b.fetchBcsStorage(bcsClusterId, nodeField, "Node")
	if err != nil {
		return nil, errors.Wrapf(err, "fetch bcs storage Node for %s failed", bcsClusterId)
	}
	endpoints, err := b.fetchBcsStorage(bcsClusterId, endpointField, "Endpoints")
	if err != nil {
		return nil, errors.Wrapf(err, "fetch bcs storage Endpoints for %s failed", bcsClusterId)
	}
	statistics, err := b.getPodCountStatistics(bcsClusterId)
	if err != nil {
		return nil, err
	}

	var result []K8sNodeInfo
	for _, node := range nodes {
		parser := KubernetesNodeJsonParser{node}
		var nodeIp = parser.NodeIp()
		var name = parser.Name()
		result = append(result, K8sNodeInfo{
			BcsClusterId:  bcsClusterId,
			Node:          node,
			Name:          name,
			Taints:        parser.TaintLabels(),
			NodeRoles:     parser.RoleList(),
			NodeIp:        nodeIp,
			Status:        parser.ServiceStatus(),
			NodeName:      name,
			LabelList:     parser.LabelList(),
			Labels:        parser.Labels(),
			EndpointCount: parser.GetEndpointsCount(endpoints),
			PodCount:      statistics[nodeIp],
			CreatedAt:     *parser.CreationTimestamp(),
			Age:           parser.Age().String(), //todo humanize
		})
	}
	return result, nil
}

// 获取bcs storage
func (BcsClusterInfoSvc) fetchBcsStorage(clusterId, field, sourceType string) ([]NodeInfo, error) {
	urlTemplate := "%s/bcsapi/v4/storage/k8s/dynamic/all_resources/clusters/%s/%s?field=%s"
	client := &http.Client{
		Timeout: 5 * time.Second,
	}
	target, err := url.Parse(fmt.Sprintf(urlTemplate, strings.TrimRight(cfg.BkApiBcsApiMicroGwUrl, "/"), clusterId, sourceType, field))
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", cfg.BkApiBcsApiGatewayToken))

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var result FetchBcsStorageResp
	err = jsonx.Unmarshal(body, &result)
	if err != nil {
		return nil, err
	}
	if result.Code != 0 {
		return nil, errors.Errorf("fetch bcs storage failed, %s", result.Message)
	}
	return result.Data, nil
}

// 获取BCSPod统计数据
func (b BcsClusterInfoSvc) getPodCountStatistics(bcsClusterId string) (map[string]int, error) {
	var bcsPodList []bcs.BCSPod
	var result = make(map[string]int)
	if err := bcs.NewBCSPodQuerySet(mysql.GetDBSession().DB).BcsClusterIDEq(bcsClusterId).All(&bcsPodList); err != nil {
		return nil, err
	}
	for _, p := range bcsPodList {
		result[p.NodeIp] = result[p.NodeIp] + 1
	}
	return result, nil
}

type GetHostByIpParams struct {
	Ip        string
	BkCloudId int
}

// 通过IP查询主机信息
func (BcsClusterInfoSvc) getHostByIp(ipList []GetHostByIpParams, BkBizId int) ([]cmdb.ListBizHostsTopoDataInfo, error) {
	cmdbApi, err := api.GetCmdbApi()
	if err != nil {
		return nil, err
	}
	params := processParams(BkBizId, ipList)
	var topoResp cmdb.ListBizHostsTopoResp
	_, err = cmdbApi.ListBizHostsTopo().SetBody(params).SetResult(&topoResp).Request()
	if err != nil {
		return nil, err
	}
	return topoResp.Data.Info, nil
}

// RegisterCluster 注册一个新的bcs集群信息
func (b BcsClusterInfoSvc) RegisterCluster(bkBizId, clusterId, projectId, creator string) (*bcs.BCSClusterInfo, error) {
	bkBizIdInt, err := strconv.ParseInt(bkBizId, 10, 64)
	if err != nil {
		return nil, err
	}
	db := mysql.GetDBSession().DB
	count, err := bcs.NewBCSClusterInfoQuerySet(db).ClusterIDEq(clusterId).Count()
	if err != nil {
		return nil, err
	}
	// 集群已经接入
	if count != 0 {
		return nil, errors.Errorf("failed to register cluster_id [%s] under project_id [%s] for cluster is already register, nothing will do any more", clusterId, projectId)
	}
	bcsUrl, err := url.ParseRequestURI(cfg.BkApiBcsApiMicroGwUrl)
	if err != nil {
		return nil, err
	}
	portStr := bcsUrl.Port()
	port, err := strconv.ParseUint(portStr, 10, 64)
	if err != nil {
		port = 443
	}

	bkEnv := cfg.BcsClusterBkEnvLabel
	cluster := bcs.BCSClusterInfo{
		ClusterID:         clusterId,
		BCSApiClusterId:   clusterId,
		BkBizId:           int(bkBizIdInt),
		ProjectId:         projectId,
		DomainName:        bcsUrl.Hostname(),
		Port:              uint(port),
		ServerAddressPath: "clusters",
		ApiKeyType:        "authorization",
		ApiKeyContent:     cfg.BkApiBcsApiGatewayToken,
		ApiKeyPrefix:      "Bearer",
		Status:            models.BcsClusterStatusRunning,
		IsSkipSslVerify:   true,
		BkEnv:             &bkEnv,
		Creator:           creator,
		LastModifyUser:    creator,
	}
	if err := cluster.Create(db); err != nil {
		return nil, err
	}
	logger.Infof("cluster [%s] create database record success", cluster.ClusterID)
	// 注册6个必要的data_id和自定义事件及自定义时序上报内容
	for usage, register := range bcsDatasourceRegisterInfo {
		// 注册data_id
		datasource, err := NewBcsClusterInfoSvc(&cluster).CreateDataSource(usage, register.EtlConfig, creator, cfg.BcsKafkaStorageClusterId, "default")
		if err != nil {
			return nil, err
		}
		logger.Infof("cluster [%s] usage [%s] is register datasource [%v] success.", cluster.ClusterID, usage, datasource.BkDataId)
		// 注册自定义时序 或 自定义事件
		var defaultStorageConfig map[string]interface{}
		var additionalOptions map[string][]string
		if register.Usage == "metric" {
			// 如果是指标的类型，需要考虑增加influxdb proxy的集群隔离配置
			defaultStorageConfig = map[string]interface{}{"proxy_cluster_name": cfg.BcsInfluxdbDefaultProxyClusterNameForK8s}
			additionalOptions = map[string][]string{models.OptionCustomReportDimensionValues: bcs.DefaultServiceMonitorDimensionTerm}
		} else {
			defaultStorageConfig = map[string]interface{}{"cluster_id": cfg.BcsCustomEventStorageClusterId}
			additionalOptions = map[string][]string{}
		}
		var bkDataId uint
		var customGroupName string
		switch register.ReportClassName {
		case "TimeSeriesGroup":
			group, err := NewTimeSeriesGroupSvc(nil).CreateCustomGroup(
				datasource.BkDataId,
				int(bkBizIdInt),
				fmt.Sprintf("bcs_%s_%s", cluster.ClusterID, usage),
				"other_rt",
				creator,
				register.IsSpitMeasurement,
				defaultStorageConfig,
				additionalOptions,
			)
			if err != nil {
				return nil, err
			}
			bkDataId = group.BkDataID
			customGroupName = group.TimeSeriesGroupName
		case "EventGroup":
			group, err := NewEventGroupSvc(nil).CreateCustomGroup(
				datasource.BkDataId, int(bkBizIdInt),
				fmt.Sprintf("bcs_%s_%s", cluster.ClusterID, usage),
				"other_rt",
				creator,
				register.IsSpitMeasurement,
				defaultStorageConfig,
				additionalOptions,
			)
			if err != nil {
				return nil, err
			}
			bkDataId = group.BkDataID
			customGroupName = group.EventGroupName
		}

		logger.Infof("cluster [%s] register group [%s] for usage [%s] success.", cluster.ClusterID, customGroupName, usage)
		// 记录data_id信息
		switch register.DatasourceName {
		case "K8sMetricDataID":
			cluster.K8sMetricDataID = bkDataId
		case "CustomMetricDataID":
			cluster.CustomMetricDataID = bkDataId
		case "K8sEventDataID":
			cluster.K8sEventDataID = bkDataId
		}
	}
	if err := cluster.Update(db, bcs.BCSClusterInfoDBSchema.K8sMetricDataID, bcs.BCSClusterInfoDBSchema.CustomMetricDataID,
		bcs.BCSClusterInfoDBSchema.K8sEventDataID); err != nil {
		return nil, err
	}
	logger.Infof("cluster [%s] all datasource info save to database success.", cluster.ClusterID)
	return &cluster, nil
}

// CreateDataSource 创建数据源
func (b BcsClusterInfoSvc) CreateDataSource(usage, etlConfig, operator string, mqClusterId uint, transferClusterId string) (*resulttable.DataSource, error) {
	if b.BCSClusterInfo == nil {
		return nil, errors.New("BCSClusterInfo obj can not be nil")
	}

	typeLabelDict := map[string]string{
		"bk_standard_v2_time_series": "time_series",
		"bk_standard_v2_event":       "event",
		"bk_flat_batch":              "log",
	}
	dataSource, err := NewDataSourceSvc(nil).CreateDataSource(
		fmt.Sprintf("bcs_%s_%s", b.ClusterID, usage),
		etlConfig,
		operator,
		"bk_monitor",
		mqClusterId,
		typeLabelDict[etlConfig],
		transferClusterId,
		cfg.BkApiAppCode,
	)
	if err != nil {
		return nil, err
	}
	logger.Infof("data_source [%v] is create by etl_config [%s] for cluster_id [%s]", dataSource.BkDataId, etlConfig, b.ClusterID)
	return dataSource, nil
}

func isIPv6(ip string) bool {
	parsedIp := net.ParseIP(ip)
	if parsedIp != nil && parsedIp.To4() == nil {
		return true
	}
	return false
}

func processParams(bkBizID int, ips []GetHostByIpParams) map[string]interface{} {
	var cloudDict = make(map[int][]string)
	for _, param := range ips {
		if ls, ok := cloudDict[param.BkCloudId]; ok {
			cloudDict[param.BkCloudId] = append(ls, param.Ip)
		} else {
			cloudDict[param.BkCloudId] = []string{param.Ip}
		}
	}
	conditions := []map[string]interface{}{}
	for cloudId, ipList := range cloudDict {
		ipv6IPs := []string{}
		ipv4IPs := []string{}
		for _, ip := range ipList {
			if isIPv6(ip) {
				ipv6IPs = append(ipv6IPs, ip)
			} else {
				ipv4IPs = append(ipv4IPs, ip)
			}
		}
		ipv4Rules := []map[string]interface{}{
			{"field": "bk_host_innerip", "operator": "in", "value": ipv4IPs},
		}

		ipv6Rules := []map[string]interface{}{
			{"field": "bk_host_innerip_v6", "operator": "in", "value": ipv6IPs},
		}

		if cloudId != -1 {
			ipv4Rules = append(ipv4Rules, map[string]interface{}{"field": "bk_cloud_id", "operator": "equal", "value": cloudId})
			ipv6Rules = append(ipv6Rules, map[string]interface{}{"field": "bk_cloud_id", "operator": "equal", "value": cloudId})
		}

		ipv4Condition := map[string]interface{}{
			"condition": "AND",
			"rules":     ipv4Rules,
		}
		ipv6Condition := map[string]interface{}{
			"condition": "AND",
			"rules":     ipv6Rules,
		}

		if len(ipv4IPs) > 0 {
			conditions = append(conditions, ipv4Condition)
		}
		if len(ipv6IPs) > 0 {
			conditions = append(conditions, ipv6Condition)
		}
	}

	var finalCondition interface{}

	if len(conditions) == 1 {
		finalCondition = conditions[0]
	} else {
		finalCondition = map[string]interface{}{
			"condition": "OR",
			"rules":     conditions,
		}
	}

	return map[string]interface{}{
		"bk_biz_id":            bkBizID,
		"host_property_filter": finalCondition,
		"fields": []string{"bk_host_innerip",
			"bk_host_innerip_v6",
			"bk_cloud_id",
			"bk_host_id",
			"bk_biz_id",
			"bk_agent_id",
			"bk_host_outerip",
			"bk_host_outerip_v6",
			"bk_host_name",
			"bk_os_name",
			"bk_os_type",
			"operator",
			"bk_bak_operator",
			"bk_state_name",
			"bk_isp_name",
			"bk_province_name",
			"bk_supplier_account",
			"bk_state",
			"bk_os_version",
			"service_template_id",
			"srv_status",
			"bk_comment",
			"idc_unit_name",
			"net_device_id",
			"rack_id",
			"bk_svr_device_cls_name",
			"svr_device_class"},
		"page": map[string]int{
			"limit": 500,
		},
	}
}

// InitResource 初始化resource信息并绑定data_id
func (b BcsClusterInfoSvc) InitResource() error {
	if b.BCSClusterInfo == nil {
		return errors.New("BCSClusterInfo obj can not be nil")
	}
	// 基于各dataid，生成配置并写入bcs集群
	for _, register := range bcsDatasourceRegisterInfo {
		dataidConfig, err := b.makeConfig(register)
		if err != nil {
			return err
		}
		name := b.composeDataidResourceName(strings.ToLower(register.DatasourceName))
		if err := b.ensureDataIdResource(name, dataidConfig); err != nil {
			return errors.Wrap(err, "ensure data id resource error")
		}
	}
	return nil

}

func (b BcsClusterInfoSvc) ensureDataIdResource(name string, config *unstructured.Unstructured) error {
	var action = "update"
	resp, err := b.GetK8sResource(name, models.BcsResourceGroupName, models.BcsResourceVersion, models.BcsResourceDataIdResourcePlural)
	if err != nil {
		var realErr *k8sErr.StatusError
		if errors.As(err, &realErr) {
			if realErr.Status().Code == http.StatusNotFound {
				action = "create"
			} else {
				return err
			}
		} else {
			return err
		}
	}
	if action == "update" {
		// 存在则更新
		config.SetResourceVersion(resp.GetResourceVersion())
		_, err = b.UpdateK8sResource(models.BcsResourceGroupName, models.BcsResourceVersion, models.BcsResourceDataIdResourcePlural, config)
		if err != nil {
			return errors.Wrapf(err, "update resource %s failed", name)
		}
	} else {
		_, err = b.CreateK8sResource(models.BcsResourceGroupName, models.BcsResourceVersion, models.BcsResourceDataIdResourcePlural, config)
		if err != nil {
			return errors.Wrapf(err, "create resource %s failed", name)
		}
	}
	logger.Infof("%s datasource %s succeed", action, name)
	return nil
}

// GetK8sClientConfig 构造k8s client的配置信息
func (b BcsClusterInfoSvc) GetK8sClientConfig() (*rest.Config, error) {
	if b.BCSClusterInfo == nil {
		return nil, errors.New("BCSClusterInfo obj can not be nil")
	}

	parsedUrl, err := url.Parse(cfg.BkApiBcsApiMicroGwUrl)
	if err != nil {
		return nil, err
	}
	scm := parsedUrl.Scheme
	if scm == "" {
		scm = "https"
	}
	config := &rest.Config{
		Host:        fmt.Sprintf("%s://%s:%v/%s/%s", scm, b.DomainName, b.Port, b.ServerAddressPath, b.ClusterID),
		BearerToken: fmt.Sprintf("%s %s", b.ApiKeyPrefix, b.ApiKeyContent),
		TLSClientConfig: rest.TLSClientConfig{
			Insecure: b.IsSkipSslVerify,
		},
	}
	return config, nil
}

// GetK8sDynamicClient 获取k8s Dynamic client
func (b BcsClusterInfoSvc) GetK8sDynamicClient() (*dynamic.DynamicClient, error) {
	if b.BCSClusterInfo == nil {
		return nil, errors.New("BCSClusterInfo obj can not be nil")
	}
	k8sConfig, err := b.GetK8sClientConfig()
	if err != nil {
		return nil, err
	}
	dynamicClient, err := dynamic.NewForConfig(k8sConfig)
	if err != nil {
		return nil, err
	}
	return dynamicClient, nil
}

func (b BcsClusterInfoSvc) GetK8sResource(name, group, version, resource string) (*unstructured.Unstructured, error) {
	if b.BCSClusterInfo == nil {
		return nil, errors.New("BCSClusterInfo obj can not be nil")
	}
	dynamicClient, err := b.GetK8sDynamicClient()
	if err != nil {
		return nil, err
	}
	gvr := schema.GroupVersionResource{Group: group, Version: version, Resource: resource}

	return dynamicClient.Resource(gvr).Get(context.Background(), name, metav1.GetOptions{})
}

// ListK8sResource 获取k8s resource信息列表
func (b BcsClusterInfoSvc) ListK8sResource(group, version, resource string) (*unstructured.UnstructuredList, error) {
	if b.BCSClusterInfo == nil {
		return nil, errors.New("BCSClusterInfo obj can not be nil")
	}
	dynamicClient, err := b.GetK8sDynamicClient()
	if err != nil {
		return nil, err
	}
	gvr := schema.GroupVersionResource{Group: group, Version: version, Resource: resource}

	return dynamicClient.Resource(gvr).List(context.Background(), metav1.ListOptions{})

}

// UpdateK8sResource 更新k8s resource信息
func (b BcsClusterInfoSvc) UpdateK8sResource(group, version, resource string, config *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	if b.BCSClusterInfo == nil {
		return nil, errors.New("BCSClusterInfo obj can not be nil")
	}
	dynamicClient, err := b.GetK8sDynamicClient()
	if err != nil {
		return nil, err
	}
	gvr := schema.GroupVersionResource{Group: group, Version: version, Resource: resource}

	return dynamicClient.Resource(gvr).Update(context.Background(), config, metav1.UpdateOptions{})
}

// CreateK8sResource 创建k8s resource信息
func (b BcsClusterInfoSvc) CreateK8sResource(group, version, resource string, config *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	if b.BCSClusterInfo == nil {
		return nil, errors.New("BCSClusterInfo obj can not be nil")
	}
	dynamicClient, err := b.GetK8sDynamicClient()
	if err != nil {
		return nil, err
	}
	gvr := schema.GroupVersionResource{Group: group, Version: version, Resource: resource}

	return dynamicClient.Resource(gvr).Create(context.Background(), config, metav1.CreateOptions{})
}

func (b BcsClusterInfoSvc) makeConfig(register *DatasourceRegister) (*unstructured.Unstructured, error) {
	rcSvc := NewReplaceConfigSvc(nil)
	replaceConfig, err := rcSvc.GetCommonReplaceConfig()
	if err != nil {
		return nil, err
	}
	clusterReplaceConfig, err := rcSvc.GetClusterReplaceConfig(b.ClusterID)
	if err != nil {
		return nil, err
	}
	for k, v := range clusterReplaceConfig[models.ReplaceTypesMetric] {
		replaceConfig[models.ReplaceTypesMetric][k] = v
	}
	for k, v := range clusterReplaceConfig[models.ReplaceTypesDimension] {
		replaceConfig[models.ReplaceTypesDimension][k] = v
	}

	var isSystem string
	if register.IsSystem {
		isSystem = "true"
	} else {
		isSystem = "false"
	}
	labels := map[string]interface{}{
		"usage":    register.Usage,
		"isCommon": "true",
		"isSystem": isSystem,
	}
	var dataId int64
	switch register.DatasourceName {
	case "K8sMetricDataID":
		dataId = int64(b.K8sMetricDataID)
	case "CustomMetricDataID":
		dataId = int64(b.CustomMetricDataID)
	case "K8sEventDataID":
		dataId = int64(b.K8sEventDataID)
	case "CustomEventDataID":
		dataId = int64(b.CustomEventDataID)
	}
	result := map[string]interface{}{
		"apiVersion": fmt.Sprintf("%s/%s", models.BcsResourceGroupName, models.BcsResourceVersion),
		"kind":       models.BcsResourceDataIdResourceKind,
		"metadata": map[string]interface{}{
			"name":   b.composeDataidResourceName(strings.ToLower(register.DatasourceName)),
			"labels": b.composeDataidResourceLabel(labels)},
		"spec": map[string]interface{}{
			"dataID": dataId,
			"labels": map[string]string{
				"bcs_cluster_id": b.ClusterID,
				"bk_biz_id":      strconv.Itoa(b.BkBizId),
			},
			"metricReplace":    replaceConfig[models.ReplaceTypesMetric],
			"dimensionReplace": replaceConfig[models.ReplaceTypesDimension],
		},
	}
	return &unstructured.Unstructured{Object: result}, nil
}

// 组装下发的配置资源的名称
func (b BcsClusterInfoSvc) composeDataidResourceName(name string) string {
	if b.bkEnvLabel() != "" {
		name = fmt.Sprintf("%s-%s", b.bkEnvLabel(), name)
	}
	return name
}

// 组装下发的配置资源的标签
func (b BcsClusterInfoSvc) composeDataidResourceLabel(labels map[string]interface{}) interface{} {
	if b.bkEnvLabel() != "" {
		labels["bk_env"] = b.bkEnvLabel()
	}
	return labels
}

// 集群配置标签
func (b BcsClusterInfoSvc) bkEnvLabel() string {
	// 如果指定集群有特定的标签，则以集群记录为准
	if b.BkEnv != nil {
		return *b.BkEnv
	}
	return cfg.BcsClusterBkEnvLabel
}

// RefreshCommonResource 刷新内置公共dataid资源信息，追加部署的资源，更新未同步的资源
func (b BcsClusterInfoSvc) RefreshCommonResource() error {
	if b.BCSClusterInfo == nil {
		return errors.New("BCSClusterInfo obj can not be nil")
	}
	resp, err := b.ListK8sResource(models.BcsResourceGroupName, models.BcsResourceVersion, models.BcsResourceDataIdResourcePlural)
	if err != nil {
		return err
	}
	logger.Infof("cluster [%s] got common dataid resource total [%v]", b.ClusterID, len(resp.Items))

	resourceMap := make(map[string]unstructured.Unstructured)
	for _, res := range resp.Items {
		resourceMap[res.GetName()] = res
	}

	for _, register := range bcsDatasourceRegisterInfo {
		datasourceNameLower := b.composeDataidResourceName(strings.ToLower(register.DatasourceName))
		dataIdConfig, err := b.makeConfig(register)
		if err != nil {
			return err
		}
		// 检查k8s集群里是否已经存在对应resource
		if _, ok := resourceMap[datasourceNameLower]; !ok {
			// 如果k8s_resource不存在，则增加
			if err := b.ensureDataIdResource(datasourceNameLower, dataIdConfig); err != nil {
				return err
			}
			return nil
		}
		// 否则检查信息是否一致，不一致则更新
		res := resourceMap[datasourceNameLower]
		if !b.isSameResourceConfig(dataIdConfig.UnstructuredContent(), res.UnstructuredContent()) {
			if err := b.ensureDataIdResource(datasourceNameLower, dataIdConfig); err != nil {
				return err
			}
			logger.Infof("cluster [%s] update resource [%v]", b.ClusterID, dataIdConfig)
		}

	}
	return nil
}

// 判断传入的config与当前是否相同，以dbConfig为准
func (b BcsClusterInfoSvc) isSameResourceConfig(dbConfig map[string]interface{}, currConfig map[string]interface{}) bool {
	// 只检查自己生成的配置，额外配置不检查
	return b.isSameMapConfig(dbConfig, currConfig)
}

func (b BcsClusterInfoSvc) isSameMapConfig(source map[string]interface{}, target map[string]interface{}) bool {
	// 以source为准
	for k, v := range source {
		val, ok := target[k]
		if !ok {
			return false
		}
		// warning 目前配置中要比较的类型不存在列表类型，先不处理
		switch reflect.TypeOf(v).Kind() {
		case reflect.Map:
			if reflect.TypeOf(val).Kind() != reflect.Map {
				return false
			} else {
				vMap, _ := v.(map[string]interface{})
				valMap, _ := val.(map[string]interface{})
				if !b.isSameMapConfig(vMap, valMap) {
					return false
				}
			}
		default:
			if v != val {
				return false
			}
		}
	}
	return true
}

// RefreshMetricLabel 刷新bcs指标的label
func (b BcsClusterInfoSvc) RefreshMetricLabel() error {
	// 获取所有bcs相关dataid
	dataids, err := b.getBcsDataids(nil)
	if err != nil {
		return errors.Wrap(err, "get all bcs dataids failed")
	}
	if len(dataids) == 0 {
		logger.Info("refresh bcs metrics label get no dataid")
		return nil
	}
	db := mysql.GetDBSession().DB
	// 基于dataid过滤出自定义指标group_id
	var tsGroups []customreport.TimeSeriesGroup
	if err := customreport.NewTimeSeriesGroupQuerySet(db).Select(customreport.TimeSeriesGroupDBSchema.TimeSeriesGroupID).BkDataIDIn(dataids...).IsDeleteEq(false).All(&tsGroups); err != nil {
		return errors.Wrap(err, "query tsGroup failed")
	}
	var tsGroupIds []uint
	for _, group := range tsGroups {
		tsGroupIds = append(tsGroupIds, group.TimeSeriesGroupID)
	}
	if len(tsGroupIds) == 0 {
		logger.Warnf("query tsGroup with dataids [%v] return no record", dataids)
		return nil
	}
	// 基于group_id拿到对应的指标项
	var tsMetrics []customreport.TimeSeriesMetric
	if err := customreport.NewTimeSeriesMetricQuerySet(db).Select(customreport.TimeSeriesMetricDBSchema.FieldName, customreport.TimeSeriesMetricDBSchema.FieldID, customreport.TimeSeriesMetricDBSchema.Label).GroupIDIn(tsGroupIds...).All(&tsMetrics); err != nil {
		return errors.Wrap(err, "query tsMetrics failed")
	}
	var defaultLabel string
	if prefix, ok := models.BcsMetricLabelPrefix["*"]; ok {
		defaultLabel = prefix
	}
	labelFieldIdMap := make(map[string][]uint)
	for _, metric := range tsMetrics {
		var targetLabel string
		// 通过遍历匹配，获取到需要处理label的指标信息
		for prefix, label := range models.BcsMetricLabelPrefix {
			if strings.HasPrefix(metric.FieldName, prefix) {
				targetLabel = label
				break
			}
		}
		if targetLabel == "" {
			targetLabel = defaultLabel
		}
		// 记录需要更新label的field_id，后面批量更新
		if metric.Label != targetLabel {
			if ids, ok := labelFieldIdMap[targetLabel]; ok {
				labelFieldIdMap[targetLabel] = append(ids, metric.FieldID)
			} else {
				labelFieldIdMap[targetLabel] = []uint{metric.FieldID}
			}
		}
	}
	// 每个label批量更新一下
	for label, ids := range labelFieldIdMap {
		for _, chunkIds := range slicex.ChunkSlice(ids, 0) {
			err := customreport.NewTimeSeriesMetricQuerySet(db).FieldIDIn(chunkIds...).GetUpdater().SetLastModifyTime(time.Now()).SetLabel(label).Update()
			if err != nil {
				logger.Errorf("update tsMetrics label [%s] for [%v] failed, %v", label, chunkIds, err)
				continue
			}
		}
	}
	return nil
}

// getBcsDataids 获取bcs相关dataid
func (BcsClusterInfoSvc) getBcsDataids(clusterIdList []string) ([]uint, error) {
	db := mysql.GetDBSession().DB
	var clusters []bcs.BCSClusterInfo
	qs := bcs.NewBCSClusterInfoQuerySet(db).Select(bcs.BCSClusterInfoDBSchema.ClusterID, bcs.BCSClusterInfoDBSchema.K8sMetricDataID, bcs.BCSClusterInfoDBSchema.CustomMetricDataID)
	if len(clusterIdList) != 0 {
		qs = qs.ClusterIDIn(clusterIdList...)
	}
	if err := qs.All(&clusters); err != nil {
		return nil, errors.Wrap(err, "query bcs cluster info failed")
	}
	if len(clusters) == 0 {
		logger.Infof("query BCSCluster but return empty")
		return []uint{}, nil
	}
	var realClusterIds []string
	dataids := mapset.NewSet[uint]()
	for _, c := range clusters {
		realClusterIds = append(realClusterIds, c.ClusterID)
		dataids.Add(c.K8sMetricDataID)
		dataids.Add(c.CustomMetricDataID)
	}

	var serviceMonitorList []bcs.ServiceMonitorInfo
	if err := bcs.NewServiceMonitorInfoQuerySet(db).Select(bcs.ServiceMonitorInfoDBSchema.BkDataId).ClusterIDIn(realClusterIds...).IsCommonDataIdEq(false).All(&serviceMonitorList); err != nil {
		return nil, errors.Wrap(err, "query service monitor info failed")
	}
	for _, info := range serviceMonitorList {
		dataids.Add(info.BkDataId)
	}

	var podMonitorList []bcs.PodMonitorInfo
	if err := bcs.NewPodMonitorInfoQuerySet(db).Select(bcs.PodMonitorInfoDBSchema.BkDataId).ClusterIDIn(realClusterIds...).IsCommonDataIdEq(false).All(&podMonitorList); err != nil {
		return nil, errors.Wrap(err, "query service monitor info failed")
	}
	for _, info := range podMonitorList {
		dataids.Add(info.BkDataId)
	}
	return dataids.ToSlice(), nil
}

// BcsClusterInfo FetchK8sClusterList 中返回的集群信息对象
type BcsClusterInfo struct {
	BkBizId      string `json:"bk_biz_id"`
	ClusterId    string `json:"cluster_id"`
	BcsClusterId string `json:"bcs_cluster_id"`
	Id           string `json:"id"`
	Name         string `json:"name"`
	ProjectId    string `json:"project_id"`
	ProjectName  string `json:"project_name"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	Status       string `json:"status"`
	Environment  string `json:"environment"`
}

// K8sNodeInfo FetchK8sNodeListByCluster中返回的节点信息对象
type K8sNodeInfo struct {
	BcsClusterId  string              `json:"bcs_cluster_id"`
	Node          NodeInfo            `json:"node"`
	Name          string              `json:"name"`
	Taints        []string            `json:"taints"`
	NodeRoles     []string            `json:"node_roles"`
	NodeIp        string              `json:"node_ip"`
	Status        string              `json:"status"`
	NodeName      string              `json:"node_name"`
	LabelList     []map[string]string `json:"label_list"`
	Labels        map[string]string   `json:"labels"`
	EndpointCount int                 `json:"endpoint_count"`
	PodCount      int                 `json:"pod_count"`
	CreatedAt     time.Time           `json:"created_at"`
	Age           string              `json:"age"`
}

// FetchBcsStorageResp FetchBcsStorage的返回对象
type FetchBcsStorageResp struct {
	define.ApiCommonRespMeta
	Data []NodeInfo `json:"data"`
}

type KubernetesNodeJsonParser struct {
	Node NodeInfo
}

// NodeIp 获得node的ip地址
func (k KubernetesNodeJsonParser) NodeIp() string {
	for _, address := range k.Node.Status.Addresses {
		if address.Type == "InternalIP" {
			return address.Address
		}
	}
	return ""
}

func (k KubernetesNodeJsonParser) Name() string {
	return k.Node.Metadata.Name
}

func (k KubernetesNodeJsonParser) Labels() map[string]string {
	return k.Node.Metadata.Labels
}

// LabelList 将标签从字段转换列表格式
func (k KubernetesNodeJsonParser) LabelList() []map[string]string {
	var labelList []map[string]string
	for key, value := range k.Node.Metadata.Labels {
		labelList = append(labelList, map[string]string{"key": key, "value": value})
	}
	return labelList
}

// RoleList 获得node角色
func (k KubernetesNodeJsonParser) RoleList() []string {
	nodeRoleKeyPrefix := "node-role.kubernetes.io/"
	var roles []string
	for key, _ := range k.Node.Metadata.Labels {
		if strings.HasPrefix(key, nodeRoleKeyPrefix) {
			value := key[len(nodeRoleKeyPrefix):]
			if value != "" {
				roles = append(roles, value)

			}
		}
	}
	return roles
}

// ServiceStatus 获得node的服务状态
func (k KubernetesNodeJsonParser) ServiceStatus() string {
	var statusList []string
	for _, c := range k.Node.Status.Conditions {
		if c.Type == "Ready" {
			if c.Status == "True" {
				statusList = append(statusList, "Ready")
			} else {
				statusList = append(statusList, "NotReady")

			}
		}
	}
	if len(statusList) == 0 {
		statusList = append(statusList, "Unknown")
	}

	if unschedulableInterface, ok := k.Node.Spec["unschedulable"]; ok {
		if unschedulableInterface.(bool) {
			statusList = append(statusList, "SchedulingDisabled")
		}
	}

	return strings.Join(statusList, ",")
}

func (k KubernetesNodeJsonParser) GetEndpointsCount(endpoints []NodeInfo) int {
	var count = 0
	for _, endpoint := range endpoints {
		for _, subset := range endpoint.Subsets {
			var addressCount int
			addressInterface, ok := subset["addresses"]
			if !ok {
				continue
			}
			addressList, ok := addressInterface.([]interface{})
			if !ok {
				continue
			}
			for _, addressInterface := range addressList {

				addressMap, ok := addressInterface.(map[string]interface{})
				if !ok {
					continue
				}
				address := optionx.NewOptions(addressMap)
				nodeName, _ := address.GetString("nodeName")
				if k.Name() == nodeName {
					addressCount += 1
				}
			}
			portsInterface, ok := subset["ports"]
			if !ok {
				continue
			}
			ports, _ := portsInterface.([]interface{})
			count += addressCount * len(ports)
		}
	}
	return count
}

// CreationTimestamp 获取创建的时间
func (k KubernetesNodeJsonParser) CreationTimestamp() *time.Time {
	if k.Node.Metadata.CreationTimestamp != nil {
		return k.Node.Metadata.CreationTimestamp
	}
	return k.Node.Metadata.CreationTimestampB
}

// TaintLabels 获得节点的污点配置
func (k KubernetesNodeJsonParser) TaintLabels() []string {
	var labels = make([]string, 0)
	taintsInterface, ok := k.Node.Spec["taints"]
	if !ok {
		return labels
	}
	taints, ok := taintsInterface.([]interface{})
	if !ok {
		return labels
	}
	for _, taintInterface := range taints {
		taint, ok := taintInterface.(map[string]interface{})
		if !ok {
			continue
		}
		t := optionx.NewOptions(taint)
		key, _ := t.GetString("key")
		value, _ := t.GetString("value")
		effect, _ := t.GetString("effect")
		if key == "" && value == "" && effect == "" {
			continue
		}
		labels = append(labels, fmt.Sprintf("%v=%v:%v", key, value, effect))
	}
	return labels
}

// Age 获得运行的时间
func (k KubernetesNodeJsonParser) Age() time.Duration {

	return time.Now().UTC().Sub(*k.CreationTimestamp())
}

// NodeInfo 节点信息
type NodeInfo struct {
	Spec   map[string]interface{} `json:"spec"`
	Status struct {
		Addresses []struct {
			Address string `json:"address"`
			Type    string `json:"type"`
		} `json:"addresses"`
		Conditions []struct {
			LastHeartbeatTime  time.Time `json:"lastHeartbeatTime"`
			LastTransitionTime time.Time `json:"lastTransitionTime"`
			Message            string    `json:"message"`
			Reason             string    `json:"reason"`
			Status             string    `json:"status"`
			Type               string    `json:"type"`
		} `json:"conditions"`
	} `json:"status"`
	Metadata struct {
		CreationTimestamp  *time.Time        `json:"creationTimestamp"`
		CreationTimestampB *time.Time        `json:"creation_timestamp"`
		Labels             map[string]string `json:"labels"`
		Name               string            `json:"name"`
		ResourceVersion    string            `json:"resourceVersion"`
	} `json:"metadata"`
	Subsets []map[string]interface{} `json:"subsets"`
}

// DatasourceRegister for datasource register
type DatasourceRegister struct {
	EtlConfig         string
	ReportClassName   string
	DatasourceName    string
	IsSpitMeasurement bool
	IsSystem          bool
	Usage             string
}

// RefreshClusterResource 当绑定资源的集群信息变动时，刷新绑定的集群资源
func (b BcsClusterInfoSvc) RefreshClusterResource() error {
	db := mysql.GetDBSession().DB
	srDimensionMap := make(map[string][]map[string]interface{})
	var srList []space.SpaceResource
	if err := space.NewSpaceResourceQuerySet(db).Select(space.SpaceResourceDBSchema.ResourceId, space.SpaceResourceDBSchema.DimensionValues).SpaceTypeIdEq(models.SpaceTypeBKCI).ResourceTypeEq(models.SpaceTypeBCS).All(&srList); err != nil {
		return errors.Wrapf(err, "query SpaceResource with space_type_id [%s] resource_type [%s] failed", models.SpaceTypeBKCI, models.SpaceTypeBCS)
	}
	for _, sr := range srList {
		dm, err := sr.GetDimensionValues()
		if err != nil {
			return errors.Wrapf(err, "get DimensionValues of SpaceResource [%v] failed", sr.Id)
		}
		srDimensionMap[*sr.ResourceId] = dm
	}
	var spList []space.Space
	if err := space.NewSpaceQuerySet(db).Select(space.SpaceDBSchema.SpaceId, space.SpaceDBSchema.SpaceCode).SpaceTypeIdEq(models.SpaceTypeBKCI).IsBcsValidEq(true).SpaceCodeNe("").All(&spList); err != nil {
		return errors.Wrapf(err, "query Space with space_type_id [%s] is_bcs_calid [%v] failed", models.SpaceTypeBKCI, true)
	}

	// 根据项目查询项目下资源的变化
	var metadataClusters []string
	var bcsClusterInfoList []bcs.BCSClusterInfo
	if err := bcs.NewBCSClusterInfoQuerySet(db).Select(bcs.BCSClusterInfoDBSchema.ClusterID).StatusEq(models.BcsClusterStatusRunning).All(&bcsClusterInfoList); err != nil {
		return errors.Wrapf(err, "query BCSClusterInfo with status [%s] failed", models.BcsClusterStatusRunning)
	}
	for _, c := range bcsClusterInfoList {
		metadataClusters = append(metadataClusters, c.ClusterID)
	}
	spaceDataIdsMap := make(map[string][]uint)
	sharedSpaceDataIdsMap := make(map[string][]uint)
	spaceIdSet := mapset.NewSet[string]()
	for _, sp := range spList {
		clusterInfos, err := apiservice.BcsClusterManager.GetProjectClusters(sp.SpaceCode, false)
		if err != nil {
			logger.Errorf("GetProjectClusters for code [%s] failed, %v", sp.SpaceCode, err)
			continue
		}
		if len(clusterInfos) == 0 {
			continue
		}

		usedClusterSet := mapset.NewSet[string]()
		projectClusterSet := mapset.NewSet[string]()
		shardClusterSet := mapset.NewSet[string]()
		shardClusterNsMap := make(map[string][]string)
		var dimensionValues []map[string]interface{}
		var skip bool
		for _, c := range clusterInfos {
			clusterId, ok := c["clusterId"].(string)
			// 防止共享集群所在项目返回相同集群的场景
			if !ok || usedClusterSet.Contains(clusterId) || !slicex.IsExistItem(metadataClusters, clusterId) {
				continue
			}
			usedClusterSet.Add(clusterId)
			// 构造dimensionValue
			isShared, _ := c["isShared"].(bool)
			if isShared {
				nsDataList, err := apiservice.Bcs.FetchSharedClusterNamespaces(clusterId, sp.SpaceCode)
				if err != nil {
					logger.Errorf("FetchSharedClusterNamespaces for cluster_id [%s] code [%s] failed,%v", clusterId, sp.SpaceCode, err)
					skip = true
					break
				}
				nsSet := mapset.NewSet[string]()
				for _, i := range nsDataList {
					if i["clusterId"] == clusterId {
						nsSet.Add(i["namespace"])
					}
				}
				nsList := nsSet.ToSlice()
				dimensionValues = append(dimensionValues, map[string]interface{}{"cluster_id": clusterId, "namespace": nsList, "cluster_type": models.BcsClusterTypeShared})
				shardClusterNsMap[clusterId] = nsList
				shardClusterSet.Add(clusterId)
			} else {
				dimensionValues = append(dimensionValues, map[string]interface{}{"cluster_id": clusterId, "namespace": nil, "cluster_type": models.BcsClusterTypeSingle})
				projectClusterSet.Add(clusterId)
			}
		}
		if skip {
			continue
		}

		projectClusterList := projectClusterSet.ToSlice()
		shardClusterList := shardClusterSet.ToSlice()

		dataids, err := b.getClusterDataIds(projectClusterList)
		if err != nil {
			logger.Errorf("getClusterDataIds for project cluster failed, %v", err)
			continue
		}
		mapx.AddSliceItems(spaceDataIdsMap, sp.SpaceId, dataids...)
		logger.Infof("cluster data id info [%v]", spaceDataIdsMap)

		dataids, err = b.getClusterDataIds(shardClusterList)
		if err != nil {
			logger.Errorf("getClusterDataIds for project cluster failed, %v", err)
			continue
		}
		mapx.AddSliceItems(sharedSpaceDataIdsMap, sp.SpaceId, dataids...)
		logger.Infof("shared cluster data id info [%v]", sharedSpaceDataIdsMap)

		dms, ok := srDimensionMap[sp.SpaceId]
		// 不存在则新建
		if !ok {
			sr := space.SpaceResource{
				SpaceTypeId:  models.SpaceTypeBKCI,
				SpaceId:      sp.SpaceId,
				ResourceType: models.SpaceTypeBCS,
				ResourceId:   &sp.SpaceId,
			}
			if err := sr.SetDimensionValues(dimensionValues); err != nil {
				logger.Errorf("set dimensionValues for sapce_id [%s] failed, %v", sp.SpaceId, err)
				continue
			}
			_ = metrics.MysqlCount(space.SpaceResource{}.TableName(), "RefreshClusterResource_create_SpaceResource", 1)
			if cfg.BypassSuffixPath != "" {
				logger.Infof("[db_diff] create SpaceResource [%#v]", sr)
			} else {
				if err := sr.Create(db); err != nil {
					logger.Errorf("create SpaceResource [%#v] failed", sr)
					continue
				}
			}
			spaceIdSet.Add(sp.SpaceId)
			logger.Infof("create bcs space resource successfully, space [%s]", sr.SpaceId)
			continue
		}
		// 获取现存的，判断是否有变化
		existProjectClusterSet := mapset.NewSet[string]()
		existShardClusterNs := make(map[string]interface{})
		var needUpdate bool
		for _, dm := range dms {
			clusterId, ok := dm["cluster_id"].(string)
			if !ok {
				needUpdate = true
				break
			}
			ns := dm["namespace"]
			if ns != nil {
				existShardClusterNs[clusterId] = ns
			} else {
				existProjectClusterSet.Add(clusterId)
			}
		}
		equal, err := jsonx.CompareObjects(existShardClusterNs, shardClusterNsMap)
		if err != nil {
			logger.Errorf("CompareObjects [%#v] and [%#v] failed, %v", existShardClusterNs, shardClusterNsMap, err)
		}
		// 有差异则更新dimensionValues 并记录space_uid
		if needUpdate || !projectClusterSet.Equal(existProjectClusterSet) || !equal {
			dmStr, err := jsonx.MarshalString(dimensionValues)
			if err != nil {
				logger.Errorf("marshal dimensionValues [%#v] failed, %v", dimensionValues, err)
				continue
			}
			_ = metrics.MysqlCount(space.SpaceResource{}.TableName(), "RefreshClusterResource_update_SpaceResource", 1)
			if cfg.BypassSuffixPath != "" {
				logger.Infof("[db_diff] update SpaceResource space_id [%s], dimension_values is different old [%v] new [%v]", sp.SpaceId, dms, dimensionValues)
			} else {
				if err := space.NewSpaceResourceQuerySet(db).SpaceTypeIdEq(models.SpaceTypeBKCI).SpaceIdEq(sp.SpaceId).ResourceTypeEq(models.SpaceTypeBCS).ResourceIdEq(sp.SpaceId).GetUpdater().SetDimensionValues(dmStr).SetUpdateTime(time.Now()).Update(); err != nil {
					logger.Errorf("update dimensionValues [%s] for SpaceResource with space_id [%s] failed,  %v", dmStr, sp.SpaceId, err)
					continue
				}
			}
			spaceIdSet.Append(sp.SpaceId)
		}
	}
	// 根据空间 data id，判断是否已经添加
	// 创建专用集群下的数据源 ID 记录
	// 因为共享集群在所属项目下会返回两次，因此，需要先创建属于专用集群的数据源关联
	changedSpaceIds, err := NewSpaceDataSourceSvc(nil).BulkCreateRecords(models.SpaceTypeBKCI, spaceDataIdsMap, false)
	if err != nil {
		logger.Errorf("BulkCreate SpaceDataSource for spaceDataIdsMap failed, %v", err)
	}
	spaceIdSet.Append(changedSpaceIds...)
	// 创建共享集群下的数据源 ID 记录
	changedSpaceIds, err = NewSpaceDataSourceSvc(nil).BulkCreateRecords(models.SpaceTypeBKCI, sharedSpaceDataIdsMap, false)
	if err != nil {
		logger.Errorf("BulkCreate SpaceDataSource for sharedSpaceDataIdsMap failed, %v", err)
	}
	spaceIdSet.Append(changedSpaceIds...)

	spaceIdList := spaceIdSet.ToSlice()
	if len(spaceIdList) != 0 {
		pusher := NewSpaceRedisSvc(0)
		if err := pusher.PushAndPublishSpaceRouter(models.SpaceTypeBKCI, "", spaceIdList); err != nil {
			return errors.Wrapf(err, "PushAndPublishSpaceRouter for space_type [%s] space_ids [%v] failed", models.SpaceTypeBKCI, spaceIdList)
		}
	}
	return nil
}

func (b BcsClusterInfoSvc) getClusterDataIds(clusterIdList []string) ([]uint, error) {
	// 如果指定结果表, 则仅过滤结果表对应的数据源
	db := mysql.GetDBSession().DB
	var clusterList []bcs.BCSClusterInfo
	if len(clusterIdList) != 0 {
		if err := bcs.NewBCSClusterInfoQuerySet(db).Select(bcs.BCSClusterInfoDBSchema.K8sMetricDataID, bcs.BCSClusterInfoDBSchema.CustomMetricDataID, bcs.BCSClusterInfoDBSchema.K8sEventDataID).StatusEq(models.BcsClusterStatusRunning).ClusterIDIn(clusterIdList...).All(&clusterList); err != nil {
			return nil, errors.Wrapf(err, "query BCSClusterInfo with cluster_id [%v] failed", clusterList)
		}
	}
	dataidSet := mapset.NewSet[uint]()
	for _, cluster := range clusterList {
		dataidSet.Add(cluster.K8sMetricDataID)
		dataidSet.Add(cluster.CustomMetricDataID)
		dataidSet.Add(cluster.K8sEventDataID)
	}
	return dataidSet.ToSlice(), nil
}
