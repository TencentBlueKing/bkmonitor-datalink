// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/elasticsearch"
)

type rawESBatchPageProjection struct {
	From        *int  `json:"from,omitempty"`
	SearchAfter []any `json:"search_after,omitempty"`
}

type rawESBatchSemanticProjection struct {
	SourceType      string                    `json:"source_type,omitempty"`
	DataSource      string                    `json:"data_source,omitempty"`
	MeasurementType string                    `json:"measurement_type,omitempty"`
	Field           string                    `json:"field,omitempty"`
	TimeField       metadata.TimeField        `json:"time_field,omitempty"`
	Timezone        string                    `json:"timezone,omitempty"`
	FieldAlias      metadata.FieldAlias       `json:"field_alias,omitempty"`
	Aggregates      metadata.Aggregates       `json:"aggregates,omitempty"`
	Condition       string                    `json:"condition,omitempty"`
	Filters         []map[string]string       `json:"filters,omitempty"`
	QueryString     string                    `json:"query_string,omitempty"`
	IsPrefix        bool                      `json:"is_prefix,omitempty"`
	AllConditions   metadata.AllConditions    `json:"all_conditions,omitempty"`
	Source          []string                  `json:"source,omitempty"`
	From            int                       `json:"from,omitempty"`
	Size            int                       `json:"size,omitempty"`
	Page            *rawESBatchPageProjection `json:"page,omitempty"`
	Orders          metadata.Orders           `json:"orders,omitempty"`
	Collapse        *metadata.Collapse        `json:"collapse,omitempty"`
}

func rawESBatchSemanticFingerprint(query *metadata.Query) ([sha256.Size]byte, error) {
	if query == nil {
		return [sha256.Size]byte{}, fmt.Errorf("query is nil")
	}

	projection := rawESBatchSemanticProjection{
		SourceType:      query.SourceType,
		DataSource:      query.DataSource,
		MeasurementType: query.MeasurementType,
		Field:           query.Field,
		TimeField:       query.TimeField,
		Timezone:        query.Timezone,
		FieldAlias:      query.FieldAlias,
		Aggregates:      query.Aggregates,
		Condition:       query.Condition,
		Filters:         query.Filters,
		QueryString:     query.QueryString,
		IsPrefix:        query.IsPrefix,
		AllConditions:   query.AllConditions,
		Source:          query.Source,
		From:            query.From,
		Size:            query.Size,
		Orders:          query.Orders,
		Collapse:        query.Collapse,
	}
	if query.ResultTableOption != nil {
		projection.Page = &rawESBatchPageProjection{
			From:        query.ResultTableOption.From,
			SearchAfter: query.ResultTableOption.SearchAfter,
		}
	}

	encoded, err := json.Marshal(projection)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(encoded), nil
}

func rawESBatchEligible(settings queryRawESBatchSettings, query *metadata.Query) bool {
	if query == nil {
		return false
	}
	if query.StorageType != metadata.ElasticsearchStorageType ||
		query.SourceType == structured.BkData ||
		query.IsElasticsearchIndexPrefixMissing() {
		return false
	}
	if query.Scroll != "" {
		return false
	}
	if option := query.ResultTableOption; option != nil {
		if option.ScrollID != "" || option.SliceMax > 0 || option.SliceIndex > 0 {
			return false
		}
	}
	return true
}

type rawESBatchExecution uint8

const (
	rawESBatchExecutionDirectSingle rawESBatchExecution = iota
	rawESBatchExecutionPreparedSingle
	rawESBatchExecutionCandidateGroup
)

// rawESBatchMemberLocation is assigned while collecting a QueryReference.
// Reference names are sorted, while the two indexes preserve the order of the
// corresponding slices. The tuple must be unique within one request.
type rawESBatchMemberLocation struct {
	referenceName  string
	referenceIndex int
	queryIndex     int
}

type rawESBatchPlanInput struct {
	location      rawESBatchMemberLocation
	connectionKey elasticsearch.RawBatchConnectionKey
	query         *metadata.Query
	prepared      bool
}

type rawESBatchPlanMember struct {
	ordinal       int
	location      rawESBatchMemberLocation
	connectionKey elasticsearch.RawBatchConnectionKey
	query         *metadata.Query
	prepared      bool
}

type rawESBatchPlanGroup struct {
	execution           rawESBatchExecution
	connectionKey       elasticsearch.RawBatchConnectionKey
	semanticFingerprint [sha256.Size]byte
	members             []rawESBatchPlanMember
}

type rawESBatchPreGroupKey struct {
	connectionKey       elasticsearch.RawBatchConnectionKey
	semanticFingerprint [sha256.Size]byte
	prepared            bool
}

// planRawESBatch is a pure pre-planner. It does not prepare or execute ES
// requests. Ineligible and uncertain members remain independent single-query
// tasks, while eligible members are grouped only by the opaque effective
// connection identity and the explicit pre-semantic fingerprint.
func planRawESBatch(
	settings queryRawESBatchSettings,
	inputs []rawESBatchPlanInput,
) ([]rawESBatchPlanGroup, error) {
	ordered := append([]rawESBatchPlanInput(nil), inputs...)
	sort.Slice(ordered, func(i, j int) bool {
		return rawESBatchMemberLocationLess(ordered[i].location, ordered[j].location)
	})

	for index := 1; index < len(ordered); index++ {
		if ordered[index-1].location == ordered[index].location {
			return nil, fmt.Errorf("duplicate raw ES batch member location")
		}
	}

	plan := make([]rawESBatchPlanGroup, 0, len(ordered))
	groupIndexes := make(map[rawESBatchPreGroupKey]int, len(ordered))
	for ordinal, input := range ordered {
		member := rawESBatchPlanMember{
			ordinal:       ordinal,
			location:      input.location,
			connectionKey: input.connectionKey,
			query:         input.query,
			prepared:      input.prepared,
		}

		if !rawESBatchEligible(settings, input.query) {
			plan = append(plan, rawESBatchSinglePlan(member))
			continue
		}

		fingerprint, err := rawESBatchSemanticFingerprint(input.query)
		if err != nil {
			plan = append(plan, rawESBatchSinglePlan(member))
			continue
		}
		key := rawESBatchPreGroupKey{
			connectionKey:       input.connectionKey,
			semanticFingerprint: fingerprint,
			prepared:            input.prepared,
		}
		groupIndex, ok := groupIndexes[key]
		if !ok {
			groupIndexes[key] = len(plan)
			plan = append(plan, rawESBatchPlanGroup{
				execution:           rawESBatchSingleExecution(member.prepared),
				connectionKey:       input.connectionKey,
				semanticFingerprint: fingerprint,
				members:             []rawESBatchPlanMember{member},
			})
			continue
		}

		plan[groupIndex].members = append(plan[groupIndex].members, member)
		plan[groupIndex].execution = rawESBatchExecutionCandidateGroup
	}

	return plan, nil
}

func rawESBatchMemberLocationLess(left, right rawESBatchMemberLocation) bool {
	if left.referenceName != right.referenceName {
		return left.referenceName < right.referenceName
	}
	if left.referenceIndex != right.referenceIndex {
		return left.referenceIndex < right.referenceIndex
	}
	return left.queryIndex < right.queryIndex
}

func rawESBatchSingleExecution(prepared bool) rawESBatchExecution {
	if prepared {
		return rawESBatchExecutionPreparedSingle
	}
	return rawESBatchExecutionDirectSingle
}

func rawESBatchSinglePlan(member rawESBatchPlanMember) rawESBatchPlanGroup {
	return rawESBatchPlanGroup{
		execution:     rawESBatchSingleExecution(member.prepared),
		connectionKey: member.connectionKey,
		members:       []rawESBatchPlanMember{member},
	}
}
