// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package relation

// sourceInferenceOrder 按实体粒度从细到粗排列内置资源。省略源类型时，维度可能同时
// 完整命中主键字段数相同的多个资源，例如 Prometheus 的 job、service 标签与同名资源
// 重合。维度同时指明多个实体时，最细的实体才是观测对象，粗粒度资源会展开到它的全部
// 成员。新增内置资源必须在这里给出位置。
var sourceInferenceOrder = []string{
	"container",
	"pod",
	"job",
	"replicaset",
	"statefulset",
	"daemonset",
	"deployment",
	"service",
	"ingress",
	"apm_service_instance",
	"node",
	"k8s_address",
	"domain",
	"apm_service",
	"app_version",
	"bklogconfig",
	"git_commit",
	"p4_changelist",
	"svn_revision",
	"system",
	"host",
	"module",
	"set",
	"biz",
	"business",
	"datasource",
}

var sourceInferenceRank = func() map[string]int {
	rank := make(map[string]int, len(sourceInferenceOrder))
	for index, name := range sourceInferenceOrder {
		rank[name] = index
	}
	return rank
}()

// SourceInferenceLess 给出主键字段数相同的候选资源的推断顺序：内置资源按粒度从细到粗，
// 业务自定义资源排在内置资源之后并按名称排序。结果只取决于资源名称。
func SourceInferenceLess(left, right string) bool {
	leftRank, leftBuiltin := sourceInferenceRank[left]
	rightRank, rightBuiltin := sourceInferenceRank[right]
	if leftBuiltin && rightBuiltin {
		return leftRank < rightRank
	}
	if leftBuiltin != rightBuiltin {
		return leftBuiltin
	}
	return left < right
}
