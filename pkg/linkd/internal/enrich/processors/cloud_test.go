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
	"context"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/rules"
)

func TestCloudResourceProcessorUsesCompositeIdentity(t *testing.T) {
	t.Parallel()
	reader := &cloudTestReader{resource: models.CloudResource{TenantID: "tenant-a", CloudID: "aws-1", ResourceType: "ec2", InstanceID: "i-123", Name: "web-1", CloudName: "生产云", ObjectModelCode: "cw-AWS_EC2", ModelInstanceID: "resource-9"}, found: true}
	alert := processorBaseTargetAlert(t, domain.DimensionMap{rules.FieldCloudID: domain.NewStringScalar("aws-1"), rules.FieldInstanceID: domain.NewStringScalar("i-123"), rules.FieldTargetType: domain.NewStringScalar("ec2")})
	chain, err := enrich.NewChain([]enrich.Processor{CloudResourceProcessor{}}, enrich.Sources{CWStrategy: reader, CloudResource: reader})
	if err != nil {
		t.Fatal(err)
	}
	result, err := chain.Enrich(context.Background(), enrich.Input{Alert: alert})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := enrich.DecodePayload(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	resource := payload.Processors[0]["cloud_resource"]
	if result.Status != domain.EnrichStatusSucceeded || resource.Status != domain.EnrichStatusSucceeded || reader.tenant != "tenant-a" || reader.cloudID != "aws-1" || reader.resourceType != "ec2" || reader.instanceID != "i-123" {
		t.Fatalf("status=%q resource=%#v reader=%#v", result.Status, resource, reader)
	}
}

func TestCloudResourceProcessorRejectsResponseIdentity(t *testing.T) {
	t.Parallel()
	reader := &cloudTestReader{resource: models.CloudResource{TenantID: "tenant-b", CloudID: "aws-1", ResourceType: "ec2", InstanceID: "i-123"}, found: true}
	alert := processorBaseTargetAlert(t, domain.DimensionMap{rules.FieldCloudID: domain.NewStringScalar("aws-1"), rules.FieldInstanceID: domain.NewStringScalar("i-123"), rules.FieldTargetType: domain.NewStringScalar("ec2")})
	chain, err := enrich.NewChain([]enrich.Processor{CloudResourceProcessor{}}, enrich.Sources{CWStrategy: reader, CloudResource: reader})
	if err != nil {
		t.Fatal(err)
	}
	result, err := chain.Enrich(context.Background(), enrich.Input{Alert: alert})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != domain.EnrichStatusFailed {
		t.Fatalf("status=%q", result.Status)
	}
}

type cloudTestReader struct {
	resource                                  models.CloudResource
	found                                     bool
	tenant, cloudID, resourceType, instanceID string
}

func (r *cloudTestReader) GetByBKStrategyID(context.Context, string, int64) (models.CWStrategy, bool, error) {
	kind := models.CWStrategyKindCloud
	biz := int64(2)
	return models.CWStrategy{Kind: kind, BKBizID: &biz, Spec: models.CWStrategySpec{ConfigType: models.CWStrategyConfigTypeData, StrategyItem: &models.CWStrategyItem{QueryConfigs: []models.StrategyQueryConfig{{ResultTableID: "cloud.table", MetricField: "usage"}}}}}, true, nil
}

func (r *cloudTestReader) GetCloudResource(_ context.Context, tenantID, cloudID, resourceType, instanceID string) (models.CloudResource, bool, error) {
	r.tenant, r.cloudID, r.resourceType, r.instanceID = tenantID, cloudID, resourceType, instanceID
	return r.resource, r.found, nil
}
