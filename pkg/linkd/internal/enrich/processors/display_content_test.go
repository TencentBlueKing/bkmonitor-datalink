// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package processors

import (
	"encoding/json"
	"reflect"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/rules"
	"linkd/internal/enrich/view"
	"linkd/internal/store/storetest"
)

func TestDisplayPreservesCreatedContentForDataAndLogs(t *testing.T) {
	t.Parallel()
	for _, scene := range []string{"data", "log_metric", "log_keyword"} {
		t.Run(scene, func(t *testing.T) {
			var sources enrich.Sources
			content := "AVG(status) >= 1, current value is 1"
			if scene == "data" {
				reader := &dataSliceReader{model: "cw-MySQL", metricValueMapping: []models.MetricValueMapping{{OriginalValue: "1", MappedValue: "正常"}}}
				sources = enrich.Sources{CWStrategy: reader, Metric: reader, Model: reader, OneModel: reader, AlarmSource: reader}
			} else {
				itemType := models.CWMonitorItemTypeLog
				if scene == "log_keyword" {
					itemType = models.CWMonitorItemTypeLogKeyword
				}
				reader := &logTestReader{strategy: logStrategy(itemType, "error"), source: models.AlarmSource{Id: "log_source", Name: "日志源"}}
				sources = enrich.Sources{CWStrategy: reader, Metric: reader, Model: reader, OneModel: reader, AlarmSource: reader}
				content = "keyword(error) >= 2, 当前值 3,关联信息：host=web"
			}
			alert := processorBaseTargetAlert(t, domain.DimensionMap{})
			alert.Content = content
			alert.SubjectName = "日志主题"
			for _, preserve := range []bool{false, true} {
				chain, err := enrich.NewChain([]enrich.Processor{Strategy{}, Resource{}, Display{PreserveContent: preserve}}, sources)
				if err != nil {
					t.Fatal(err)
				}
				result, err := chain.Enrich(t.Context(), enrich.Input{Event: alert})
				if err != nil {
					t.Fatal(err)
				}
				payload, err := enrich.DecodePayload(result.Data.Evaluations[0].Data)
				if err != nil {
					t.Fatal(err)
				}
				envelope := payload.Processors[2][rules.DisplayProcessor]
				if preserve {
					for _, patch := range envelope.Patches {
						if patch.Path == "$.content" {
							t.Fatal("created-content mode published a source content override")
						}
					}
					continue
				}
				var got string
				if err := json.Unmarshal(envelope.Value["content"], &got); err != nil {
					t.Fatal(err)
				}
				if (got == content) != preserve || alert.Content != content {
					t.Fatalf("preserve=%t display=%q core=%q", preserve, got, alert.Content)
				}
			}
		})
	}
}

// 资源分支可以继续丰富标题和对象，但新模式下读取视图必须保留已生成的核心内容。
// 这些是隔离的处理器验证，不代替对应场景的线上检测事实对照。
func TestDisplayCreatedContentAcrossResourceScenes(t *testing.T) {
	t.Parallel()
	for _, scene := range []string{"target_monitor_source", "target_no_data", "target_system_metric", "target_basic", "uptime", "cloud", "k8s", "apm"} {
		t.Run(scene, func(t *testing.T) {
			alert := processorBaseTargetAlert(t, domain.DimensionMap{})
			var sources enrich.Sources
			switch scene {
			case "target_monitor_source", "target_no_data", "target_system_metric", "target_basic":
				modelCode := rules.HostModelCode
				reader := processorHostReader()
				switch scene {
				case "target_monitor_source":
					alert.Dimensions[rules.FieldBKInstID] = processorNumber(t, 101)
				case "target_no_data":
					modelCode, reader = "cw-Service", processorServiceReader()
					alert.Dimensions = domain.DimensionMap{rules.FieldNoDataDimension: domain.NewBoolScalar(true), rules.FieldModelID: domain.NewStringScalar(modelCode), rules.FieldModelInstID: processorNumber(t, 2)}
				case "target_system_metric":
					alert.Dimensions = domain.DimensionMap{rules.FieldBKTargetIP: domain.NewStringScalar("10.0.0.1"), rules.FieldBKTargetCloudID: processorNumber(t, 0)}
				default:
					modelCode = "cw-Disk"
					reader = &processorBaseTargetReader{model: processorModel("tenant-a", modelCode, "磁盘", "disk"), modelFound: true}
				}
				sources = enrich.Sources{CWStrategy: processorBaseTargetStrategy{modelCode: modelCode}, Metric: processorBaseTargetMetric{}, Model: reader, OneModel: reader, CollectTopology: reader}
			case "uptime":
				alert = uptimeProcessorAlert(t)
				reader := &processorUptimeReader{task: models.UptimeTask{ID: 55, TaskID: 10079, Name: "拨测任务", Protocol: models.UptimeProtocolICMP, BKBizID: 2, BKTenantID: "tenant-a"}, node: models.UptimeNode{ID: 8, Name: "北京节点", IP: "10.0.0.8", BKTenantID: "tenant-a"}}
				sources = enrich.Sources{CWStrategy: processorUptimeStrategy{}, Metric: processorUptimeMetric{}, Model: reader, OneModel: reader, Uptime: reader, UptimeNode: reader}
			case "cloud":
				alert.Dimensions = domain.DimensionMap{rules.FieldCloudID: domain.NewStringScalar("aws-1"), rules.FieldInstanceID: domain.NewStringScalar("i-123"), rules.FieldTargetType: domain.NewStringScalar("ec2")}
				reader := &cloudTestReader{resource: models.CloudResource{TenantID: "tenant-a", CloudID: "aws-1", ResourceType: "ec2", InstanceID: "i-123", Name: "web-1", ObjectModelCode: "cw-AWS_EC2", ModelInstanceID: "resource-9"}, found: true}
				sources = enrich.Sources{CWStrategy: reader, CloudResource: reader, Metric: processorBaseTargetMetric{}}
			case "k8s":
				alert.Dimensions = domain.DimensionMap{rules.FieldBCSClusterID: domain.NewStringScalar("cluster-1"), rules.FieldNamespace: domain.NewStringScalar("default"), rules.FieldService: domain.NewStringScalar("web")}
				sources = enrich.Sources{CWStrategy: k8sStrategyReader{}, Metric: processorBaseTargetMetric{}}
			case "apm":
				alert.Dimensions = domain.DimensionMap{"apm_app_id": domain.NewStringScalar("17"), rules.FieldServiceName: domain.NewStringScalar("checkout"), rules.FieldAPMInstanceID: domain.NewStringScalar("instance-a")}
				reader := apmTestReader{}
				sources = enrich.Sources{CWStrategy: reader, Metric: reader, Model: reader, OneModel: reader, APMApplication: reader}
			}
			alert.Content = "AVG(usage) >= 1%, 当前值 2%,关联信息：保留原文"
			original := alert.Clone()
			chain, err := enrich.NewChain([]enrich.Processor{Strategy{}, Resource{}, Display{PreserveContent: true}}, sources)
			if err != nil {
				t.Fatal(err)
			}
			result, err := chain.Enrich(t.Context(), enrich.Input{Event: alert})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(alert, original) {
				t.Fatal("enrichment changed its input Alert")
			}
			payload, err := enrich.DecodePayload(result.Data.Evaluations[0].Data)
			if err != nil {
				t.Fatal(err)
			}
			display := payload.Processors[2][rules.DisplayProcessor]

			if display.Status != domain.EnrichStatusSucceeded {
				t.Fatalf("unexpected display status %s", display.Status)
			}
			for _, patch := range display.Patches {
				if patch.Path == "$.content" {
					t.Fatal("source content overrides generated Alert content")
				}
			}
			opening := storetest.Alert(alert.BKTenantID, "created", alert.EventID, alert.Fingerprint, alert.Evaluations[0].Severity)
			opening.Content = "generated: immutable"
			opening.EnrichStatus, opening.Enrich = result.Data.Evaluations[0].Status, result.Data.Evaluations[0].Data
			projected, err := view.EnrichedAlert(opening)
			if err != nil {
				t.Fatal(err)
			}
			if projected.Content != opening.Content {
				t.Fatalf("projected content=%q", projected.Content)
			}
		})
	}
}
