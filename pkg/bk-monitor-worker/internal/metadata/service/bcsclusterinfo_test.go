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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/jinzhu/gorm"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/bcs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
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
	config.InitConfig()
	var createCount, updateCount int
	patchDBSession := gomonkey.ApplyFunc(mysql.GetDBSession, func() *mysql.DBSession {
		db, err := gorm.Open(viper.GetString("test.database.type"), fmt.Sprintf(
			"%s:%s@tcp(%s:%s)/%s?&parseTime=True&loc=Local",
			viper.GetString("test.database.user"),
			viper.GetString("test.database.password"),
			viper.GetString("test.database.host"),
			viper.GetString("test.database.port"),
			viper.GetString("test.database.db_name"),
		))
		assert.Nil(t, err)
		return &mysql.DBSession{DB: db}
	})
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
	defer patchDBSession.Reset()
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
