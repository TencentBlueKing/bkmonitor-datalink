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
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/apiservice"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/bcs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/customreport"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/hashconsul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

func TestBcsClusterInfoSvc_UpdateBcsClusterCloudIdConfig(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")

	gomonkey.ApplyMethod(&http.Client{}, "Do", func(t *http.Client, req *http.Request) (*http.Response, error) {
		var data string
		if strings.Contains(req.URL.Path, "list_biz_hosts_topo") {
			data = `{"result":true,"code":0,"data":{"count":3,"info":[{"host":{"bk_agent_id":"020000000052540078494116983065049641","bk_bak_operator":"admin","bk_cloud_id":99,"bk_comment":"","bk_host_id":54,"bk_host_innerip":"1.0.0.1","bk_host_innerip_v6":"","bk_host_name":"VM-6-23-centos","bk_host_outerip":"","bk_host_outerip_v6":"","bk_isp_name":null,"bk_os_name":"linux centos","bk_os_type":"1","bk_os_version":"7.8.2003","bk_province_name":null,"bk_state":null,"bk_state_name":null,"bk_supplier_account":"0","operator":"admin"},"topo":[{"bk_set_id":2,"bk_set_name":"idle pool","module":[{"bk_module_id":3,"bk_module_name":"idle host"}]}]},{"host":{"bk_agent_id":"02000000005254005e03fe1700488328477x","bk_bak_operator":"admin","bk_cloud_id":99,"bk_comment":"","bk_host_id":94,"bk_host_innerip":"1.0.0.2","bk_host_innerip_v6":"","bk_host_name":"VM-6-27-centos","bk_host_outerip":"","bk_host_outerip_v6":"","bk_isp_name":null,"bk_os_name":"linux centos","bk_os_type":"1","bk_os_version":"7.8.2003","bk_province_name":null,"bk_state":null,"bk_state_name":null,"bk_supplier_account":"0","operator":"admin"},"topo":[{"bk_set_id":2,"bk_set_name":"idle pool","module":[{"bk_module_id":3,"bk_module_name":"idle host"}]}]},{"host":{"bk_agent_id":"020000000052540045dba11700492467313n","bk_bak_operator":"admin","bk_cloud_id":0,"bk_comment":"","bk_host_id":95,"bk_host_innerip":"1.0.0.3","bk_host_innerip_v6":"","bk_host_name":"VM-6-29-centos","bk_host_outerip":"","bk_host_outerip_v6":"","bk_isp_name":null,"bk_os_name":"linux centos","bk_os_type":"1","bk_os_version":"7.8.2003","bk_province_name":null,"bk_state":null,"bk_state_name":null,"bk_supplier_account":"0","operator":"admin"},"topo":[{"bk_set_id":2,"bk_set_name":"idle pool","module":[{"bk_module_id":3,"bk_module_name":"idle host"}]}]}]},"message":"success","permission":null,"request_id":"55f8fc9b67fe4bc6a34c125c56911099"}`
		}
		if strings.Contains(req.URL.Path, "Node") {
			data = `{"message":"ok","result":true,"code":0,"data":[{"status":{"addresses":[{"address":"1.0.0.1","type":"InternalIP"},{"address":"1.0.0.1","type":"Hostname"}],"conditions":[{"lastHeartbeatTime":"2023-05-23T11:29:30Z","lastTransitionTime":"2023-05-23T11:29:30Z","message":"RouteController created a route","reason":"RouteCreated","status":"False","type":"NetworkUnavailable"},{"type":"MemoryPressure","lastHeartbeatTime":"2023-11-07T11:00:20Z","lastTransitionTime":"2023-08-14T07:23:01Z","message":"kubelet has sufficient memory available","reason":"KubeletHasSufficientMemory","status":"False"},{"message":"kubelet has no disk pressure","reason":"KubeletHasNoDiskPressure","status":"False","type":"DiskPressure","lastHeartbeatTime":"2023-11-07T11:00:20Z","lastTransitionTime":"2023-08-14T07:23:01Z"},{"status":"False","type":"PIDPressure","lastHeartbeatTime":"2023-11-07T11:00:20Z","lastTransitionTime":"2023-08-14T07:23:01Z","message":"kubelet has sufficient PID available","reason":"KubeletHasSufficientPID"},{"type":"Ready","lastHeartbeatTime":"2023-11-07T11:00:20Z","lastTransitionTime":"2023-08-14T07:23:01Z","message":"kubelet is posting ready status","reason":"KubeletReady","status":"True"}]},"metadata":{"labels":{"eng-cstate-7fca4c84-1":"1","kubernetes.io/arch":"amd64","topology.kubernetes.io/region":"gz","topology.com.tencent.cloud.csi.cbs/zone":"ap-guangzhou-6","beta.kubernetes.io/arch":"amd64","kubernetes.io/hostname":"1.0.0.1","beta.kubernetes.io/instance-type":"SA3.MEDIUM2","kubernetes.io/os":"linux","eng-cstate-22817e80-4":"1","eng-cstate-86fabc2b-6":"1","eng-cstate-3b9bc87a-7":"1","beta.kubernetes.io/os":"linux","dedicated":"ingress-nginx","topology.kubernetes.io/zone":"100006","eng-cstate-12616088-2":"1","failure-domain.beta.kubernetes.io/region":"gz","node.kubernetes.io/instance-type":"SA3.MEDIUM2","cloud.tencent.com/node-instance-id":"ins-qcc4u546","eng-cstate-3e8191b0-5":"1","ingress-nginx":"true","eng-cstate-b59d451c-3":"1","failure-domain.beta.kubernetes.io/zone":"100006"},"name":"1.0.0.1","resourceVersion":"11386901553","creationTimestamp":"2023-05-23T11:29:27Z"},"spec":{"unschedulable":true,"taints":[{"effect":"PreferNoSchedule","key":"dedicated","value":"ingress-nginx"},{"effect":"NoSchedule","key":"node.kubernetes.io/unschedulable","timeAdded":"2023-09-27T09:58:05Z"}]}},{"metadata":{"resourceVersion":"12191526357","creationTimestamp":"2023-11-20T14:59:56Z","labels":{"kubernetes.io/arch":"amd64","kubernetes.io/hostname":"1.0.0.2","topology.com.tencent.cloud.csi.cbs/zone":"ap-guangzhou-6","tke.cloud.tencent.com/cbs-mountable":"true","cloud.tencent.com/node-instance-id":"ins-olh8t1mu","tke.cloud.tencent.com/nodepool-id":"np-a72t3t12","failure-domain.beta.kubernetes.io/region":"gz","beta.kubernetes.io/instance-type":"SA3.8XLARGE64","kubernetes.io/os":"linux","beta.kubernetes.io/arch":"amd64","failure-domain.beta.kubernetes.io/zone":"100006","topology.kubernetes.io/region":"gz","topology.kubernetes.io/zone":"100006","cloud.tencent.com/auto-scaling-group-id":"asg-5ndk7a9g","node.kubernetes.io/instance-type":"SA3.8XLARGE64","beta.kubernetes.io/os":"linux"},"name":"1.0.0.2"},"spec":{},"status":{"addresses":[{"address":"1.0.0.2","type":"InternalIP"},{"type":"Hostname","address":"1.0.0.2"}],"conditions":[{"lastHeartbeatTime":"2023-11-20T15:00:04Z","lastTransitionTime":"2023-11-20T15:00:04Z","message":"RouteController created a route","reason":"RouteCreated","status":"False","type":"NetworkUnavailable"},{"type":"MemoryPressure","lastHeartbeatTime":"2023-11-24T03:16:59Z","lastTransitionTime":"2023-11-20T14:59:56Z","message":"kubelet has sufficient memory available","reason":"KubeletHasSufficientMemory","status":"False"},{"lastHeartbeatTime":"2023-11-24T03:16:59Z","lastTransitionTime":"2023-11-20T14:59:56Z","message":"kubelet has no disk pressure","reason":"KubeletHasNoDiskPressure","status":"False","type":"DiskPressure"},{"lastTransitionTime":"2023-11-20T14:59:56Z","message":"kubelet has sufficient PID available","reason":"KubeletHasSufficientPID","status":"False","type":"PIDPressure","lastHeartbeatTime":"2023-11-24T03:16:59Z"},{"lastTransitionTime":"2023-11-20T15:01:09Z","message":"kubelet is posting ready status","reason":"KubeletReady","status":"True","type":"Ready","lastHeartbeatTime":"2023-11-24T03:16:59Z"}]}},{"metadata":{"creationTimestamp":"2023-05-23T05:44:29Z","labels":{"kubernetes.io/hostname":"1.0.0.3","eng-cstate-22817e80-4":"1","eng-cstate-12616088-2":"1","dedicated":"bkSaaS","node.kubernetes.io/instance-type":"SA3.4XLARGE32","topology.kubernetes.io/region":"gz","beta.kubernetes.io/instance-type":"SA3.4XLARGE32","failure-domain.beta.kubernetes.io/region":"gz","tke.cloud.tencent.com/nodepool-id":"np-nckqef5i","cloud.tencent.com/auto-scaling-group-id":"asg-1r49l84c","eng-cstate-7fca4c84-1":"1","eng-cstate-b59d451c-3":"1","eng-cstate-3b9bc87a-7":"1","kubernetes.io/arch":"amd64","failure-domain.beta.kubernetes.io/zone":"100006","beta.kubernetes.io/arch":"amd64","beta.kubernetes.io/os":"linux","cloud.tencent.com/node-instance-id":"ins-3yvkivri","eng-cstate-3e8191b0-5":"1","topology.kubernetes.io/zone":"100006","kubernetes.io/os":"linux","topology.com.tencent.cloud.csi.cbs/zone":"ap-guangzhou-6","eng-cstate-86fabc2b-6":"1"},"name":"1.0.0.3","resourceVersion":"12091824029"},"spec":{"taints":[{"key":"dedicated","value":"bkSaaS","effect":"NoSchedule"}]},"status":{"addresses":[{"address":"1.0.0.3","type":"InternalIP"},{"address":"1.0.0.3","type":"Hostname"}],"conditions":[{"message":"RouteController created a route","reason":"RouteCreated","status":"False","type":"NetworkUnavailable","lastHeartbeatTime":"2023-05-23T05:44:40Z","lastTransitionTime":"2023-05-23T05:44:40Z"},{"message":"kubelet has sufficient memory available","reason":"KubeletHasSufficientMemory","status":"False","type":"MemoryPressure","lastHeartbeatTime":"2023-11-22T02:14:10Z","lastTransitionTime":"2023-11-04T07:48:28Z"},{"status":"False","type":"DiskPressure","lastHeartbeatTime":"2023-11-22T02:14:10Z","lastTransitionTime":"2023-11-10T08:41:43Z","message":"kubelet has no disk pressure","reason":"KubeletHasNoDiskPressure"},{"message":"kubelet has sufficient PID available","reason":"KubeletHasSufficientPID","status":"False","type":"PIDPressure","lastHeartbeatTime":"2023-11-22T02:14:10Z","lastTransitionTime":"2023-11-04T07:48:28Z"},{"lastTransitionTime":"2023-11-04T07:48:28Z","message":"kubelet is posting ready status","reason":"KubeletReady","status":"True","type":"Ready","lastHeartbeatTime":"2023-11-22T02:14:10Z"}]}}]}`
		}
		if strings.Contains(req.URL.Path, "Endpoints") {
			data = `{"message":"ok","result":true,"code":0,"data":[]}`
		}
		body := io.NopCloser(strings.NewReader(data))
		return &http.Response{
			Status:        "ok",
			StatusCode:    200,
			Body:          body,
			ContentLength: int64(len(data)),
			Request:       req,
		}, nil
	})
	db := mysql.GetDBSession().DB
	cluster := &bcs.BCSClusterInfo{
		ClusterID:          "BCS-K8S-00000",
		BCSApiClusterId:    "BCS-K8S-00000",
		BkBizId:            2,
		ProjectId:          "xxxxx",
		Status:             models.BcsClusterStatusRunning,
		DomainName:         "www.xxx.com",
		Port:               80,
		ServerAddressPath:  "clusters",
		ApiKeyType:         "authorization",
		ApiKeyContent:      "xxxxxx",
		ApiKeyPrefix:       "Bearer",
		IsSkipSslVerify:    true,
		K8sMetricDataID:    1572864,
		CustomMetricDataID: 1572865,
		K8sEventDataID:     1572866,
		Creator:            "system",
		CreateTime:         time.Now(),
		LastModifyTime:     time.Now(),
		LastModifyUser:     "system",
	}
	db.Delete(&cluster, "cluster_id = ?", cluster.ClusterID)
	err := cluster.Create(db)
	assert.NoError(t, err)
	err = NewBcsClusterInfoSvc(cluster).UpdateBcsClusterCloudIdConfig()
	assert.NoError(t, err)
	assert.Equal(t, 99, *cluster.BkCloudId)
}

func TestBcsClusterInfoSvc_isSameMapConfig(t *testing.T) {
	type fields struct {
		BCSClusterInfo *bcs.BCSClusterInfo
	}
	type args struct {
		source map[string]interface{}
		target map[string]interface{}
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   bool
	}{
		{
			name: "t1",
			fields: fields{
				BCSClusterInfo: nil,
			},
			args: args{
				source: map[string]interface{}{
					"a": 1,
					"c": map[string]interface{}{"1": "1", "2": "2"},
				},
				target: map[string]interface{}{
					"a": 1,
					"c": map[string]interface{}{"1": "1", "2": "2", "3": "3"},
				},
			},
			want: true,
		},
		{
			name: "t2",
			fields: fields{
				BCSClusterInfo: nil,
			},
			args: args{
				source: map[string]interface{}{
					"a": 1,
					"c": map[string]interface{}{"1": "0", "2": "2"},
				},
				target: map[string]interface{}{
					"a": 1,
					"c": map[string]interface{}{"1": "1", "2": "2", "3": "3"},
				},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := BcsClusterInfoSvc{
				BCSClusterInfo: tt.fields.BCSClusterInfo,
			}
			assert.Equalf(t, tt.want, b.isSameMapConfig(tt.args.source, tt.args.target), "isSameMapConfig(%v, %v)", tt.args.source, tt.args.target)
		})
	}
}

func TestBcsClusterInfoSvc_RefreshCommonResource(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	var createCount, updateCount int
	var data = []byte(`{"apiVersion":"monitoring.bk.tencent.com/v1beta1","items":[{"apiVersion":"monitoring.bk.tencent.com/v1beta1","kind":"DataID","metadata":{"creationTimestamp":"2023-07-19T10:40:03Z","generation":1,"labels":{"isCommon":"true","isSystem":"false","usage":"metric"},"managedFields":[{"apiVersion":"monitoring.bk.tencent.com/v1beta1","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:labels":{".":{},"f:isCommon":{},"f:isSystem":{},"f:usage":{}}},"f:spec":{".":{},"f:dataID":{},"f:dimensionReplace":{},"f:labels":{".":{},"f:bcs_cluster_id":{},"f:bk_biz_id":{}},"f:metricReplace":{}}},"manager":"OpenAPI-Generator","operation":"Update","time":"2023-07-19T10:40:03Z"}],"name":"custommetricdataid","resourceVersion":"5719372880","selfLink":"/apis/monitoring.bk.tencent.com/v1beta1/dataids/custommetricdataid","uid":"2f2f4b12-e63f-49d8-83e2-dd0d79f9fa16"},"spec":{"dataID":1572865,"dimensionReplace":{},"labels":{"bcs_cluster_id":"BCS-K8S-00000","bk_biz_id":"2"},"metricReplace":{}}},{"apiVersion":"monitoring.bk.tencent.com/v1beta1","kind":"DataID","metadata":{"creationTimestamp":"2023-07-19T10:40:04Z","generation":1,"labels":{"isCommon":"true","isSystem":"true","usage":"event"},"managedFields":[{"apiVersion":"monitoring.bk.tencent.com/v1beta1","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:labels":{".":{},"f:isCommon":{},"f:isSystem":{},"f:usage":{}}},"f:spec":{".":{},"f:dataID":{},"f:dimensionReplace":{},"f:labels":{".":{},"f:bcs_cluster_id":{},"f:bk_biz_id":{}},"f:metricReplace":{}}},"manager":"OpenAPI-Generator","operation":"Update","time":"2023-07-19T10:40:04Z"}],"name":"k8seventdataid","resourceVersion":"5719372903","selfLink":"/apis/monitoring.bk.tencent.com/v1beta1/dataids/k8seventdataid","uid":"33482264-805f-40a2-9487-84e15887797a"},"spec":{"dataID":1572866,"dimensionReplace":{},"labels":{"bcs_cluster_id":"BCS-K8S-00000","bk_biz_id":"2"},"metricReplace":{}}},{"apiVersion":"monitoring.bk.tencent.com/v1beta1","kind":"DataID","metadata":{"creationTimestamp":"2023-07-19T10:40:03Z","generation":1,"labels":{"isCommon":"true","isSystem":"true","usage":"metric"},"managedFields":[{"apiVersion":"monitoring.bk.tencent.com/v1beta1","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:labels":{".":{},"f:isCommon":{},"f:isSystem":{},"f:usage":{}}},"f:spec":{".":{},"f:dataID":{},"f:dimensionReplace":{},"f:labels":{".":{},"f:bcs_cluster_id":{},"f:bk_biz_id":{}},"f:metricReplace":{}}},"manager":"OpenAPI-Generator","operation":"Update","time":"2023-07-19T10:40:03Z"}],"name":"k8smetricdataid","resourceVersion":"5719372853","selfLink":"/apis/monitoring.bk.tencent.com/v1beta1/dataids/k8smetricdataid","uid":"fc006b95-9a97-4479-88c3-8d7a53500f00"},"spec":{"dataID":1572864,"dimensionReplace":{},"labels":{"bcs_cluster_id":"BCS-K8S-00000","bk_biz_id":"2"},"metricReplace":{}}}],"kind":"DataIDList","metadata":{"continue":"","resourceVersion":"10899929740","selfLink":"/apis/monitoring.bk.tencent.com/v1beta1/dataids"}}`)
	patchListK8sResource := gomonkey.ApplyFunc(BcsClusterInfoSvc.ListK8sResource, func(b BcsClusterInfoSvc, group, version, resource string) (*unstructured.UnstructuredList, error) {
		var target unstructured.UnstructuredList
		unstructured.UnstructuredJSONScheme.Decode(data, &schema.GroupVersionKind{models.BcsResourceGroupName, models.BcsResourceVersion, models.BcsResourceDataIdResourceKind}, &target)
		return &target, nil
	})
	patchCreateK8sResource := gomonkey.ApplyFunc(BcsClusterInfoSvc.CreateK8sResource, func(b BcsClusterInfoSvc, group, version, resource string) (*unstructured.UnstructuredList, error) {
		createCount += 1
		return nil, nil
	})
	patchUpdateK8sResource := gomonkey.ApplyFunc(BcsClusterInfoSvc.UpdateK8sResource, func(b BcsClusterInfoSvc, group, version, resource string) (*unstructured.UnstructuredList, error) {
		updateCount += 1
		return nil, nil
	})
	patchGetK8sResource := gomonkey.ApplyFunc(BcsClusterInfoSvc.GetK8sResource, func(b BcsClusterInfoSvc, name, group, version, resource string) (*unstructured.Unstructured, error) {
		var target unstructured.Unstructured
		switch name {
		case "k8seventdataid":
			data := []byte(`{"apiVersion":"monitoring.bk.tencent.com/v1beta1","kind":"DataID","metadata":{"creationTimestamp":"2023-07-19T10:40:04Z","generation":1,"labels":{"isCommon":"true","isSystem":"true","usage":"event"},"managedFields":[{"apiVersion":"monitoring.bk.tencent.com/v1beta1","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:labels":{".":{},"f:isCommon":{},"f:isSystem":{},"f:usage":{}}},"f:spec":{".":{},"f:dataID":{},"f:dimensionReplace":{},"f:labels":{".":{},"f:bcs_cluster_id":{},"f:bk_biz_id":{}},"f:metricReplace":{}}},"manager":"OpenAPI-Generator","operation":"Update","time":"2023-07-19T10:40:04Z"}],"name":"k8seventdataid","resourceVersion":"5719372903","selfLink":"/apis/monitoring.bk.tencent.com/v1beta1/dataids/k8seventdataid","uid":"33482264-805f-40a2-9487-84e15887797a"},"spec":{"dataID":1572866,"dimensionReplace":{},"labels":{"bcs_cluster_id":"BCS-K8S-00000","bk_biz_id":"2"},"metricReplace":{}}}`)
			unstructured.UnstructuredJSONScheme.Decode(data, &schema.GroupVersionKind{models.BcsResourceGroupName, models.BcsResourceVersion, models.BcsResourceDataIdResourceKind}, &target)
			return &target, nil
		case "k8smetricdataid":
			data := []byte(`{"apiVersion":"monitoring.bk.tencent.com/v1beta1","kind":"DataID","metadata":{"creationTimestamp":"2023-07-19T10:40:03Z","generation":1,"labels":{"isCommon":"true","isSystem":"true","usage":"metric"},"managedFields":[{"apiVersion":"monitoring.bk.tencent.com/v1beta1","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:labels":{".":{},"f:isCommon":{},"f:isSystem":{},"f:usage":{}}},"f:spec":{".":{},"f:dataID":{},"f:dimensionReplace":{},"f:labels":{".":{},"f:bcs_cluster_id":{},"f:bk_biz_id":{}},"f:metricReplace":{}}},"manager":"OpenAPI-Generator","operation":"Update","time":"2023-07-19T10:40:03Z"}],"name":"k8smetricdataid","resourceVersion":"5719372853","selfLink":"/apis/monitoring.bk.tencent.com/v1beta1/dataids/k8smetricdataid","uid":"fc006b95-9a97-4479-88c3-8d7a53500f00"},"spec":{"dataID":1572864,"dimensionReplace":{},"labels":{"bcs_cluster_id":"BCS-K8S-00000","bk_biz_id":"2"},"metricReplace":{}}}`)
			unstructured.UnstructuredJSONScheme.Decode(data, &schema.GroupVersionKind{models.BcsResourceGroupName, models.BcsResourceVersion, models.BcsResourceDataIdResourceKind}, &target)
			return &target, nil
		default:
			// 模拟不存在的resource
			return nil, &k8sErr.StatusError{ErrStatus: metav1.Status{
				TypeMeta: metav1.TypeMeta{},
				ListMeta: metav1.ListMeta{},
				Code:     404,
			}}
		}

	})
	defer patchListK8sResource.Reset()
	defer patchCreateK8sResource.Reset()
	defer patchUpdateK8sResource.Reset()
	defer patchGetK8sResource.Reset()
	var cloudId = 0
	cluster := &bcs.BCSClusterInfo{
		ClusterID:          "BCS-K8S-00000",
		BCSApiClusterId:    "BCS-K8S-00000",
		BkBizId:            2,
		BkCloudId:          &cloudId,
		ProjectId:          "xxxxx",
		Status:             "",
		DomainName:         "www.xxx.com",
		Port:               80,
		ServerAddressPath:  "clusters",
		ApiKeyType:         "authorization",
		ApiKeyContent:      "xxxxxx",
		ApiKeyPrefix:       "Bearer",
		IsSkipSslVerify:    true,
		K8sMetricDataID:    1572864,
		CustomMetricDataID: 1572865,
		K8sEventDataID:     1572866,
		Creator:            "system",
		CreateTime:         time.Now(),
		LastModifyTime:     time.Now(),
		LastModifyUser:     "system",
	}
	svc := NewBcsClusterInfoSvc(cluster)
	// 模拟集群和db中数据一致
	err := svc.RefreshCommonResource()
	assert.Nil(t, err)
	assert.Equal(t, 0, createCount)
	assert.Equal(t, 0, updateCount)

	// 模拟配置不同，更新resource
	cluster.K8sEventDataID = 1234567
	err = svc.RefreshCommonResource()
	assert.Nil(t, err)
	assert.Equal(t, 0, createCount)
	assert.Equal(t, 1, updateCount)

	// 模拟集群中缺少resource，进行创建
	data = []byte(`{"apiVersion":"monitoring.bk.tencent.com/v1beta1","items":[{"apiVersion":"monitoring.bk.tencent.com/v1beta1","kind":"DataID","metadata":{"creationTimestamp":"2023-07-19T10:40:04Z","generation":1,"labels":{"isCommon":"true","isSystem":"true","usage":"event"},"managedFields":[{"apiVersion":"monitoring.bk.tencent.com/v1beta1","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:labels":{".":{},"f:isCommon":{},"f:isSystem":{},"f:usage":{}}},"f:spec":{".":{},"f:dataID":{},"f:dimensionReplace":{},"f:labels":{".":{},"f:bcs_cluster_id":{},"f:bk_biz_id":{}},"f:metricReplace":{}}},"manager":"OpenAPI-Generator","operation":"Update","time":"2023-07-19T10:40:04Z"}],"name":"k8seventdataid","resourceVersion":"5719372903","selfLink":"/apis/monitoring.bk.tencent.com/v1beta1/dataids/k8seventdataid","uid":"33482264-805f-40a2-9487-84e15887797a"},"spec":{"dataID":1572866,"dimensionReplace":{},"labels":{"bcs_cluster_id":"BCS-K8S-00000","bk_biz_id":"2"},"metricReplace":{}}},{"apiVersion":"monitoring.bk.tencent.com/v1beta1","kind":"DataID","metadata":{"creationTimestamp":"2023-07-19T10:40:03Z","generation":1,"labels":{"isCommon":"true","isSystem":"true","usage":"metric"},"managedFields":[{"apiVersion":"monitoring.bk.tencent.com/v1beta1","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:labels":{".":{},"f:isCommon":{},"f:isSystem":{},"f:usage":{}}},"f:spec":{".":{},"f:dataID":{},"f:dimensionReplace":{},"f:labels":{".":{},"f:bcs_cluster_id":{},"f:bk_biz_id":{}},"f:metricReplace":{}}},"manager":"OpenAPI-Generator","operation":"Update","time":"2023-07-19T10:40:03Z"}],"name":"k8smetricdataid","resourceVersion":"5719372853","selfLink":"/apis/monitoring.bk.tencent.com/v1beta1/dataids/k8smetricdataid","uid":"fc006b95-9a97-4479-88c3-8d7a53500f00"},"spec":{"dataID":1572864,"dimensionReplace":{},"labels":{"bcs_cluster_id":"BCS-K8S-00000","bk_biz_id":"2"},"metricReplace":{}}}],"kind":"DataIDList","metadata":{"continue":"","resourceVersion":"10899929740","selfLink":"/apis/monitoring.bk.tencent.com/v1beta1/dataids"}}`)
	err = svc.RefreshCommonResource()
	assert.Nil(t, err)
	assert.Equal(t, 1, createCount)
}

func Test_isIPv6(t *testing.T) {
	type args struct {
		ip string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{"ipv4", args{ip: "1.1.2.3"}, false},
		{"err", args{ip: "127.0.0.1"}, false},
		{"ipv6", args{ip: "fe80::eca3:77af:98e1:725c"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, isIPv6(tt.args.ip), "isIPv6(%v)", tt.args.ip)
		})
	}
}

func TestKubernetesNodeJsonParser(t *testing.T) {
	var nodeInfo NodeInfo
	err := jsonx.UnmarshalString(`{"metadata":{"selfLink":"/api/v1/nodes/127.0.0.1","uid":"f974e001-0398-48f9-a305-c1d50ab68a20","annotations":{"csi.volume.kubernetes.io/nodeid":"{\"com.tencent.cloud.csi.cbs\":\"ins-qcc4u546\"}","node.alpha.kubernetes.io/ttl":"0","volumes.kubernetes.io/controller-managed-attach-detach":"true"},"creationTimestamp":"2023-05-23T11:29:27Z","labels":{"beta.kubernetes.io/arch":"amd64","beta.kubernetes.io/os":"linux","node-role.kubernetes.io/role-test":"test-role"},"name":"127.0.0.1-test-name","resourceVersion":"10804990572"},"spec":{"taints":[{"key":"dedicated","value":"ingress-nginx","effect":"PreferNoSchedule"},{"effect":"NoSchedule","key":"node.kubernetes.io/unschedulable","timeAdded":"2023-09-27T09:58:05Z"}],"unschedulable":true,"podCIDR":"172.0.0.0/24","podCIDRs":["172.0.0.0/24"],"providerID":"qcloud:///100006/ins-qcc4u546"},"status":{"conditions":[{"lastHeartbeatTime":"2023-05-23T11:29:30Z","lastTransitionTime":"2023-05-23T11:29:30Z","message":"RouteController created a route","reason":"RouteCreated","status":"False","type":"NetworkUnavailable"},{"lastTransitionTime":"2023-08-14T07:23:01Z","message":"kubelet has sufficient memory available","reason":"KubeletHasSufficientMemory","status":"False","type":"MemoryPressure","lastHeartbeatTime":"2023-10-26T07:48:56Z"},{"lastHeartbeatTime":"2023-10-26T07:48:56Z","lastTransitionTime":"2023-08-14T07:23:01Z","message":"kubelet has no disk pressure","reason":"KubeletHasNoDiskPressure","status":"False","type":"DiskPressure"},{"type":"PIDPressure","lastHeartbeatTime":"2023-10-26T07:48:56Z","lastTransitionTime":"2023-08-14T07:23:01Z","message":"kubelet has sufficient PID available","reason":"KubeletHasSufficientPID","status":"False"},{"lastTransitionTime":"2023-08-14T07:23:01Z","message":"kubelet is posting ready status","reason":"KubeletReady","status":"True","type":"Ready","lastHeartbeatTime":"2023-10-26T07:48:56Z"}],"daemonEndpoints":{"kubeletEndpoint":{"Port":10250}},"images":[{"names":["ccr.ccs.tencentyun.com/tkeimages/hyperkube@sha256:3c16349f393b7dc0820661a87dc5160904176ce371f7d40b4d338524dc3aaa6f","ccr.ccs.tencentyun.com/tkeimages/hyperkube:v1.20.6-tke.33"],"sizeBytes":638859459},{"names":["ccr.ccs.tencentyun.com/library/pause@sha256:5ab61aabaedd6c40d05ce1ac4ea72c2079f4a0f047ec1dc100ea297b553539ab","ccr.ccs.tencentyun.com/library/pause:latest"],"sizeBytes":682696}],"nodeInfo":{"osImage":"CentOS Linux 7 (Core)","bootID":"122e6ee1-39b6-42f7-a0f6-3357231f6d2e","kernelVersion":"3.10.0-1160.88.1.el7.x86_64","machineID":"f4fb1e809fc4402a9d3e7822776988fa","systemUUID":"F4FB1E80-9FC4-402A-9D3E-7822776988FA","operatingSystem":"linux","architecture":"amd64","kubeProxyVersion":"v1.20.6-tke.33","kubeletVersion":"v1.20.6-tke.33","containerRuntimeVersion":"docker://19.3.9-tke.1"},"addresses":[{"type":"InternalIP","address":"127.0.0.1"},{"address":"127.0.0.1","type":"Hostname"}],"allocatable":{"memory":"3090468Ki","pods":"253","cpu":"1900m","ephemeral-storage":"94998384074","hugepages-1Gi":"0","hugepages-2Mi":"0"},"capacity":{"cpu":"2","ephemeral-storage":"103079844Ki","hugepages-1Gi":"0","hugepages-2Mi":"0","memory":"3717156Ki","pods":"253"}},"apiVersion":"v1","kind":"Node"}`, &nodeInfo)
	assert.NoError(t, err)
	parser := KubernetesNodeJsonParser{Node: nodeInfo}
	assert.Equal(t, "127.0.0.1", parser.NodeIp())
	assert.Equal(t, "127.0.0.1-test-name", parser.Name())

	labelsJson, err := jsonx.MarshalString(parser.Labels())
	assert.NoError(t, err)
	assert.JSONEq(t, `{"beta.kubernetes.io/arch":"amd64","beta.kubernetes.io/os":"linux","node-role.kubernetes.io/role-test":"test-role"}`, labelsJson)
	assert.Equal(t, []string{"role-test"}, parser.RoleList())
	assert.Equal(t, "Ready,SchedulingDisabled", parser.ServiceStatus())
	tm, err := time.Parse("2006-01-02 15:04:05", "2023-05-23 11:29:27")
	assert.NoError(t, err)
	assert.Equal(t, tm, *parser.CreationTimestamp())
	parser.TaintLabels()
	assert.ElementsMatch(t, []string{"dedicated=ingress-nginx:PreferNoSchedule", "node.kubernetes.io/unschedulable=:NoSchedule"}, parser.TaintLabels())

	result := make(map[string]string)
	for _, m := range parser.LabelList() {
		result[m["key"]] = m["value"]
	}
	assert.True(t, assert.ObjectsAreEqual(map[string]string{"beta.kubernetes.io/arch": "amd64", "beta.kubernetes.io/os": "linux", "node-role.kubernetes.io/role-test": "test-role"}, result))
}

func TestBCSClusterInfo_Create(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	c := bcs.BCSClusterInfo{
		ClusterID: "new_create_cluster",
	}
	db := mysql.GetDBSession().DB
	db.Delete(&c, "cluster_id = ?", c.ClusterID)
	err := c.Create(db)
	assert.NoError(t, err)
	assert.Equal(t, "Bearer", c.ApiKeyPrefix)
	assert.Equal(t, "authorization", c.ApiKeyType)
	assert.Equal(t, models.BcsClusterStatusRunning, c.Status)
}

func TestBcsClusterInfoSvc_IsClusterIdInGray(t *testing.T) {
	svc := NewBcsClusterInfoSvc(nil)
	// 未启用灰度
	config.BcsEnableBcsGray = false
	config.BcsGrayClusterIdList = []string{"cluster_1", "cluster_2"}
	assert.True(t, svc.IsClusterIdInGray("abc"))

	// 启用灰度
	config.BcsEnableBcsGray = true
	assert.False(t, svc.IsClusterIdInGray("abc"))
	assert.True(t, svc.IsClusterIdInGray("cluster_2"))
}

func TestBcsClusterInfoSvc_FetchK8sClusterList(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	gomonkey.ApplyMethod(&http.Client{}, "Do", func(t *http.Client, req *http.Request) (*http.Response, error) {
		data := `{"message":"ok","result":true,"code":200,"data":[{"clusterID":"BCS-K8S-00000","clusterName":"蓝鲸","federationClusterID":"","provider":"bluekingCloud","region":"default","vpcID":"","projectID":"xxxxxxx750477982c23","businessID":"2","environment":"prod","engineType":"k8s","isExclusive":true,"clusterType":"single","labels":{},"creator":"admin","createTime":"2023-10-26T21:01:57+08:00","updateTime":"2023-10-26T21:01:57+08:00","bcsAddons":{},"extraAddons":{},"systemID":"","manageType":"INDEPENDENT_CLUSTER","master":{},"networkSettings":{"clusterIPxxxxx":"","serviceIxx":"","maxNodePodNum":0,"maxServiceNum":0,"enableVPCCni":false,"eniSubnetIDs":[],"subnetSource":null,"isStaticIpMode":false,"claimExpiredSeconds":0,"multiClusterCIDR":[],"cidrStep":0},"clusterBasicSettings":{"OS":"Linux","version":"v1.20.6-tke.34","clusterTags":{},"versionName":""},"clusterAdvanceSettings":{"IPVS":true,"containerRuntime":"docker","runtimeVersion":"19.3","extraArgs":{"Etcd":"node-data-dir=/data/bcs/lib/etcd;"}},"nodeSettings":{"dockerGraphPath":"/data/bcs/lib/docker","mountTarget":"/data","unSchedulable":1,"labels":{},"extraArgs":{}},"status":"RUNNING","updater":"","networkType":"overlay","autoGenerateMasterNodes":false,"template":[],"extraInfo":{},"moduleID":"","extraClusterID":"","isCommonCluster":false,"description":"xxxxx部署环境","clusterCategory":"","is_shared":false,"kubeConfig":"","importCategory":"","cloudAccountID":""}]}`
		body := io.NopCloser(strings.NewReader(data))
		return &http.Response{
			Status:        "ok",
			StatusCode:    200,
			Body:          body,
			ContentLength: int64(len(data)),
			Request:       req,
		}, nil
	})
	svc := NewBcsClusterInfoSvc(nil)
	clusterList, err := svc.FetchK8sClusterList()
	assert.NoError(t, err)
	assert.Equal(t, 1, len(clusterList))
	assert.Equal(t, "BCS-K8S-00000", clusterList[0].ClusterId)

}

func TestBcsClusterInfoSvc_FetchK8sNodeListByCluster(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	gomonkey.ApplyMethod(&http.Client{}, "Do", func(t *http.Client, req *http.Request) (*http.Response, error) {
		var data string
		if strings.Contains(req.URL.Path, "Node") {
			data = `{"message":"ok","result":true,"code":0,"data":[{"status":{"addresses":[{"address":"1.0.0.1","type":"InternalIP"},{"address":"1.0.0.1","type":"Hostname"}],"conditions":[{"lastHeartbeatTime":"2023-05-23T11:29:30Z","lastTransitionTime":"2023-05-23T11:29:30Z","message":"RouteController created a route","reason":"RouteCreated","status":"False","type":"NetworkUnavailable"},{"type":"MemoryPressure","lastHeartbeatTime":"2023-11-07T11:00:20Z","lastTransitionTime":"2023-08-14T07:23:01Z","message":"kubelet has sufficient memory available","reason":"KubeletHasSufficientMemory","status":"False"},{"message":"kubelet has no disk pressure","reason":"KubeletHasNoDiskPressure","status":"False","type":"DiskPressure","lastHeartbeatTime":"2023-11-07T11:00:20Z","lastTransitionTime":"2023-08-14T07:23:01Z"},{"status":"False","type":"PIDPressure","lastHeartbeatTime":"2023-11-07T11:00:20Z","lastTransitionTime":"2023-08-14T07:23:01Z","message":"kubelet has sufficient PID available","reason":"KubeletHasSufficientPID"},{"type":"Ready","lastHeartbeatTime":"2023-11-07T11:00:20Z","lastTransitionTime":"2023-08-14T07:23:01Z","message":"kubelet is posting ready status","reason":"KubeletReady","status":"True"}]},"metadata":{"labels":{"eng-cstate-7fca4c84-1":"1","kubernetes.io/arch":"amd64","topology.kubernetes.io/region":"gz","topology.com.tencent.cloud.csi.cbs/zone":"ap-guangzhou-6","beta.kubernetes.io/arch":"amd64","kubernetes.io/hostname":"1.0.0.1","beta.kubernetes.io/instance-type":"SA3.MEDIUM2","kubernetes.io/os":"linux","eng-cstate-22817e80-4":"1","eng-cstate-86fabc2b-6":"1","eng-cstate-3b9bc87a-7":"1","beta.kubernetes.io/os":"linux","dedicated":"ingress-nginx","topology.kubernetes.io/zone":"100006","eng-cstate-12616088-2":"1","failure-domain.beta.kubernetes.io/region":"gz","node.kubernetes.io/instance-type":"SA3.MEDIUM2","cloud.tencent.com/node-instance-id":"ins-qcc4u546","eng-cstate-3e8191b0-5":"1","ingress-nginx":"true","eng-cstate-b59d451c-3":"1","failure-domain.beta.kubernetes.io/zone":"100006"},"name":"1.0.0.1","resourceVersion":"11386901553","creationTimestamp":"2023-05-23T11:29:27Z"},"spec":{"unschedulable":true,"taints":[{"effect":"PreferNoSchedule","key":"dedicated","value":"ingress-nginx"},{"effect":"NoSchedule","key":"node.kubernetes.io/unschedulable","timeAdded":"2023-09-27T09:58:05Z"}]}},{"metadata":{"resourceVersion":"12191526357","creationTimestamp":"2023-11-20T14:59:56Z","labels":{"kubernetes.io/arch":"amd64","kubernetes.io/hostname":"1.0.0.2","topology.com.tencent.cloud.csi.cbs/zone":"ap-guangzhou-6","tke.cloud.tencent.com/cbs-mountable":"true","cloud.tencent.com/node-instance-id":"ins-olh8t1mu","tke.cloud.tencent.com/nodepool-id":"np-a72t3t12","failure-domain.beta.kubernetes.io/region":"gz","beta.kubernetes.io/instance-type":"SA3.8XLARGE64","kubernetes.io/os":"linux","beta.kubernetes.io/arch":"amd64","failure-domain.beta.kubernetes.io/zone":"100006","topology.kubernetes.io/region":"gz","topology.kubernetes.io/zone":"100006","cloud.tencent.com/auto-scaling-group-id":"asg-5ndk7a9g","node.kubernetes.io/instance-type":"SA3.8XLARGE64","beta.kubernetes.io/os":"linux"},"name":"1.0.0.2"},"spec":{},"status":{"addresses":[{"address":"1.0.0.2","type":"InternalIP"},{"type":"Hostname","address":"1.0.0.2"}],"conditions":[{"lastHeartbeatTime":"2023-11-20T15:00:04Z","lastTransitionTime":"2023-11-20T15:00:04Z","message":"RouteController created a route","reason":"RouteCreated","status":"False","type":"NetworkUnavailable"},{"type":"MemoryPressure","lastHeartbeatTime":"2023-11-24T03:16:59Z","lastTransitionTime":"2023-11-20T14:59:56Z","message":"kubelet has sufficient memory available","reason":"KubeletHasSufficientMemory","status":"False"},{"lastHeartbeatTime":"2023-11-24T03:16:59Z","lastTransitionTime":"2023-11-20T14:59:56Z","message":"kubelet has no disk pressure","reason":"KubeletHasNoDiskPressure","status":"False","type":"DiskPressure"},{"lastTransitionTime":"2023-11-20T14:59:56Z","message":"kubelet has sufficient PID available","reason":"KubeletHasSufficientPID","status":"False","type":"PIDPressure","lastHeartbeatTime":"2023-11-24T03:16:59Z"},{"lastTransitionTime":"2023-11-20T15:01:09Z","message":"kubelet is posting ready status","reason":"KubeletReady","status":"True","type":"Ready","lastHeartbeatTime":"2023-11-24T03:16:59Z"}]}},{"metadata":{"creationTimestamp":"2023-05-23T05:44:29Z","labels":{"kubernetes.io/hostname":"1.0.0.3","eng-cstate-22817e80-4":"1","eng-cstate-12616088-2":"1","dedicated":"bkSaaS","node.kubernetes.io/instance-type":"SA3.4XLARGE32","topology.kubernetes.io/region":"gz","beta.kubernetes.io/instance-type":"SA3.4XLARGE32","failure-domain.beta.kubernetes.io/region":"gz","tke.cloud.tencent.com/nodepool-id":"np-nckqef5i","cloud.tencent.com/auto-scaling-group-id":"asg-1r49l84c","eng-cstate-7fca4c84-1":"1","eng-cstate-b59d451c-3":"1","eng-cstate-3b9bc87a-7":"1","kubernetes.io/arch":"amd64","failure-domain.beta.kubernetes.io/zone":"100006","beta.kubernetes.io/arch":"amd64","beta.kubernetes.io/os":"linux","cloud.tencent.com/node-instance-id":"ins-3yvkivri","eng-cstate-3e8191b0-5":"1","topology.kubernetes.io/zone":"100006","kubernetes.io/os":"linux","topology.com.tencent.cloud.csi.cbs/zone":"ap-guangzhou-6","eng-cstate-86fabc2b-6":"1"},"name":"1.0.0.3","resourceVersion":"12091824029"},"spec":{"taints":[{"key":"dedicated","value":"bkSaaS","effect":"NoSchedule"}]},"status":{"addresses":[{"address":"1.0.0.3","type":"InternalIP"},{"address":"1.0.0.3","type":"Hostname"}],"conditions":[{"message":"RouteController created a route","reason":"RouteCreated","status":"False","type":"NetworkUnavailable","lastHeartbeatTime":"2023-05-23T05:44:40Z","lastTransitionTime":"2023-05-23T05:44:40Z"},{"message":"kubelet has sufficient memory available","reason":"KubeletHasSufficientMemory","status":"False","type":"MemoryPressure","lastHeartbeatTime":"2023-11-22T02:14:10Z","lastTransitionTime":"2023-11-04T07:48:28Z"},{"status":"False","type":"DiskPressure","lastHeartbeatTime":"2023-11-22T02:14:10Z","lastTransitionTime":"2023-11-10T08:41:43Z","message":"kubelet has no disk pressure","reason":"KubeletHasNoDiskPressure"},{"message":"kubelet has sufficient PID available","reason":"KubeletHasSufficientPID","status":"False","type":"PIDPressure","lastHeartbeatTime":"2023-11-22T02:14:10Z","lastTransitionTime":"2023-11-04T07:48:28Z"},{"lastTransitionTime":"2023-11-04T07:48:28Z","message":"kubelet is posting ready status","reason":"KubeletReady","status":"True","type":"Ready","lastHeartbeatTime":"2023-11-22T02:14:10Z"}]}}]}`
		}
		if strings.Contains(req.URL.Path, "Endpoints") {
			data = `{"message":"ok","result":true,"code":0,"data":[]}`
		}
		body := io.NopCloser(strings.NewReader(data))
		return &http.Response{
			Status:        "ok",
			StatusCode:    200,
			Body:          body,
			ContentLength: int64(len(data)),
			Request:       req,
		}, nil
	})
	nodes, err := NewBcsClusterInfoSvc(nil).FetchK8sNodeListByCluster("BCS-K8S-00000")
	assert.NoError(t, err)
	assert.Len(t, nodes, 3)
	var ips []string
	for _, node := range nodes {
		ips = append(ips, node.NodeIp)
	}
	assert.ElementsMatch(t, []string{"1.0.0.1", "1.0.0.2", "1.0.0.3"}, ips)
}

func TestBcsClusterInfoSvc_RegisterCluster(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	clusterID := "BCS-K8S-00001"
	bkBizId := "2"
	projectId := "project_id_xxxxx"
	var initChannelId uint = 1999900
	c := storage.ClusterInfo{
		ClusterID:        1,
		ClusterName:      "kafka_default_test",
		ClusterType:      models.StorageTypeKafka,
		DomainName:       "127.0.0.1",
		Port:             9096,
		IsDefaultCluster: true,
		GseStreamToId:    0,
	}
	c.Delete(db)
	err := c.Create(db)
	assert.NoError(t, err)
	db.Delete(&bcs.BCSClusterInfo{}, "cluster_id = ?", clusterID)
	db.Delete(&resulttable.DataSource{}, fmt.Sprintf("data_name like '%%%s%%'", clusterID))
	db.Delete(&storage.KafkaTopicInfo{}, "bk_data_id > ?", initChannelId)
	db.Delete(&resulttable.DataSourceOption{}, "bk_data_id > ?", initChannelId)
	db.Delete(&customreport.TimeSeriesGroup{}, fmt.Sprintf("time_series_group_name like '%%%s%%'", clusterID))
	db.Delete(&customreport.EventGroup{}, "bk_data_id > ?", initChannelId)
	httpPatch := gomonkey.ApplyMethod(&http.Client{}, "Do", func(t *http.Client, req *http.Request) (*http.Response, error) {
		var data string
		if strings.Contains(req.URL.Path, "add_route") {
			initChannelId += 1
			data = fmt.Sprintf(`{"message":"ok","result":true,"code":0,"data":{"channel_id":%v}}`, initChannelId)
		}
		if strings.Contains(req.URL.Path, "query_route") {
			data = `{"message":"ok","result":true,"code":0}`
		}
		body := io.NopCloser(strings.NewReader(data))
		return &http.Response{
			Status:        "ok",
			StatusCode:    200,
			Body:          body,
			ContentLength: int64(len(data)),
			Request:       req,
		}, nil
	})
	defer httpPatch.Reset()
	patch := gomonkey.ApplyMethod(ResultTableSvc{}, "CreateResultTable", func(ResultTableSvc, uint, int, string, string, bool, string, string, string, map[string]interface{}, []map[string]interface{}, bool, map[string]interface{}, string, map[string]interface{}) error {
		return nil
	})
	defer patch.Reset()
	gomonkey.ApplyFunc(hashconsul.Put, func(c *consul.Instance, key, val string) error { return nil })
	cluster, err := NewBcsClusterInfoSvc(nil).RegisterCluster(bkBizId, clusterID, projectId, "test")
	assert.NoError(t, err)
	dataIdList := []uint{cluster.K8sMetricDataID, cluster.CustomMetricDataID, cluster.K8sEventDataID}
	targetIdList := []uint{initChannelId, initChannelId - 1, initChannelId - 2}
	assert.ElementsMatch(t, []uint{initChannelId, initChannelId - 1, initChannelId - 2}, dataIdList)
	count, err := resulttable.NewDataSourceQuerySet(db).BkDataIdIn(targetIdList...).Count()
	assert.NoError(t, err)
	assert.Equal(t, len(targetIdList), count)

}

func TestBcsClusterInfoSvc_RefreshMetricLabel(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	// mocker bcsClusterInfo
	cluster := bcs.BCSClusterInfo{
		ClusterID:          "test_metric_label_bcs_cluster",
		K8sMetricDataID:    170000,
		CustomMetricDataID: 170001,
	}
	db.Delete(&cluster, "cluster_id = ?", cluster.ClusterID)
	err := cluster.Create(db)
	assert.NoError(t, err)
	// mocker serviceMonitor
	serviceMonitor := bcs.ServiceMonitorInfo{
		BCSResource: bcs.BCSResource{
			ClusterID:          cluster.ClusterID,
			BkDataId:           170002,
			RecordCreateTime:   time.Now(),
			ResourceCreateTime: time.Now(),
		},
	}
	db.Delete(&serviceMonitor, "cluster_id = ?", serviceMonitor.ClusterID)
	err = serviceMonitor.Create(db)
	assert.NoError(t, err)
	// mocker podMonitor
	podMonitor := bcs.PodMonitorInfo{
		BCSResource: bcs.BCSResource{
			ClusterID:          cluster.ClusterID,
			BkDataId:           170003,
			RecordCreateTime:   time.Now(),
			ResourceCreateTime: time.Now(),
		},
	}
	db.Delete(&podMonitor, "cluster_id = ?", podMonitor.ClusterID)
	err = podMonitor.Create(db)
	assert.NoError(t, err)
	// mock tsGroup
	tsGroup0 := customreport.TimeSeriesGroup{
		CustomGroupBase: customreport.CustomGroupBase{
			BkDataID: 170000,
			Label:    "",
			IsEnable: true,
		},
	}
	tsGroup1 := customreport.TimeSeriesGroup{
		CustomGroupBase: customreport.CustomGroupBase{
			BkDataID: 170001,
			Label:    "",
			IsEnable: true,
		},
	}
	tsGroup2 := customreport.TimeSeriesGroup{
		CustomGroupBase: customreport.CustomGroupBase{
			BkDataID: 170002,
			Label:    "",
			IsEnable: true,
		},
	}
	tsGroup3 := customreport.TimeSeriesGroup{
		CustomGroupBase: customreport.CustomGroupBase{
			BkDataID: 170003,
			Label:    "",
			IsEnable: true,
		},
	}
	db.Delete(&customreport.TimeSeriesGroup{}, "bk_data_id in (?)", []uint{170000, 170001, 170002, 170003})
	err = tsGroup0.Create(db)
	assert.NoError(t, err)
	err = tsGroup1.Create(db)
	assert.NoError(t, err)
	err = tsGroup2.Create(db)
	assert.NoError(t, err)
	err = tsGroup3.Create(db)
	assert.NoError(t, err)

	// mock tsMetrics
	tsMetric0 := customreport.TimeSeriesMetric{
		GroupID:   tsGroup0.TimeSeriesGroupID,
		FieldName: "any_for_default_prefix",
		Label:     "label0",
	}
	tsMetric1 := customreport.TimeSeriesMetric{
		GroupID:   tsGroup1.TimeSeriesGroupID,
		FieldName: "node_for_node_prefix",
		Label:     "label1",
	}
	tsMetric2 := customreport.TimeSeriesMetric{
		GroupID:   tsGroup2.TimeSeriesGroupID,
		FieldName: "container_for_container_prefix",
		Label:     "label2",
	}
	tsMetric3 := customreport.TimeSeriesMetric{
		GroupID:   tsGroup3.TimeSeriesGroupID,
		FieldName: "kube_for_kube_prefix",
		Label:     "label3",
	}
	filedNames := []string{"any_for_default_prefix", "node_for_node_prefix", "container_for_container_prefix", "kube_for_kube_prefix"}
	db.Delete(&customreport.TimeSeriesMetric{}, "field_name in (?)", filedNames)
	err = tsMetric0.Create(db)
	assert.NoError(t, err)
	err = tsMetric1.Create(db)
	assert.NoError(t, err)
	err = tsMetric2.Create(db)
	assert.NoError(t, err)
	err = tsMetric3.Create(db)
	assert.NoError(t, err)

	err = NewBcsClusterInfoSvc(nil).RefreshMetricLabel()
	assert.NoError(t, err)
	var metrics []customreport.TimeSeriesMetric
	err = customreport.NewTimeSeriesMetricQuerySet(db).Select(customreport.TimeSeriesMetricDBSchema.FieldName, customreport.TimeSeriesMetricDBSchema.Label).FieldNameIn(filedNames...).All(&metrics)
	assert.NoError(t, err)
	assert.NotEmpty(t, metrics)
	for _, m := range metrics {
		var target string
		for k, v := range models.BcsMetricLabelPrefix {
			if strings.HasPrefix(m.FieldName, k) {
				target = v
			}
		}
		if target == "" {
			target = models.BcsMetricLabelPrefix["*"]
		}
		assert.Equal(t, target, m.Label)
	}
}

func TestBcsClusterInfoSvc_RefreshClusterResource(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	sp := space.Space{
		SpaceTypeId: models.SpaceTypeBKCI,
		SpaceId:     "bkci_biz_test",
		SpaceName:   "bkci_biz_test",
		SpaceCode:   "1234561234567f3e750477982c23",
		IsBcsValid:  true,
	}
	db.Delete(&sp, "space_id = ?", sp.SpaceId)
	err := sp.Create(db)
	assert.NoError(t, err)

	cluster := bcs.BCSClusterInfo{
		ClusterID:          "space_resource_test_cluster",
		BCSApiClusterId:    "space_resource_test_cluster",
		BkBizId:            22,
		ProjectId:          "1234561234567f3e750477982c23",
		K8sMetricDataID:    1880001,
		CustomMetricDataID: 1880003,
		K8sEventDataID:     1880002,
	}
	db.Delete(&cluster, "cluster_id = ?", cluster.ClusterID)
	err = cluster.Create(db)
	assert.NoError(t, err)

	clusterShared := bcs.BCSClusterInfo{
		ClusterID:          "space_resource_test_cluster_shared",
		BCSApiClusterId:    "space_resource_test_cluster_shared",
		BkBizId:            22,
		ProjectId:          "1234561234567f3e750477982c23",
		K8sMetricDataID:    1880011,
		CustomMetricDataID: 1880013,
		K8sEventDataID:     1880012,
	}
	db.Delete(&clusterShared, "cluster_id = ?", clusterShared.ClusterID)
	err = clusterShared.Create(db)
	assert.NoError(t, err)

	db.Delete(&space.SpaceResource{}, "space_id = ?", sp.SpaceId)
	db.Delete(&space.SpaceDataSource{}, "space_id = ?", sp.SpaceId)

	gomonkey.ApplyFunc(apiservice.BcsClusterManagerService.GetProjectClusters, func(s apiservice.BcsClusterManagerService, projectId string, excludeSharedCluster bool) ([]map[string]interface{}, error) {
		if projectId != "1234561234567f3e750477982c23" {
			return nil, nil
		}
		return []map[string]interface{}{
			{
				"projectId": "1234561234567f3e750477982c23",
				"clusterId": "space_resource_test_cluster",
				"bkBizId":   "2",
				"isShared":  false,
			},
			{
				"projectId": "1234561234567f3e750477982c23",
				"clusterId": "space_resource_test_cluster_shared",
				"bkBizId":   "2",
				"isShared":  true,
			},
		}, nil
	})

	gomonkey.ApplyFunc(apiservice.BcsService.FetchSharedClusterNamespaces, func(s apiservice.BcsService, clusterId string, projectCode string) ([]map[string]string, error) {
		return []map[string]string{
			{
				"projectId":   "shared_cluster",
				"projectCode": projectCode,
				"clusterId":   clusterId,
				"namespace":   "n1",
			},
			{
				"projectId":   "shared_cluster",
				"projectCode": projectCode,
				"clusterId":   clusterId,
				"namespace":   "n2",
			},
		}, nil
	})

	err = NewBcsClusterInfoSvc(nil).RefreshClusterResource()
	assert.NoError(t, err)

	var spdsList []space.SpaceDataSource
	err = space.NewSpaceDataSourceQuerySet(db).SpaceTypeIdEq(models.SpaceTypeBKCI).SpaceIdEq(sp.SpaceId).All(&spdsList)
	assert.NoError(t, err)

	var dataids []uint
	for _, spds := range spdsList {
		dataids = append(dataids, spds.BkDataId)
	}
	assert.ElementsMatch(t, dataids, []uint{1880001, 1880002, 1880003, 1880011, 1880012, 1880013})

	var sr space.SpaceResource
	err = space.NewSpaceResourceQuerySet(db).SpaceIdEq(sp.SpaceId).One(&sr)
	assert.NoError(t, err)

	equal, err := jsonx.CompareJson(sr.DimensionValues, `[{"cluster_id":"space_resource_test_cluster","cluster_type":"single","namespace":null},{"cluster_id":"space_resource_test_cluster_shared","cluster_type":"shared","namespace":["n2","n1"]}]`)
	assert.NoError(t, err)
	assert.True(t, equal)

	dm, err := sr.GetDimensionValues()
	assert.NoError(t, err)
	err = sr.SetDimensionValues(dm[:1])
	assert.NoError(t, err)
	err = sr.Update(db, space.SpaceResourceDBSchema.DimensionValues)
	assert.NoError(t, err)

	err = NewBcsClusterInfoSvc(nil).RefreshClusterResource()
	assert.NoError(t, err)

	var sr2 space.SpaceResource
	err = space.NewSpaceResourceQuerySet(db).SpaceIdEq(sp.SpaceId).One(&sr2)
	assert.NoError(t, err)

	equal, err = jsonx.CompareJson(sr2.DimensionValues, `[{"cluster_id":"space_resource_test_cluster","cluster_type":"single","namespace":null},{"cluster_id":"space_resource_test_cluster_shared","cluster_type":"shared","namespace":["n2","n1"]}]`)
	assert.NoError(t, err)
	assert.True(t, equal)

}
