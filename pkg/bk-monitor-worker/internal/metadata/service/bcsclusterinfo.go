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

	"github.com/pkg/errors"
	"github.com/spf13/viper"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/bcsclustermanager"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/bcs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	EnableBcsGrayPath                        = "bcs.enable_bcs_gray"                             // 是否启用BCS集群灰度模式
	BcsGrayClusterIdListPath                 = "bcs.gray_cluster_id_list"                        // BCS集群灰度ID名单
	BcsClusterBkEnvLabelPath                 = "bcs.cluster_bk_env_label"                        // BCS集群配置来源标签
	BcsKafkaStorageClusterIdPath             = "bcs.kafka_storage_cluster_id"                    // BCS kafka 存储集群ID
	BcsCustomEventStorageClusterId           = "bcs.custom_event_storage_cluster_id"             // 自定义上报存储集群ID
	BcsInfluxdbDefaultProxyClusterNameForK8s = "bcs.influxdb_default_proxy_cluster_name_for_k8s" // influxdb proxy给k8s默认使用集群名
)

func init() {
	viper.SetDefault(EnableBcsGrayPath, false)
	viper.SetDefault(BcsGrayClusterIdListPath, []uint{})
	viper.SetDefault(BcsClusterBkEnvLabelPath, "")
	viper.SetDefault(BcsKafkaStorageClusterIdPath, 0)
	viper.SetDefault(BcsInfluxdbDefaultProxyClusterNameForK8s, "default")
	viper.SetDefault(BcsCustomEventStorageClusterId, 0)
}

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
	// For test
	//err := jsonx.UnmarshalString(`{"code":0,"message":"success","result":true,"data":[{"clusterID":"BCS-K8S-00000","clusterName":"蓝鲸","federationClusterID":"","provider":"bluekingCloud","region":"default","vpcID":"","projectID":"7538606f025efa007f3e750477982c23","businessID":"2","environment":"prod","engineType":"k8s","isExclusive":true,"clusterType":"single","labels":{},"creator":"admin","createTime":"2023-08-08T19:54:03+08:00","updateTime":"2023-08-08T19:54:03+08:00","bcsAddons":{},"extraAddons":{},"systemID":"","manageType":"INDEPENDENT_CLUSTER","master":{},"networkSettings":{"clusterIPv4CIDR":"","serviceIPv4CIDR":"","maxNodePodNum":0,"maxServiceNum":0,"enableVPCCni":false,"eniSubnetIDs":[],"subnetSource":null,"isStaticIpMode":false,"claimExpiredSeconds":0,"multiClusterCIDR":[],"cidrStep":0},"clusterBasicSettings":{"OS":"Linux","version":"v1.20.6-tke.34","clusterTags":{},"versionName":""},"clusterAdvanceSettings":{"IPVS":true,"containerRuntime":"docker","runtimeVersion":"19.3","extraArgs":{"Etcd":"node-data-dir=/data/bcs/lib/etcd;"}},"nodeSettings":{"dockerGraphPath":"/data/bcs/lib/docker","mountTarget":"/data","unSchedulable":1,"labels":{},"extraArgs":{}},"status":"RUNNING","updater":"","networkType":"overlay","autoGenerateMasterNodes":false,"template":[],"extraInfo":{},"moduleID":"","extraClusterID":"","isCommonCluster":false,"description":"蓝鲸容器化部署环境","clusterCategory":"","is_shared":false,"kubeConfig":"","importCategory":"","cloudAccountID":""}],"clusterExtraInfo":{"BCS-K8S-00000":{"canDeleted":true,"providerType":"k8s"}},"web_annotations":{"perms":{}}}`, &resp)
	_, err = managerApi.FetchClusters().SetResult(&resp).Request()
	if err != nil {
		return nil, err
	}
	var clusterList []BcsClusterInfo
	for _, cluster := range resp.Data {
		clusterId := (cluster["clusterID"]).(string)
		businessID := (cluster["businessID"]).(string)
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

		clusterList = append(clusterList, BcsClusterInfo{
			BkBizId:      businessID,
			ClusterId:    clusterId,
			BcsClusterId: clusterId,
			Id:           clusterId,
			Name:         (cluster["clusterName"]).(string),
			ProjectId:    (cluster["projectID"]).(string),
			ProjectName:  "",
			CreatedAt:    (cluster["createTime"]).(string),
			UpdatedAt:    (cluster["updateTime"]).(string),
			Status:       (cluster["status"]).(string),
			Environment:  (cluster["environment"]).(string),
		})
	}

	return clusterList, nil
}

// IsClusterIdInGray 判断cluster id是否在灰度配置中
func (BcsClusterInfoSvc) IsClusterIdInGray(clusterId string) bool {
	// 未启用灰度配置，全返回true
	if !viper.GetBool(EnableBcsGrayPath) {
		return true
	}
	grayBcsClusterList := viper.GetStringSlice(BcsGrayClusterIdListPath)

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
		hostInfo, err := b.getHostByIp(ips, b.BkBizId)
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
		return nil, errors.Wrap(err, fmt.Sprintf("fetch bcs storage Node for %s failed, %s", bcsClusterId, err))
	}
	endpoints, err := b.fetchBcsStorage(bcsClusterId, endpointField, "Endpoints")
	if err != nil {
		return nil, errors.Wrap(err, fmt.Sprintf("fetch bcs storage Endpoints for %s failed, %s", bcsClusterId, err))
	}
	statistics, err := b.getPodCountStatistics(bcsClusterId)
	if err != nil {
		return nil, err
	}

	var result []K8sNodeInfo
	for _, node := range nodes {
		parser := KubernetesNodeJsonParser{node.Data}
		var nodeIp = parser.NodeIp()
		var name = parser.Name()
		result = append(result, K8sNodeInfo{
			BcsClusterId:  bcsClusterId,
			Node:          node.Data,
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
			Age:           strconv.FormatInt(int64(parser.Age()), 10), //todo humanize
		})
	}
	return result, nil
}

// 获取bcs storage
func (BcsClusterInfoSvc) fetchBcsStorage(clusterId, field, sourceType string) ([]FetchBcsStorageRespData, error) {
	urlTemplate := "%s://%s:%v/bcsapi/v4/storage/k8s/dynamic/all_resources/clusters/%s/%s?field=%s"
	client := &http.Client{
		Timeout: 5 * time.Second,
	}
	target, err := url.Parse(fmt.Sprintf(urlTemplate, viper.GetString(api.BkApiBcsApiGatewaySchemaPath), viper.GetString(api.BkApiBcsApiGatewayHostPath), viper.GetString(api.BkApiBcsApiGatewayPortPath), clusterId, sourceType, field))
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", viper.GetString(api.BkApiBcsApiGatewayTokenPath)))

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	// For test
	//var body []byte
	//if sourceType == "Node" {
	//	body = []byte(`{"result":true,"code":0,"message":"success","data":[{"_id":"6507b8ce2299083f14c9c3d3","data":{"status":{"addresses":[{"address":"10.0.3.5","type":"InternalIP"},{"address":"10.0.3.5","type":"Hostname"}],"conditions":[{"type":"NetworkUnavailable","lastHeartbeatTime":"2023-09-18T02:41:24Z","lastTransitionTime":"2023-09-18T02:41:24Z","message":"RouteController created a route","reason":"RouteCreated","status":"False"},{"lastTransitionTime":"2023-09-18T02:41:18Z","message":"kubelet has sufficient memory available","reason":"KubeletHasSufficientMemory","status":"False","type":"MemoryPressure","lastHeartbeatTime":"2023-10-12T13:04:07Z"},{"lastHeartbeatTime":"2023-10-12T13:04:07Z","lastTransitionTime":"2023-09-18T02:41:18Z","message":"kubelet has no disk pressure","reason":"KubeletHasNoDiskPressure","status":"False","type":"DiskPressure"},{"reason":"KubeletHasSufficientPID","status":"False","type":"PIDPressure","lastHeartbeatTime":"2023-10-12T13:04:07Z","lastTransitionTime":"2023-09-18T02:41:18Z","message":"kubelet has sufficient PID available"},{"lastHeartbeatTime":"2023-10-12T13:04:07Z","lastTransitionTime":"2023-09-18T02:42:08Z","message":"kubelet is posting ready status","reason":"KubeletReady","status":"True","type":"Ready"}]},"metadata":{"labels":{"kubernetes.io/arch":"amd64","tke.cloud.tencent.com/cbs-mountable":"true","kubernetes.io/os":"linux","cloud.tencent.com/auto-scaling-group-id":"asg-2asi3wuq","failure-domain.beta.kubernetes.io/region":"gz","beta.kubernetes.io/arch":"amd64","node.kubernetes.io/instance-type":"SA2.8XLARGE64","tke.cloud.tencent.com/nodepool-id":"np-2w3daqz8","topology.kubernetes.io/zone":"100003","failure-domain.beta.kubernetes.io/zone":"100003","cloud.tencent.com/node-instance-id":"ins-891cqjk0","beta.kubernetes.io/instance-type":"SA2.8XLARGE64","topology.com.tencent.cloud.csi.cbs/zone":"ap-guangzhou-3","beta.kubernetes.io/os":"linux","topology.kubernetes.io/region":"gz","eng-cstate-86fabc2b-6":"1","kubernetes.io/hostname":"10.0.3.5"},"name":"10.0.3.5","resourceVersion":"10118787130","creationTimestamp":"2023-09-18T02:41:18Z"},"spec":{}}},{"_id":"64f976472299083f1401f278","data":{"metadata":{"creationTimestamp":"2023-09-07T07:05:43Z","labels":{"node-role.kubernetes.io/abcd":"abc" ,"eng-cstate-86fabc2b-6":"1","failure-domain.beta.kubernetes.io/zone":"100003","kubernetes.io/hostname":"10.0.3.6","eng-cstate-3e8191b0-5":"1","beta.kubernetes.io/instance-type":"SA2.8XLARGE64","kubernetes.io/os":"linux","tke.cloud.tencent.com/cbs-mountable":"true","topology.kubernetes.io/region":"gz","beta.kubernetes.io/os":"linux","node.kubernetes.io/instance-type":"SA2.8XLARGE64","failure-domain.beta.kubernetes.io/region":"gz","tke.cloud.tencent.com/nodepool-id":"np-2w3daqz8","beta.kubernetes.io/arch":"amd64","kubernetes.io/arch":"amd64","cloud.tencent.com/auto-scaling-group-id":"asg-2asi3wuq","topology.com.tencent.cloud.csi.cbs/zone":"ap-guangzhou-3","cloud.tencent.com/node-instance-id":"ins-6orwh5dw","topology.kubernetes.io/zone":"100003"},"name":"10.0.3.6","resourceVersion":"10117999929"},"spec":{},"status":{"addresses":[{"address":"10.0.3.6","type":"InternalIP"},{"address":"10.0.3.6","type":"Hostname"}],"conditions":[{"lastHeartbeatTime":"2023-09-07T07:05:50Z","lastTransitionTime":"2023-09-07T07:05:50Z","message":"RouteController created a route","reason":"RouteCreated","status":"False","type":"NetworkUnavailable"},{"lastHeartbeatTime":"2023-10-12T12:42:49Z","lastTransitionTime":"2023-09-07T07:05:43Z","message":"kubelet has sufficient memory available","reason":"KubeletHasSufficientMemory","status":"False","type":"MemoryPressure"},{"reason":"KubeletHasNoDiskPressure","status":"False","type":"DiskPressure","lastHeartbeatTime":"2023-10-12T12:42:49Z","lastTransitionTime":"2023-09-07T07:05:43Z","message":"kubelet has no disk pressure"},{"reason":"KubeletHasSufficientPID","status":"False","type":"PIDPressure","lastHeartbeatTime":"2023-10-12T12:42:49Z","lastTransitionTime":"2023-09-07T07:05:43Z","message":"kubelet has sufficient PID available"},{"message":"kubelet is posting ready status","reason":"KubeletReady","status":"True","type":"Ready","lastHeartbeatTime":"2023-10-12T12:42:49Z","lastTransitionTime":"2023-09-07T07:06:53Z"}]}}},{"_id":"6507b8ce2299083f14c9c457","data":{"metadata":{"resourceVersion":"10149527788","creationTimestamp":"2023-09-18T02:41:18Z","labels":{"kubernetes.io/hostname":"10.0.3.7","kubernetes.io/os":"linux","beta.kubernetes.io/arch":"amd64","tke.cloud.tencent.com/cbs-mountable":"true","tke.cloud.tencent.com/nodepool-id":"np-2w3daqz8","topology.kubernetes.io/region":"gz","node.kubernetes.io/instance-type":"SA2.8XLARGE64","eng-cstate-86fabc2b-6":"1","cloud.tencent.com/auto-scaling-group-id":"asg-2asi3wuq","topology.kubernetes.io/zone":"100003","topology.com.tencent.cloud.csi.cbs/zone":"ap-guangzhou-3","cloud.tencent.com/node-instance-id":"ins-hcc1rv8o","failure-domain.beta.kubernetes.io/zone":"100003","beta.kubernetes.io/os":"linux","kubernetes.io/arch":"amd64","failure-domain.beta.kubernetes.io/region":"gz","beta.kubernetes.io/instance-type":"SA2.8XLARGE64"},"name":"10.0.3.7"},"spec":{},"status":{"addresses":[{"address":"10.0.3.7","type":"InternalIP"},{"type":"Hostname","address":"10.0.3.7"}],"conditions":[{"reason":"RouteCreated","status":"False","type":"NetworkUnavailable","lastHeartbeatTime":"2023-09-18T02:41:24Z","lastTransitionTime":"2023-09-18T02:41:24Z","message":"RouteController created a route"},{"reason":"KubeletHasSufficientMemory","status":"False","type":"MemoryPressure","lastHeartbeatTime":"2023-10-13T02:53:55Z","lastTransitionTime":"2023-09-18T02:41:18Z","message":"kubelet has sufficient memory available"},{"message":"kubelet has no disk pressure","reason":"KubeletHasNoDiskPressure","status":"False","type":"DiskPressure","lastHeartbeatTime":"2023-10-13T02:53:55Z","lastTransitionTime":"2023-09-25T08:18:35Z"},{"type":"PIDPressure","lastHeartbeatTime":"2023-10-13T02:53:55Z","lastTransitionTime":"2023-09-18T02:41:18Z","message":"kubelet has sufficient PID available","reason":"KubeletHasSufficientPID","status":"False"},{"status":"True","type":"Ready","lastHeartbeatTime":"2023-10-13T02:53:55Z","lastTransitionTime":"2023-09-18T02:41:58Z","message":"kubelet is posting ready status","reason":"KubeletReady"}]}}},{"_id":"64f977012299083f14029ab8","data":{"status":{"conditions":[{"lastTransitionTime":"2023-09-07T07:09:00Z","message":"RouteController created a route","reason":"RouteCreated","status":"False","type":"NetworkUnavailable","lastHeartbeatTime":"2023-09-07T07:09:00Z"},{"lastTransitionTime":"2023-09-07T07:08:49Z","message":"kubelet has sufficient memory available","reason":"KubeletHasSufficientMemory","status":"False","type":"MemoryPressure","lastHeartbeatTime":"2023-10-13T02:54:12Z"},{"lastHeartbeatTime":"2023-10-13T02:54:12Z","lastTransitionTime":"2023-09-07T07:08:49Z","message":"kubelet has no disk pressure","reason":"KubeletHasNoDiskPressure","status":"False","type":"DiskPressure"},{"message":"kubelet has sufficient PID available","reason":"KubeletHasSufficientPID","status":"False","type":"PIDPressure","lastHeartbeatTime":"2023-10-13T02:54:12Z","lastTransitionTime":"2023-09-07T07:08:49Z"},{"reason":"KubeletReady","status":"True","type":"Ready","lastHeartbeatTime":"2023-10-13T02:54:12Z","lastTransitionTime":"2023-09-07T07:09:29Z","message":"kubelet is posting ready status"}],"addresses":[{"address":"10.0.4.12","type":"InternalIP"},{"type":"Hostname","address":"10.0.4.12"}]},"metadata":{"creationTimestamp":"2023-09-07T07:08:49Z","labels":{"cloud.tencent.com/node-instance-id":"ins-ho6ylcci","failure-domain.beta.kubernetes.io/zone":"100004","eng-cstate-3e8191b0-5":"1","beta.kubernetes.io/arch":"amd64","failure-domain.beta.kubernetes.io/region":"gz","beta.kubernetes.io/instance-type":"SA2.8XLARGE64","topology.kubernetes.io/region":"gz","kubernetes.io/arch":"amd64","tke.cloud.tencent.com/cbs-mountable":"true","node.kubernetes.io/instance-type":"SA2.8XLARGE64","topology.kubernetes.io/zone":"100004","tke.cloud.tencent.com/nodepool-id":"np-2w3daqz8","kubernetes.io/hostname":"10.0.4.12","kubernetes.io/os":"linux","cloud.tencent.com/auto-scaling-group-id":"asg-2asi3wuq","eng-cstate-86fabc2b-6":"1","beta.kubernetes.io/os":"linux","topology.com.tencent.cloud.csi.cbs/zone":"ap-guangzhou-4"},"name":"10.0.4.12","resourceVersion":"10149538252"},"spec":{}}},{"data":{"status":{"addresses":[{"address":"10.0.4.4","type":"InternalIP"},{"type":"Hostname","address":"10.0.4.4"}],"conditions":[{"message":"RouteController created a route","reason":"RouteCreated","status":"False","type":"NetworkUnavailable","lastHeartbeatTime":"2023-09-18T02:41:14Z","lastTransitionTime":"2023-09-18T02:41:14Z"},{"type":"MemoryPressure","lastHeartbeatTime":"2023-10-12T13:03:59Z","lastTransitionTime":"2023-09-18T02:41:11Z","message":"kubelet has sufficient memory available","reason":"KubeletHasSufficientMemory","status":"False"},{"lastTransitionTime":"2023-09-18T02:41:11Z","message":"kubelet has no disk pressure","reason":"KubeletHasNoDiskPressure","status":"False","type":"DiskPressure","lastHeartbeatTime":"2023-10-12T13:03:59Z"},{"message":"kubelet has sufficient PID available","reason":"KubeletHasSufficientPID","status":"False","type":"PIDPressure","lastHeartbeatTime":"2023-10-12T13:03:59Z","lastTransitionTime":"2023-09-18T02:41:11Z"},{"type":"Ready","lastHeartbeatTime":"2023-10-12T13:03:59Z","lastTransitionTime":"2023-09-18T02:41:31Z","message":"kubelet is posting ready status","reason":"KubeletReady","status":"True"}]},"metadata":{"name":"10.0.4.4","resourceVersion":"10118782449","creationTimestamp":"2023-09-18T02:41:11Z","labels":{"topology.kubernetes.io/zone":"100004","kubernetes.io/os":"linux","cloud.tencent.com/node-instance-id":"ins-81nylka2","eng-cstate-86fabc2b-6":"1","beta.kubernetes.io/arch":"amd64","failure-domain.beta.kubernetes.io/zone":"100004","node.kubernetes.io/instance-type":"SA2.8XLARGE64","topology.kubernetes.io/region":"gz","kubernetes.io/arch":"amd64","tke.cloud.tencent.com/cbs-mountable":"true","cloud.tencent.com/auto-scaling-group-id":"asg-2asi3wuq","beta.kubernetes.io/instance-type":"SA2.8XLARGE64","failure-domain.beta.kubernetes.io/region":"gz","beta.kubernetes.io/os":"linux","tke.cloud.tencent.com/nodepool-id":"np-2w3daqz8","kubernetes.io/hostname":"10.0.4.4","topology.com.tencent.cloud.csi.cbs/zone":"ap-guangzhou-4"}},"spec":{}},"_id":"6507b8c72299083f14c9b8c6"},{"_id":"6507b8c42299083f14c9ac5e","data":{"spec":{},"status":{"conditions":[{"status":"False","type":"NetworkUnavailable","lastHeartbeatTime":"2023-09-18T02:41:14Z","lastTransitionTime":"2023-09-18T02:41:14Z","message":"RouteController created a route","reason":"RouteCreated"},{"lastHeartbeatTime":"2023-10-13T02:54:09Z","lastTransitionTime":"2023-09-18T02:41:08Z","message":"kubelet has sufficient memory available","reason":"KubeletHasSufficientMemory","status":"False","type":"MemoryPressure"},{"status":"False","type":"DiskPressure","lastHeartbeatTime":"2023-10-13T02:54:09Z","lastTransitionTime":"2023-09-18T02:41:08Z","message":"kubelet has no disk pressure","reason":"KubeletHasNoDiskPressure"},{"status":"False","type":"PIDPressure","lastHeartbeatTime":"2023-10-13T02:54:09Z","lastTransitionTime":"2023-09-18T02:41:08Z","message":"kubelet has sufficient PID available","reason":"KubeletHasSufficientPID"},{"lastTransitionTime":"2023-09-18T02:41:58Z","message":"kubelet is posting ready status","reason":"KubeletReady","status":"True","type":"Ready","lastHeartbeatTime":"2023-10-13T02:54:09Z"}],"addresses":[{"address":"10.0.4.7","type":"InternalIP"},{"address":"10.0.4.7","type":"Hostname"}]},"metadata":{"creationTimestamp":"2023-09-18T02:41:08Z","labels":{"beta.kubernetes.io/arch":"amd64","failure-domain.beta.kubernetes.io/zone":"100004","kubernetes.io/os":"linux","node.kubernetes.io/instance-type":"SA2.8XLARGE64","topology.kubernetes.io/zone":"100004","cloud.tencent.com/node-instance-id":"ins-d7281exs","tke.cloud.tencent.com/nodepool-id":"np-2w3daqz8","beta.kubernetes.io/os":"linux","cloud.tencent.com/auto-scaling-group-id":"asg-2asi3wuq","beta.kubernetes.io/instance-type":"SA2.8XLARGE64","kubernetes.io/arch":"amd64","topology.kubernetes.io/region":"gz","tke.cloud.tencent.com/cbs-mountable":"true","eng-cstate-86fabc2b-6":"1","kubernetes.io/hostname":"10.0.4.7","failure-domain.beta.kubernetes.io/region":"gz","topology.com.tencent.cloud.csi.cbs/zone":"ap-guangzhou-4"},"name":"10.0.4.7","resourceVersion":"10149536708"}}},{"_id":"646e19362299083f144a6883","data":{"status":{"conditions":[{"lastTransitionTime":"2023-05-23T11:29:30Z","message":"RouteController created a route","reason":"RouteCreated","status":"False","type":"NetworkUnavailable","lastHeartbeatTime":"2023-05-23T11:29:30Z"},{"message":"kubelet has sufficient memory available","reason":"KubeletHasSufficientMemory","status":"False","type":"MemoryPressure","lastHeartbeatTime":"2023-10-12T06:45:11Z","lastTransitionTime":"2023-08-14T07:23:01Z"},{"type":"DiskPressure","lastHeartbeatTime":"2023-10-12T06:45:11Z","lastTransitionTime":"2023-08-14T07:23:01Z","message":"kubelet has no disk pressure","reason":"KubeletHasNoDiskPressure","status":"False"},{"reason":"KubeletHasSufficientPID","status":"False","type":"PIDPressure","lastHeartbeatTime":"2023-10-12T06:45:11Z","lastTransitionTime":"2023-08-14T07:23:01Z","message":"kubelet has sufficient PID available"},{"message":"kubelet is posting ready status","reason":"KubeletReady","status":"True","type":"Ready","lastHeartbeatTime":"2023-10-12T06:45:11Z","lastTransitionTime":"2023-08-14T07:23:01Z"}],"addresses":[{"type":"InternalIP","address":"10.0.6.23"},{"address":"10.0.6.23","type":"Hostname"}]},"metadata":{"labels":{"node.kubernetes.io/instance-type":"SA3.MEDIUM2","eng-cstate-3e8191b0-5":"1","beta.kubernetes.io/os":"linux","topology.kubernetes.io/region":"gz","topology.kubernetes.io/zone":"100006","kubernetes.io/os":"linux","failure-domain.beta.kubernetes.io/region":"gz","beta.kubernetes.io/arch":"amd64","eng-cstate-22817e80-4":"1","eng-cstate-b59d451c-3":"1","topology.com.tencent.cloud.csi.cbs/zone":"ap-guangzhou-6","beta.kubernetes.io/instance-type":"SA3.MEDIUM2","ingress-nginx":"true","dedicated":"ingress-nginx","kubernetes.io/hostname":"10.0.6.23","cloud.tencent.com/node-instance-id":"ins-qcc4u546","kubernetes.io/arch":"amd64","failure-domain.beta.kubernetes.io/zone":"100006","eng-cstate-7fca4c84-1":"1","eng-cstate-86fabc2b-6":"1","eng-cstate-12616088-2":"1"},"name":"10.0.6.23","resourceVersion":"10104758232","creationTimestamp":"2023-05-23T11:29:27Z"},"spec":{"taints":[{"effect":"PreferNoSchedule","key":"dedicated","value":"ingress-nginx"},{"timeAdded":"2023-09-27T09:58:05Z","effect":"NoSchedule","key":"node.kubernetes.io/unschedulable"}],"unschedulable":true}}},{"_id":"64cb4ab72299083f14c3e3cb","data":{"spec":{},"status":{"conditions":[{"reason":"RouteCreated","status":"False","type":"NetworkUnavailable","lastHeartbeatTime":"2023-08-03T06:35:43Z","lastTransitionTime":"2023-08-03T06:35:43Z","message":"RouteController created a route"},{"lastHeartbeatTime":"2023-10-11T23:07:55Z","lastTransitionTime":"2023-08-03T06:35:35Z","message":"kubelet has sufficient memory available","reason":"KubeletHasSufficientMemory","status":"False","type":"MemoryPressure"},{"lastHeartbeatTime":"2023-10-11T23:07:55Z","lastTransitionTime":"2023-08-03T06:35:35Z","message":"kubelet has no disk pressure","reason":"KubeletHasNoDiskPressure","status":"False","type":"DiskPressure"},{"status":"False","type":"PIDPressure","lastHeartbeatTime":"2023-10-11T23:07:55Z","lastTransitionTime":"2023-08-03T06:35:35Z","message":"kubelet has sufficient PID available","reason":"KubeletHasSufficientPID"},{"lastHeartbeatTime":"2023-10-11T23:07:55Z","lastTransitionTime":"2023-08-03T06:36:45Z","message":"kubelet is posting ready status","reason":"KubeletReady","status":"True","type":"Ready"}],"addresses":[{"type":"InternalIP","address":"10.0.6.27"},{"type":"Hostname","address":"10.0.6.27"}]},"metadata":{"creationTimestamp":"2023-08-03T06:35:35Z","labels":{"cloud.tencent.com/auto-scaling-group-id":"asg-2asi3wuq","beta.kubernetes.io/os":"linux","topology.kubernetes.io/zone":"100006","failure-domain.beta.kubernetes.io/zone":"100006","kubernetes.io/os":"linux","kubernetes.io/arch":"amd64","eng-cstate-22817e80-4":"1","topology.com.tencent.cloud.csi.cbs/zone":"ap-guangzhou-6","node.kubernetes.io/instance-type":"SA3.8XLARGE64","beta.kubernetes.io/instance-type":"SA3.8XLARGE64","kubernetes.io/hostname":"10.0.6.27","tke.cloud.tencent.com/nodepool-id":"np-2w3daqz8","failure-domain.beta.kubernetes.io/region":"gz","topology.kubernetes.io/region":"gz","eng-cstate-3e8191b0-5":"1","beta.kubernetes.io/arch":"amd64","cloud.tencent.com/node-instance-id":"ins-6sql5pzg","eng-cstate-86fabc2b-6":"1","tke.cloud.tencent.com/cbs-mountable":"true"},"name":"10.0.6.27","resourceVersion":"10087849407"}}},{"_id":"646e19362299083f144a66ba","data":{"metadata":{"resourceVersion":"10105454532","creationTimestamp":"2023-05-23T05:44:32Z","labels":{"kubernetes.io/os":"linux","failure-domain.beta.kubernetes.io/zone":"100006","eng-cstate-b59d451c-3":"1","eng-cstate-3e8191b0-5":"1","cloud.tencent.com/auto-scaling-group-id":"asg-1r49l84c","topology.com.tencent.cloud.csi.cbs/zone":"ap-guangzhou-6","eng-cstate-22817e80-4":"1","tke.cloud.tencent.com/nodepool-id":"np-nckqef5i","topology.kubernetes.io/zone":"100006","beta.kubernetes.io/instance-type":"SA3.4XLARGE32","eng-cstate-86fabc2b-6":"1","cloud.tencent.com/node-instance-id":"ins-mangzswa","beta.kubernetes.io/os":"linux","eng-cstate-12616088-2":"1","kubernetes.io/arch":"amd64","failure-domain.beta.kubernetes.io/region":"gz","dedicated":"bkSaaS","eng-cstate-7fca4c84-1":"1","node.kubernetes.io/instance-type":"SA3.4XLARGE32","beta.kubernetes.io/arch":"amd64","kubernetes.io/hostname":"10.0.6.29","topology.kubernetes.io/region":"gz"},"name":"10.0.6.29"},"spec":{"taints":[{"key":"dedicated","value":"bkSaaS","effect":"NoSchedule"}]},"status":{"conditions":[{"lastHeartbeatTime":"2023-05-23T05:44:40Z","lastTransitionTime":"2023-05-23T05:44:40Z","message":"RouteController created a route","reason":"RouteCreated","status":"False","type":"NetworkUnavailable"},{"status":"False","type":"MemoryPressure","lastHeartbeatTime":"2023-10-12T07:04:04Z","lastTransitionTime":"2023-05-23T05:44:31Z","message":"kubelet has sufficient memory available","reason":"KubeletHasSufficientMemory"},{"status":"False","type":"DiskPressure","lastHeartbeatTime":"2023-10-12T07:04:04Z","lastTransitionTime":"2023-09-18T02:56:01Z","message":"kubelet has no disk pressure","reason":"KubeletHasNoDiskPressure"},{"type":"PIDPressure","lastHeartbeatTime":"2023-10-12T07:04:04Z","lastTransitionTime":"2023-05-23T05:44:31Z","message":"kubelet has sufficient PID available","reason":"KubeletHasSufficientPID","status":"False"},{"reason":"KubeletReady","status":"True","type":"Ready","lastHeartbeatTime":"2023-10-12T07:04:04Z","lastTransitionTime":"2023-05-23T05:45:22Z","message":"kubelet is posting ready status"}],"addresses":[{"type":"InternalIP","address":"10.0.6.29"},{"address":"10.0.6.29","type":"Hostname"}]}}},{"_id":"6476bf1d2299083f14c034c3","data":{"spec":{},"status":{"conditions":[{"type":"NetworkUnavailable","lastHeartbeatTime":"2023-05-31T03:29:39Z","lastTransitionTime":"2023-05-31T03:29:39Z","message":"RouteController created a route","reason":"RouteCreated","status":"False"},{"lastHeartbeatTime":"2023-10-13T02:54:18Z","lastTransitionTime":"2023-09-20T00:28:00Z","message":"kubelet has sufficient memory available","reason":"KubeletHasSufficientMemory","status":"False","type":"MemoryPressure"},{"lastHeartbeatTime":"2023-10-13T02:54:18Z","lastTransitionTime":"2023-10-11T04:06:44Z","message":"kubelet has no disk pressure","reason":"KubeletHasNoDiskPressure","status":"False","type":"DiskPressure"},{"lastTransitionTime":"2023-09-20T00:28:00Z","message":"kubelet has sufficient PID available","reason":"KubeletHasSufficientPID","status":"False","type":"PIDPressure","lastHeartbeatTime":"2023-10-13T02:54:18Z"},{"status":"True","type":"Ready","lastHeartbeatTime":"2023-10-13T02:54:18Z","lastTransitionTime":"2023-09-20T00:28:00Z","message":"kubelet is posting ready status","reason":"KubeletReady"}],"addresses":[{"address":"10.0.6.35","type":"InternalIP"},{"address":"10.0.6.35","type":"Hostname"}]},"metadata":{"resourceVersion":"10149541700","creationTimestamp":"2023-05-31T03:29:33Z","labels":{"eng-cstate-b59d451c-3":"1","eng-cstate-22817e80-4":"1","tke.cloud.tencent.com/nodepool-id":"np-2w3daqz8","failure-domain.beta.kubernetes.io/region":"gz","beta.kubernetes.io/instance-type":"SA3.8XLARGE64","cloud.tencent.com/node-instance-id":"ins-pp90sjjc","topology.com.tencent.cloud.csi.cbs/zone":"ap-guangzhou-6","topology.kubernetes.io/zone":"100006","eng-cstate-12616088-2":"1","cloud.tencent.com/auto-scaling-group-id":"asg-2asi3wuq","kubernetes.io/hostname":"10.0.6.35","failure-domain.beta.kubernetes.io/zone":"100006","eng-cstate-86fabc2b-6":"1","beta.kubernetes.io/arch":"amd64","node.kubernetes.io/instance-type":"SA3.8XLARGE64","beta.kubernetes.io/os":"linux","kubernetes.io/arch":"amd64","kubernetes.io/os":"linux","topology.kubernetes.io/region":"gz","eng-cstate-3e8191b0-5":"1"},"name":"10.0.6.35"}}},{"_id":"646e19362299083f144a67ae","data":{"spec":{"taints":[{"effect":"NoSchedule","key":"dedicated","value":"bkSaaS"}]},"status":{"addresses":[{"address":"10.0.6.37","type":"InternalIP"},{"address":"10.0.6.37","type":"Hostname"}],"conditions":[{"lastHeartbeatTime":"2023-05-23T05:44:40Z","lastTransitionTime":"2023-05-23T05:44:40Z","message":"RouteController created a route","reason":"RouteCreated","status":"False","type":"NetworkUnavailable"},{"status":"False","type":"MemoryPressure","lastHeartbeatTime":"2023-10-12T07:04:05Z","lastTransitionTime":"2023-05-23T05:44:29Z","message":"kubelet has sufficient memory available","reason":"KubeletHasSufficientMemory"},{"message":"kubelet has no disk pressure","reason":"KubeletHasNoDiskPressure","status":"False","type":"DiskPressure","lastHeartbeatTime":"2023-10-12T07:04:05Z","lastTransitionTime":"2023-09-18T07:40:25Z"},{"lastHeartbeatTime":"2023-10-12T07:04:05Z","lastTransitionTime":"2023-05-23T05:44:29Z","message":"kubelet has sufficient PID available","reason":"KubeletHasSufficientPID","status":"False","type":"PIDPressure"},{"status":"True","type":"Ready","lastHeartbeatTime":"2023-10-12T07:04:05Z","lastTransitionTime":"2023-05-23T05:45:19Z","message":"kubelet is posting ready status","reason":"KubeletReady"}]},"metadata":{"creationTimestamp":"2023-05-23T05:44:29Z","labels":{"node.kubernetes.io/instance-type":"SA3.4XLARGE32","beta.kubernetes.io/arch":"amd64","cloud.tencent.com/node-instance-id":"ins-3yvkivri","eng-cstate-7fca4c84-1":"1","failure-domain.beta.kubernetes.io/zone":"100006","eng-cstate-3e8191b0-5":"1","topology.kubernetes.io/region":"gz","kubernetes.io/hostname":"10.0.6.37","tke.cloud.tencent.com/nodepool-id":"np-nckqef5i","beta.kubernetes.io/os":"linux","eng-cstate-b59d451c-3":"1","failure-domain.beta.kubernetes.io/region":"gz","beta.kubernetes.io/instance-type":"SA3.4XLARGE32","dedicated":"bkSaaS","cloud.tencent.com/auto-scaling-group-id":"asg-1r49l84c","kubernetes.io/arch":"amd64","eng-cstate-12616088-2":"1","eng-cstate-86fabc2b-6":"1","kubernetes.io/os":"linux","topology.com.tencent.cloud.csi.cbs/zone":"ap-guangzhou-6","eng-cstate-22817e80-4":"1","topology.kubernetes.io/zone":"100006"},"name":"10.0.6.37","resourceVersion":"10105455563"}}},{"_id":"6476c9332299083f14c74035","data":{"metadata":{"labels":{"eng-cstate-b59d451c-3":"1","topology.kubernetes.io/region":"gz","kubernetes.io/os":"linux","tke.cloud.tencent.com/nodepool-id":"np-2w3daqz8","eng-cstate-3e8191b0-5":"1","topology.kubernetes.io/zone":"100006","kubernetes.io/arch":"amd64","beta.kubernetes.io/arch":"amd64","beta.kubernetes.io/instance-type":"SA3.8XLARGE64","cloud.tencent.com/node-instance-id":"ins-bzo7g2te","failure-domain.beta.kubernetes.io/region":"gz","failure-domain.beta.kubernetes.io/zone":"100006","topology.com.tencent.cloud.csi.cbs/zone":"ap-guangzhou-6","eng-cstate-12616088-2":"1","eng-cstate-86fabc2b-6":"1","node.kubernetes.io/instance-type":"SA3.8XLARGE64","beta.kubernetes.io/os":"linux","eng-cstate-22817e80-4":"1","cloud.tencent.com/auto-scaling-group-id":"asg-2asi3wuq","kubernetes.io/hostname":"10.0.6.40"},"name":"10.0.6.40","resourceVersion":"10087828804","creationTimestamp":"2023-05-31T04:12:35Z"},"spec":{},"status":{"conditions":[{"reason":"RouteCreated","status":"False","type":"NetworkUnavailable","lastHeartbeatTime":"2023-05-31T04:12:39Z","lastTransitionTime":"2023-05-31T04:12:39Z","message":"RouteController created a route"},{"type":"MemoryPressure","lastHeartbeatTime":"2023-10-11T23:07:22Z","lastTransitionTime":"2023-05-31T04:12:35Z","message":"kubelet has sufficient memory available","reason":"KubeletHasSufficientMemory","status":"False"},{"lastTransitionTime":"2023-05-31T04:12:35Z","message":"kubelet has no disk pressure","reason":"KubeletHasNoDiskPressure","status":"False","type":"DiskPressure","lastHeartbeatTime":"2023-10-11T23:07:22Z"},{"status":"False","type":"PIDPressure","lastHeartbeatTime":"2023-10-11T23:07:22Z","lastTransitionTime":"2023-05-31T04:12:35Z","message":"kubelet has sufficient PID available","reason":"KubeletHasSufficientPID"},{"type":"Ready","lastHeartbeatTime":"2023-10-11T23:07:22Z","lastTransitionTime":"2023-05-31T04:13:25Z","message":"kubelet is posting ready status","reason":"KubeletReady","status":"True"}],"addresses":[{"type":"InternalIP","address":"10.0.6.40"},{"address":"10.0.6.40","type":"Hostname"}]}}},{"data":{"status":{"addresses":[{"address":"10.0.7.22","type":"InternalIP"},{"address":"10.0.7.22","type":"Hostname"}],"conditions":[{"status":"False","type":"NetworkUnavailable","lastHeartbeatTime":"2023-06-15T14:08:57Z","lastTransitionTime":"2023-06-15T14:08:57Z","message":"RouteController created a route","reason":"RouteCreated"},{"type":"MemoryPressure","lastHeartbeatTime":"2023-10-11T23:07:36Z","lastTransitionTime":"2023-06-15T14:08:53Z","message":"kubelet has sufficient memory available","reason":"KubeletHasSufficientMemory","status":"False"},{"lastTransitionTime":"2023-08-23T08:34:31Z","message":"kubelet has no disk pressure","reason":"KubeletHasNoDiskPressure","status":"False","type":"DiskPressure","lastHeartbeatTime":"2023-10-11T23:07:36Z"},{"status":"False","type":"PIDPressure","lastHeartbeatTime":"2023-10-11T23:07:36Z","lastTransitionTime":"2023-06-15T14:08:53Z","message":"kubelet has sufficient PID available","reason":"KubeletHasSufficientPID"},{"type":"Ready","lastHeartbeatTime":"2023-10-11T23:07:36Z","lastTransitionTime":"2023-06-15T14:09:43Z","message":"kubelet is posting ready status","reason":"KubeletReady","status":"True"}]},"metadata":{"creationTimestamp":"2023-06-15T14:08:53Z","labels":{"topology.kubernetes.io/zone":"100007","eng-cstate-b59d451c-3":"1","failure-domain.beta.kubernetes.io/region":"gz","eng-cstate-3e8191b0-5":"1","beta.kubernetes.io/instance-type":"SA3.8XLARGE64","eng-cstate-22817e80-4":"1","kubernetes.io/hostname":"10.0.7.22","failure-domain.beta.kubernetes.io/zone":"100007","beta.kubernetes.io/arch":"amd64","tke.cloud.tencent.com/nodepool-id":"np-2w3daqz8","topology.com.tencent.cloud.csi.cbs/zone":"ap-guangzhou-7","eng-cstate-86fabc2b-6":"1","cloud.tencent.com/auto-scaling-group-id":"asg-2asi3wuq","node.kubernetes.io/instance-type":"SA3.8XLARGE64","topology.kubernetes.io/region":"gz","kubernetes.io/arch":"amd64","cloud.tencent.com/node-instance-id":"ins-52iupors","beta.kubernetes.io/os":"linux","kubernetes.io/os":"linux"},"name":"10.0.7.22","resourceVersion":"10087837446"},"spec":{}},"_id":"648b1c122299083f144cc208"},{"_id":"648b1a7b2299083f144c0662","data":{"metadata":{"creationTimestamp":"2023-06-15T14:04:43Z","labels":{"failure-domain.beta.kubernetes.io/zone":"100007","kubernetes.io/arch":"amd64","beta.kubernetes.io/os":"linux","eng-cstate-22817e80-4":"1","topology.kubernetes.io/zone":"100007","tke.cloud.tencent.com/nodepool-id":"np-2w3daqz8","topology.kubernetes.io/region":"gz","node.kubernetes.io/instance-type":"SA3.8XLARGE64","eng-cstate-86fabc2b-6":"1","topology.com.tencent.cloud.csi.cbs/zone":"ap-guangzhou-7","eng-cstate-3e8191b0-5":"1","kubernetes.io/os":"linux","kubernetes.io/hostname":"10.0.7.35","failure-domain.beta.kubernetes.io/region":"gz","cloud.tencent.com/auto-scaling-group-id":"asg-2asi3wuq","cloud.tencent.com/node-instance-id":"ins-azq2fw9s","beta.kubernetes.io/arch":"amd64","beta.kubernetes.io/instance-type":"SA3.8XLARGE64","eng-cstate-b59d451c-3":"1"},"name":"10.0.7.35","resourceVersion":"10149531685"},"spec":{},"status":{"addresses":[{"address":"10.0.7.35","type":"InternalIP"},{"type":"Hostname","address":"10.0.7.35"}],"conditions":[{"reason":"RouteCreated","status":"False","type":"NetworkUnavailable","lastHeartbeatTime":"2023-06-15T14:04:47Z","lastTransitionTime":"2023-06-15T14:04:47Z","message":"RouteController created a route"},{"lastTransitionTime":"2023-06-15T14:04:43Z","message":"kubelet has sufficient memory available","reason":"KubeletHasSufficientMemory","status":"False","type":"MemoryPressure","lastHeartbeatTime":"2023-10-13T02:54:01Z"},{"reason":"KubeletHasNoDiskPressure","status":"False","type":"DiskPressure","lastHeartbeatTime":"2023-10-13T02:54:01Z","lastTransitionTime":"2023-09-12T12:52:18Z","message":"kubelet has no disk pressure"},{"reason":"KubeletHasSufficientPID","status":"False","type":"PIDPressure","lastHeartbeatTime":"2023-10-13T02:54:01Z","lastTransitionTime":"2023-06-15T14:04:43Z","message":"kubelet has sufficient PID available"},{"message":"kubelet is posting ready status","reason":"KubeletReady","status":"True","type":"Ready","lastHeartbeatTime":"2023-10-13T02:54:01Z","lastTransitionTime":"2023-06-15T14:05:33Z"}]}}},{"_id":"6481ad5e2299083f14f1abf9","data":{"metadata":{"labels":{"eng-cstate-b59d451c-3":"1","eng-cstate-86fabc2b-6":"1","kubernetes.io/hostname":"10.0.7.37","kubernetes.io/os":"linux","tke.cloud.tencent.com/nodepool-id":"np-2w3daqz8","beta.kubernetes.io/instance-type":"SA3.8XLARGE64","beta.kubernetes.io/arch":"amd64","failure-domain.beta.kubernetes.io/region":"gz","kubernetes.io/arch":"amd64","eng-cstate-3e8191b0-5":"1","cloud.tencent.com/auto-scaling-group-id":"asg-2asi3wuq","eng-cstate-22817e80-4":"1","topology.kubernetes.io/region":"gz","cloud.tencent.com/node-instance-id":"ins-c61t6vhq","beta.kubernetes.io/os":"linux","node.kubernetes.io/instance-type":"SA3.8XLARGE64","topology.com.tencent.cloud.csi.cbs/zone":"ap-guangzhou-7","topology.kubernetes.io/zone":"100007","failure-domain.beta.kubernetes.io/zone":"100007"},"name":"10.0.7.37","resourceVersion":"10117991859","creationTimestamp":"2023-06-08T10:28:46Z"},"spec":{},"status":{"conditions":[{"status":"False","type":"NetworkUnavailable","lastHeartbeatTime":"2023-06-08T10:28:51Z","lastTransitionTime":"2023-06-08T10:28:51Z","message":"RouteController created a route","reason":"RouteCreated"},{"lastTransitionTime":"2023-08-09T20:03:38Z","message":"kubelet has sufficient memory available","reason":"KubeletHasSufficientMemory","status":"False","type":"MemoryPressure","lastHeartbeatTime":"2023-10-12T12:42:36Z"},{"status":"False","type":"DiskPressure","lastHeartbeatTime":"2023-10-12T12:42:36Z","lastTransitionTime":"2023-09-15T10:20:39Z","message":"kubelet has no disk pressure","reason":"KubeletHasNoDiskPressure"},{"message":"kubelet has sufficient PID available","reason":"KubeletHasSufficientPID","status":"False","type":"PIDPressure","lastHeartbeatTime":"2023-10-12T12:42:36Z","lastTransitionTime":"2023-08-09T20:03:38Z"},{"type":"Ready","lastHeartbeatTime":"2023-10-12T12:42:36Z","lastTransitionTime":"2023-08-09T20:03:38Z","message":"kubelet is posting ready status","reason":"KubeletReady","status":"True"}],"addresses":[{"type":"InternalIP","address":"10.0.7.37"},{"address":"10.0.7.37","type":"Hostname"}]}}}]}`)
	//} else {
	//	body = []byte(`{"result":true,"code":0,"message":"success","data":[{"_id":"646e19362299083f144a67ae","data":{"subsets":[{"addresses":[{"nodeName":"10.0.7.37"}],"ports":["1","2","3"]},{"addresses":[{"nodeName":"10.0.7.37"}],"ports":["12","22"]},{"addresses":[{"nodeName":"10.0.3.5"}],"ports":["1","2","3"]}]}}]}`)
	//}
	var result FetchBcsStorageResp
	err = jsonx.Unmarshal(body, &result)
	if err != nil {
		return nil, err
	}
	if result.Code != 0 {
		return nil, errors.New(fmt.Sprintf("fetch bcs storage failed, %s", result.Message))
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

// 通过IP查询主机信息
func (BcsClusterInfoSvc) getHostByIp(ipList []string, bkCloudId int) ([]cmdb.ListBizHostsTopoDataInfo, error) {
	cmdbApi, err := api.GetCmdbApi()
	if err != nil {
		return nil, err
	}
	params := processParams(bkCloudId, ipList)
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

	count, err := bcs.NewBCSClusterInfoQuerySet(mysql.GetDBSession().DB).ClusterIDEq(clusterId).Count()
	if err != nil {
		return nil, err
	}
	// 集群已经接入
	if count != 0 {
		return nil, errors.New(
			fmt.Sprintf("failed to register cluster_id [%s] under project_id [%s] for cluster is already register, nothing will do any more", clusterId, projectId),
		)
	}

	bkEnv := viper.GetString(BcsClusterBkEnvLabelPath)
	cluster := bcs.BCSClusterInfo{
		ClusterID:         clusterId,
		BCSApiClusterId:   clusterId,
		BkBizId:           int(bkBizIdInt),
		ProjectId:         projectId,
		DomainName:        viper.GetString(api.BkApiBcsApiGatewayHostPath),
		Port:              viper.GetUint(api.BkApiBcsApiGatewayPortPath),
		ServerAddressPath: "clusters",
		ApiKeyType:        "authorization",
		ApiKeyContent:     viper.GetString(api.BkApiBcsApiGatewayTokenPath),
		ApiKeyPrefix:      "Bearer",
		Status:            models.BcsClusterStatusRunning,
		IsSkipSslVerify:   true,
		BkEnv:             &bkEnv,
		Creator:           creator,
		LastModifyUser:    creator,
	}

	if err := cluster.Create(mysql.GetDBSession().DB); err != nil {
		return nil, err
	}
	logger.Infof("cluster [%s] create database record success", cluster.ClusterID)
	// 注册6个必要的data_id和自定义事件及自定义时序上报内容
	for usage, register := range bcsDatasourceRegisterInfo {
		// 注册data_id
		datasource, err := NewBcsClusterInfoSvc(&cluster).CreateDataSource(usage, register.EtlConfig, creator, viper.GetUint(BcsKafkaStorageClusterIdPath), "default")
		if err != nil {
			return nil, err
		}
		logger.Infof("cluster [%s] usage [%s] is register datasource [%v] success.", cluster.ClusterID, usage, datasource.BkDataId)
		// 注册自定义时序 或 自定义事件
		var defaultStorageConfig map[string]interface{}
		var additionalOptions map[string][]string
		if register.Usage == "metric" {
			// 如果是指标的类型，需要考虑增加influxdb proxy的集群隔离配置
			defaultStorageConfig = map[string]interface{}{"proxy_cluster_name": viper.GetString(BcsInfluxdbDefaultProxyClusterNameForK8s)}
			additionalOptions = map[string][]string{models.OptionCustomReportDimensionValues: bcs.DefaultServiceMonitorDimensionTerm}
		} else {
			defaultStorageConfig = map[string]interface{}{"cluster_id": viper.GetUint(BcsCustomEventStorageClusterId)}
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
	if err := cluster.Update(mysql.GetDBSession().DB, bcs.BCSClusterInfoDBSchema.K8sMetricDataID, bcs.BCSClusterInfoDBSchema.CustomMetricDataID,
		bcs.BCSClusterInfoDBSchema.K8sEventDataID); err != nil {
		return nil, err
	}
	if err != nil {
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
		viper.GetString(api.BkApiAppCodePath),
	)
	if err != nil {
		return nil, err
	}
	logger.Infof("data_source [%s] is create by etl_config [%s] for cluster_id [%s]", dataSource.BkDataId, etlConfig, b.ClusterID)
	return dataSource, nil
}

func isIPv6(ip string) bool {
	parsedIp := net.ParseIP(ip)
	if parsedIp != nil && parsedIp.To4() == nil {
		return true
	}
	return false
}

func processParams(bkBizID int, ips []string) map[string]interface{} {
	conditions := []map[string]interface{}{}
	ipv6IPs := []string{}
	ipv4IPs := []string{}

	for _, ip := range ips {
		if isIPv6(ip) {
			ipv6IPs = append(ipv6IPs, ip)
		} else {
			ipv4IPs = append(ipv4IPs, ip)
		}
	}

	ipv4Condition := map[string]interface{}{
		"condition": "AND",
		"rules": []map[string]interface{}{
			{"field": "bk_host_innerip", "operator": "in", "value": ipv4IPs},
		},
	}
	ipv6Condition := map[string]interface{}{
		"condition": "AND",
		"rules": []map[string]interface{}{
			{"field": "bk_host_innerip_v6", "operator": "in", "value": ipv6IPs},
		},
	}

	if len(ipv4IPs) > 0 {
		conditions = append(conditions, ipv4Condition)
	}
	if len(ipv6IPs) > 0 {
		conditions = append(conditions, ipv6Condition)
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
			return errors.Wrap(err, fmt.Sprintf("ensure data id resource error, %s", err))
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
			return errors.Wrap(err, fmt.Sprintf("update resource %s failed, %v", name, err))
		}
	} else {
		_, err = b.CreateK8sResource(models.BcsResourceGroupName, models.BcsResourceVersion, models.BcsResourceDataIdResourcePlural, config)
		if err != nil {
			return errors.Wrap(err, fmt.Sprintf("create resource %s failed, %v", name, err))
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
	config := &rest.Config{
		Host:        fmt.Sprintf("%s://%s:%v/%s/%s", viper.GetString(api.BkApiBcsApiGatewaySchemaPath), b.DomainName, b.Port, b.ServerAddressPath, b.ClusterID),
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
	return viper.GetString(BcsClusterBkEnvLabelPath)
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
	Data []FetchBcsStorageRespData `json:"data"`
}

type FetchBcsStorageRespData struct {
	Id   string   `json:"_id"`
	Data NodeInfo `json:"data"`
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

func (k KubernetesNodeJsonParser) GetEndpointsCount(endpoints []FetchBcsStorageRespData) int {
	var count = 0
	for _, endpoint := range endpoints {
		for _, subset := range endpoint.Data.Subsets {
			var addressCount int
			addressInterface, ok := subset["addresses"]
			if !ok {
				continue
			}
			addressList := addressInterface.([]interface{})
			for _, addressInterface := range addressList {
				address := addressInterface.(map[string]interface{})
				nodeName := address["nodeName"]
				if nodeName == nil {
					continue
				}
				if k.Name() == nodeName.(string) {
					addressCount += 1
				}
			}
			portsInterface, ok := subset["ports"]
			if !ok {
				continue
			}
			ports := portsInterface.([]interface{})
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

	for _, taintInterface := range taintsInterface.([]interface{}) {
		taint := taintInterface.(map[string]interface{})
		key, ok := taint["key"].(string)
		if !ok {
			key = ""
		}
		value, ok := taint["value"].(string)
		if !ok {
			value = ""
		}
		effect, ok := taint["effect"].(string)
		if !ok {
			effect = ""
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
