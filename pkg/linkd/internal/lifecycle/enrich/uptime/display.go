// Tencent is pleased to support the open source community. All rights reserved.
// Licensed under the MIT License.

package uptime

import (
	"context"
	"fmt"
	"strconv"

	"linkd/internal/domain"
	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/models"
	"linkd/internal/lifecycle/enrich/rules"
)

type displayProjection struct {
	Object      string
	Dimensions  []models.DimensionDisplay
	Diagnostics []enrich.Diagnostic
}

func projectDisplay(
	ctx context.Context,
	resolution Resolution,
	dimensions domain.DimensionMap,
	resource models.ResourceValues,
) (displayProjection, error) {
	if err := ctx.Err(); err != nil {
		return displayProjection{}, err
	}
	projection := displayProjection{
		Dimensions:  make([]models.DimensionDisplay, 0, 8),
		Diagnostics: make([]enrich.Diagnostic, 0, 1),
	}

	taskValue := resolution.taskRaw
	if resolution.taskInputState == taskInputValid && resolution.taskErr == nil &&
		resolution.taskFound && validTask(resolution) && resolution.task.Name != "" {
		taskValue = domain.NewStringScalar(resolution.task.Name)
		projection.Object = resolution.task.Name
	}
	if projection.Object == "" {
		projection.Object = taskRawText(resolution) + "（该拨测任务已被删除）"
	}
	taskKey := rules.FieldTaskID
	taskRaw := resolution.taskRaw
	projection.Dimensions = append(projection.Dimensions, models.DimensionDisplay{
		Name: "任务", Value: taskValue, RealKey: &taskKey, RealValue: &taskRaw,
	})

	if resolution.nodeProvided {
		value := resolution.nodeRaw
		switch {
		case resolution.nodeErr != nil:
			projection.Diagnostics = appendDependency(projection.Diagnostics, rules.DependencyUptime)
		case resolution.nodeFound && validNode(resolution) && resolution.node.Name != "":
			value = domain.NewStringScalar(resolution.node.Name)
		}
		realKey := rules.FieldNodeID
		realValue := resolution.nodeRaw
		projection.Dimensions = append(projection.Dimensions, models.DimensionDisplay{
			Name: "节点", Value: value, RealKey: &realKey, RealValue: &realValue,
		})
	}

	if resource.BKBizID != nil {
		bizID, ok := resource.BKBizID.(int64)
		if !ok {
			return displayProjection{}, fmt.Errorf("uptime business ID has invalid type %T", resource.BKBizID)
		}
		bizIDValue, err := domain.NewNumberScalar(float64(bizID))
		if err != nil {
			return displayProjection{}, err
		}
		bizValue := bizIDValue
		if resource.BKBizName != "" {
			bizValue = domain.NewStringScalar(resource.BKBizName)
		}
		realKey, realValue := rules.FieldBKBizID, bizIDValue
		projection.Dimensions = append(projection.Dimensions, models.DimensionDisplay{Name: "业务", Value: bizValue, RealKey: &realKey, RealValue: &realValue})
	}
	if value, exists := dimensions[rules.FieldBKTargetCloudID]; exists {
		realKey, realValue := rules.FieldBKTargetCloudID, value
		cloudValue := value
		if resolution.cloudErr == nil && resolution.cloudFound && validCloudHost(resolution) {
			if name, ok := rules.FirstStringField(resolution.cloudHost.Attributes, rules.FieldBKCloudName); ok {
				if cloudID, valid := targetCloudIdentityFromHost(resolution.cloudHost); valid && cloudID == resolution.cloudID {
					cloudValue = domain.NewStringScalar(name)
				}
			}
		}
		projection.Dimensions = append(projection.Dimensions, models.DimensionDisplay{
			Name: "云区域", Value: cloudValue, RealKey: &realKey, RealValue: &realValue,
		})
	}
	for _, field := range targetDimensions(resolution) {
		if value, exists := dimensions[field.key]; exists && rules.ScalarProvided(value) {
			projection.Dimensions = append(projection.Dimensions, models.DimensionDisplay{
				Name: field.name, Value: value,
			})
		}
	}
	return projection, nil
}

type targetDimension struct{ key, name string }

func targetDimensions(resolution Resolution) []targetDimension {
	if resolution.taskFound && resolution.taskErr == nil && validTask(resolution) {
		switch resolution.task.Protocol {
		case models.UptimeProtocolHTTP:
			return []targetDimension{{rules.FieldURL, "目标url"}}
		case models.UptimeProtocolTCP, models.UptimeProtocolUDP:
			return []targetDimension{{rules.FieldTargetPort, "目标端口"}, {rules.FieldTargetHost, "目标IP"}}
		}
	}
	return []targetDimension{{rules.FieldTarget, "目标地址"}, {rules.FieldTargetType, "目标地址类型"}}
}

func taskRawText(resolution Resolution) string {
	text := rules.DimensionText(domain.DimensionMap{rules.FieldTaskID: resolution.taskRaw}, rules.FieldTaskID)
	if text == "" {
		return "0"
	}
	return text
}

func validNode(resolution Resolution) bool {
	return resolution.node.ID > 0 && resolution.node.BKTenantID == resolution.tenantID &&
		resolution.nodeIdentity == strconv.FormatInt(resolution.node.PlatID, 10)+":"+resolution.node.IP
}
