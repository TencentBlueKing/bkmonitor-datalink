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
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/bcs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

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
	config.FilePath = "../../../bmw.yaml"
	mocker.PatchDBSession()
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
	assert.Equal(t, 1, updateCount)
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
}
