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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/bcs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

func TestPodMonitorInfoSvc_RefreshResource(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	var data = []byte(`{"apiVersion":"monitoring.coreos.com/v1","items":[{"apiVersion":"monitoring.coreos.com/v1","kind":"PodMonitor","metadata":{"annotations":{"meta.helm.sh/release-name":"bkbase-dgraph","meta.helm.sh/release-namespace":"bkbase"},"creationTimestamp":"2023-10-26T09:15:55Z","generation":1,"labels":{"app.kubernetes.io/managed-by":"Helm"},"managedFields":[{"apiVersion":"monitoring.coreos.com/v1","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:meta.helm.sh/release-name":{},"f:meta.helm.sh/release-namespace":{}},"f:labels":{".":{},"f:app.kubernetes.io/managed-by":{}}},"f:spec":{".":{},"f:endpoints":{},"f:namespaceSelector":{".":{},"f:any":{}},"f:selector":{".":{},"f:matchLabels":{".":{},"f:app":{},"f:chart":{},"f:heritage":{},"f:release":{}}}}},"manager":"helm","operation":"Update","time":"2023-10-26T09:15:55Z"}],"name":"bkbase-dgraph-bkbase-dgr-alpha","namespace":"bkbase","resourceVersion":"10807858063","selfLink":"/apis/monitoring.coreos.com/v1/namespaces/bkbase/podmonitors/bkbase-dgraph-bkbase-dgr-alpha","uid":"e09f8c99-01d8-42f4-8481-0b65a8dca90a"},"spec":{"endpoints":[{"interval":"15s","path":"/debug/prometheus_metrics","port":"alpha-http"}],"namespaceSelector":{"any":true},"selector":{"matchLabels":{"app":"bkbase-dgraph","chart":"bkbase-dgraph-0.0.9","heritage":"Helm","release":"bkbase-dgraph"}}}},{"apiVersion":"monitoring.coreos.com/v1","kind":"PodMonitor","metadata":{"annotations":{"meta.helm.sh/release-name":"bkbase-dgraph","meta.helm.sh/release-namespace":"bkbase"},"creationTimestamp":"2023-10-26T09:15:55Z","generation":1,"labels":{"app.kubernetes.io/managed-by":"Helm"},"managedFields":[{"apiVersion":"monitoring.coreos.com/v1","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:meta.helm.sh/release-name":{},"f:meta.helm.sh/release-namespace":{}},"f:labels":{".":{},"f:app.kubernetes.io/managed-by":{}}},"f:spec":{".":{},"f:endpoints":{},"f:namespaceSelector":{".":{},"f:any":{}},"f:selector":{".":{},"f:matchLabels":{".":{},"f:app":{},"f:chart":{},"f:heritage":{},"f:release":{}}}}},"manager":"helm","operation":"Update","time":"2023-10-26T09:15:55Z"}],"name":"bkbase-dgraph-bkbase-dgr-zero","namespace":"bkbase","resourceVersion":"10807858054","selfLink":"/apis/monitoring.coreos.com/v1/namespaces/bkbase/podmonitors/bkbase-dgraph-bkbase-dgr-zero","uid":"86525481-3a3b-466f-81ed-1cdc59b3c92b"},"spec":{"endpoints":[{"interval":"15s","path":"/debug/prometheus_metrics","port":"zero-http"}],"namespaceSelector":{"any":true},"selector":{"matchLabels":{"app":"bkbase-dgraph","chart":"bkbase-dgraph-0.0.9","heritage":"Helm","release":"bkbase-dgraph"}}}},{"apiVersion":"monitoring.coreos.com/v1","kind":"PodMonitor","metadata":{"annotations":{"meta.helm.sh/release-name":"bkbase-jobnavischeduler","meta.helm.sh/release-namespace":"bkbase"},"creationTimestamp":"2023-10-26T10:20:11Z","generation":1,"labels":{"app.kubernetes.io/managed-by":"Helm"},"managedFields":[{"apiVersion":"monitoring.coreos.com/v1","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:meta.helm.sh/release-name":{},"f:meta.helm.sh/release-namespace":{}},"f:labels":{".":{},"f:app.kubernetes.io/managed-by":{}}},"f:spec":{".":{},"f:endpoints":{},"f:namespaceSelector":{".":{},"f:any":{}},"f:selector":{".":{},"f:matchLabels":{".":{},"f:app.kubernetes.io/service-type":{},"f:k8s-app":{},"f:meta.helm.sh/release-name":{}}}}},"manager":"helm","operation":"Update","time":"2023-10-26T10:20:11Z"}],"name":"bkbase-jobnavischeduler","namespace":"bkbase","resourceVersion":"10809974244","selfLink":"/apis/monitoring.coreos.com/v1/namespaces/bkbase/podmonitors/bkbase-jobnavischeduler","uid":"6b788376-2566-42df-a852-d5ef20ceec2a"},"spec":{"endpoints":[{"interval":"20s","path":"/metrics?","port":"metrics"}],"namespaceSelector":{"any":true},"selector":{"matchLabels":{"app.kubernetes.io/service-type":"metrics","k8s-app":"bkbase-jobnavischeduler","meta.helm.sh/release-name":"bkbase-jobnavischeduler"}}}}],"kind":"PodMonitorList","metadata":{"continue":"","resourceVersion":"10995632417","selfLink":"/apis/monitoring.coreos.com/v1/podmonitors"}}`)
	patchListK8sResource := gomonkey.ApplyFunc(BcsClusterInfoSvc.ListK8sResource, func(b BcsClusterInfoSvc, group, version, resource string) (*unstructured.UnstructuredList, error) {
		var target unstructured.UnstructuredList
		unstructured.UnstructuredJSONScheme.Decode(data, &schema.GroupVersionKind{models.BcsResourceGroupName, models.BcsResourceVersion, models.BcsResourceDataIdResourceKind}, &target)
		return &target, nil
	})

	defer patchListK8sResource.Reset()
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
	db := mysql.GetDBSession().DB
	db.Delete(&bcs.PodMonitorInfo{}, "cluster_id = ?", cluster.ClusterID)
	svc := NewBcsClusterInfoSvc(cluster)
	err := NewPodMonitorInfoSvc(nil).RefreshResource(&svc, cluster.K8sMetricDataID)
	assert.Nil(t, err)
	var results []bcs.PodMonitorInfo
	err = bcs.NewPodMonitorInfoQuerySet(db).ClusterIDEq(cluster.ClusterID).All(&results)
	assert.Nil(t, err)
	assert.Equal(t, 3, len(results))
	// 删除一个
	data = []byte(`{"apiVersion":"monitoring.coreos.com/v1","items":[{"apiVersion":"monitoring.coreos.com/v1","kind":"PodMonitor","metadata":{"annotations":{"meta.helm.sh/release-name":"bkbase-dgraph","meta.helm.sh/release-namespace":"bkbase"},"creationTimestamp":"2023-10-26T09:15:55Z","generation":1,"labels":{"app.kubernetes.io/managed-by":"Helm"},"managedFields":[{"apiVersion":"monitoring.coreos.com/v1","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:meta.helm.sh/release-name":{},"f:meta.helm.sh/release-namespace":{}},"f:labels":{".":{},"f:app.kubernetes.io/managed-by":{}}},"f:spec":{".":{},"f:endpoints":{},"f:namespaceSelector":{".":{},"f:any":{}},"f:selector":{".":{},"f:matchLabels":{".":{},"f:app":{},"f:chart":{},"f:heritage":{},"f:release":{}}}}},"manager":"helm","operation":"Update","time":"2023-10-26T09:15:55Z"}],"name":"bkbase-dgraph-bkbase-dgr-zero","namespace":"bkbase","resourceVersion":"10807858054","selfLink":"/apis/monitoring.coreos.com/v1/namespaces/bkbase/podmonitors/bkbase-dgraph-bkbase-dgr-zero","uid":"86525481-3a3b-466f-81ed-1cdc59b3c92b"},"spec":{"endpoints":[{"interval":"15s","path":"/debug/prometheus_metrics","port":"zero-http"}],"namespaceSelector":{"any":true},"selector":{"matchLabels":{"app":"bkbase-dgraph","chart":"bkbase-dgraph-0.0.9","heritage":"Helm","release":"bkbase-dgraph"}}}},{"apiVersion":"monitoring.coreos.com/v1","kind":"PodMonitor","metadata":{"annotations":{"meta.helm.sh/release-name":"bkbase-jobnavischeduler","meta.helm.sh/release-namespace":"bkbase"},"creationTimestamp":"2023-10-26T10:20:11Z","generation":1,"labels":{"app.kubernetes.io/managed-by":"Helm"},"managedFields":[{"apiVersion":"monitoring.coreos.com/v1","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{".":{},"f:meta.helm.sh/release-name":{},"f:meta.helm.sh/release-namespace":{}},"f:labels":{".":{},"f:app.kubernetes.io/managed-by":{}}},"f:spec":{".":{},"f:endpoints":{},"f:namespaceSelector":{".":{},"f:any":{}},"f:selector":{".":{},"f:matchLabels":{".":{},"f:app.kubernetes.io/service-type":{},"f:k8s-app":{},"f:meta.helm.sh/release-name":{}}}}},"manager":"helm","operation":"Update","time":"2023-10-26T10:20:11Z"}],"name":"bkbase-jobnavischeduler","namespace":"bkbase","resourceVersion":"10809974244","selfLink":"/apis/monitoring.coreos.com/v1/namespaces/bkbase/podmonitors/bkbase-jobnavischeduler","uid":"6b788376-2566-42df-a852-d5ef20ceec2a"},"spec":{"endpoints":[{"interval":"20s","path":"/metrics?","port":"metrics"}],"namespaceSelector":{"any":true},"selector":{"matchLabels":{"app.kubernetes.io/service-type":"metrics","k8s-app":"bkbase-jobnavischeduler","meta.helm.sh/release-name":"bkbase-jobnavischeduler"}}}}],"kind":"PodMonitorList","metadata":{"continue":"","resourceVersion":"10995632417","selfLink":"/apis/monitoring.coreos.com/v1/podmonitors"}}`)
	err = NewPodMonitorInfoSvc(nil).RefreshResource(&svc, cluster.K8sMetricDataID)
	assert.Nil(t, err)
	err = bcs.NewPodMonitorInfoQuerySet(db).ClusterIDEq(cluster.ClusterID).All(&results)
	assert.Nil(t, err)
	assert.Equal(t, 2, len(results))
}

func TestPodMonitorInfoSvc_GetConfigName(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB

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
	db.Delete(&cluster, "cluster_id = ?", cluster.ClusterID)
	err := cluster.Create(db)
	assert.NoError(t, err)
	pmi := &bcs.PodMonitorInfo{
		BCSResource: bcs.BCSResource{
			ClusterID:          cluster.ClusterID,
			Name:               "podtest",
			IsCustomResource:   false,
			IsCommonDataId:     false,
			RecordCreateTime:   time.Now(),
			ResourceCreateTime: time.Now(),
		},
	}
	configName, err := NewPodMonitorInfoSvc(pmi).GetConfigName()
	assert.NoError(t, err)
	assert.Equal(t, "custom-metric-podtest-system", configName)
	config.BcsClusterBkEnvLabel = "bcsLabel"
	configName, err = NewPodMonitorInfoSvc(pmi).GetConfigName()
	assert.NoError(t, err)
	assert.Equal(t, "bcsLabel-custom-metric-podtest-system", configName)
}
