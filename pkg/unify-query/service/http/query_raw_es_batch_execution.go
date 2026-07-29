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
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/elasticsearch"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/prometheus"
)

type rawQueryExecutionSink struct {
	dataCh             chan<- map[string]any
	errCh              chan<- error
	resultTableOptions metadata.ResultTableOptions
	successedPaths     *atomic.Uint32
	total              *int64
	lock               *sync.Mutex
	allLabelMap        map[string][]function.LabelMapValue
	allFieldsMap       metadata.FieldsMap
}

func (s *rawQueryExecutionSink) mergeLabelMap(labelMap map[string][]function.LabelMapValue) {
	s.lock.Lock()
	defer s.lock.Unlock()
	for key, values := range labelMap {
		if _, ok := s.allLabelMap[key]; !ok {
			s.allLabelMap[key] = make([]function.LabelMapValue, 0)
		}
		s.allLabelMap[key] = append(s.allLabelMap[key], values...)
	}
}

func (s *rawQueryExecutionSink) mergeFieldsMap(fieldsMap metadata.FieldsMap) {
	if fieldsMap == nil {
		return
	}
	s.lock.Lock()
	defer s.lock.Unlock()
	for key, value := range fieldsMap {
		if _, ok := s.allFieldsMap[key]; !ok {
			s.allFieldsMap[key] = value
		}
	}
}

func (s *rawQueryExecutionSink) recordSuccess(
	query *metadata.Query,
	total int64,
	option *metadata.ResultTableOption,
) {
	s.successedPaths.Add(1)
	s.lock.Lock()
	s.resultTableOptions.SetOption(query.TableUUID(), option)
	s.lock.Unlock()
	atomic.AddInt64(s.total, total)
}

func (s *rawQueryExecutionSink) guardTask(task func()) func() {
	return func() {
		defer func() {
			if recover() != nil {
				s.errCh <- fmt.Errorf("query raw execution task panicked")
			}
		}()
		task()
	}
}

type rawESBatchRuntimeMember struct {
	location rawESBatchMemberLocation
	query    *metadata.Query
	instance tsdb.Instance
	es       *elasticsearch.Instance
	prepared *elasticsearch.PreparedRawQuery
}

func runRawQueryExecutionProducer(
	dataCh chan map[string]any,
	errCh chan error,
	execute func(),
) {
	defer close(dataCh)
	defer close(errCh)
	defer func() {
		if recover() != nil {
			errCh <- fmt.Errorf("query raw ES batch execution panicked")
		}
	}()
	execute()
}

// rawQueryDispatcher applies the request-wide routing bound to both preparation
// and execution. Phases are submitted sequentially, so worker tasks never wait
// for nested work in the same pool.
type rawQueryDispatcher struct {
	limit int
}

func (d rawQueryDispatcher) run(tasks []func()) {
	if len(tasks) == 0 {
		return
	}

	if d.limit <= 0 {
		var wg sync.WaitGroup
		wg.Add(len(tasks))
		for _, task := range tasks {
			task := task
			go func() {
				defer wg.Done()
				task()
			}()
		}
		wg.Wait()
		return
	}

	workerCount := min(d.limit, len(tasks))
	taskCh := make(chan func())
	var wg sync.WaitGroup
	wg.Add(workerCount)
	for range workerCount {
		go func() {
			defer wg.Done()
			for task := range taskCh {
				task()
			}
		}()
	}
	for _, task := range tasks {
		taskCh <- task
	}
	close(taskCh)
	wg.Wait()
}

// executeQueryRawWithESBatch preserves the query_raw aggregation contract while
// replacing only eligible, body-equivalent direct ES searches with _msearch.
func executeQueryRawWithESBatch(
	ctx context.Context,
	queryTs *structured.QueryTs,
	queryRef metadata.QueryReference,
	settings queryRawESBatchSettings,
	sink *rawQueryExecutionSink,
) {
	ctx, span := trace.NewSpan(ctx, "query-raw-es-batch")
	startedAt := time.Now()
	var spanErr error
	metric.QueryRawESBatchEventInc(ctx, metric.QueryRawESBatchEventPlannerEnabled)

	qb := metadata.GetQueryParams(ctx)
	dispatcher := rawQueryDispatcher{limit: QueryMaxRouting}
	queryCount := queryRef.Count()

	var (
		allMembers      []*rawESBatchRuntimeMember
		candidateInputs []rawESBatchPlanInput
		runtimeByLoc    = make(map[rawESBatchMemberLocation]*rawESBatchRuntimeMember)
		candidateGroups []rawESBatchPlanGroup
		esMemberCount   int
		preGroupCount   int
		finalGroupCount int
		batchCount      int
		singleCount     int
	)
	defer func() {
		span.Set("es_batch_enabled", true)
		span.Set("es_batch_candidate_members", len(candidateInputs))
		span.Set("es_batch_pre_groups", preGroupCount)
		span.Set("es_batch_final_groups", finalGroupCount)
		span.Set("es_batch_requests", batchCount)
		span.Set("es_batch_single_fallbacks", singleCount)
		metric.QueryRawESBatchDurationObserve(
			ctx,
			metric.QueryRawESBatchDurationTotal,
			time.Since(startedAt),
		)
		span.End(&spanErr)
	}()

	referenceNames := make([]string, 0, len(queryRef))
	for referenceName := range queryRef {
		referenceNames = append(referenceNames, referenceName)
	}
	sort.Strings(referenceNames)

	for _, referenceName := range referenceNames {
		references := queryRef[referenceName]
		for referenceIndex, reference := range references {
			if reference == nil {
				continue
			}
			for queryIndex, query := range stableRawESBatchQueries(reference.QueryList) {
				if query == nil {
					continue
				}

				localQuery := *query
				localQuery.ResultTableOption = query.ResultTableOption.Clone()
				if queryTs.IsSearchAfter && len(queryTs.ResultTableOptions) > 0 {
					if localQuery.ResultTableOption == nil ||
						len(localQuery.ResultTableOption.SearchAfter) == 0 {
						continue
					}
				}

				sink.mergeLabelMap(function.LabelMap(ctx, &localQuery))
				if queryCount > 1 && !queryTs.IsMultiFrom {
					localQuery.Size += localQuery.From
					localQuery.From = 0
				}

				location := rawESBatchMemberLocation{
					referenceName:  referenceName,
					referenceIndex: referenceIndex,
					queryIndex:     queryIndex,
				}
				instance := prometheus.GetTsDbInstance(ctx, &localQuery)
				member := &rawESBatchRuntimeMember{
					location: location,
					query:    &localQuery,
					instance: instance,
				}
				allMembers = append(allMembers, member)
				runtimeByLoc[location] = member

				esInstance, isES := instance.(*elasticsearch.Instance)
				if !isES {
					continue
				}

				member.es = esInstance
				esMemberCount++
				if !rawESBatchEligible(settings, &localQuery) {
					continue
				}
				candidateInputs = append(candidateInputs, rawESBatchPlanInput{
					location:      location,
					connectionKey: esInstance.RawBatchConnectionKey(ctx),
					query:         &localQuery,
				})
			}
		}
	}
	metric.QueryRawESBatchEventAdd(
		ctx,
		metric.QueryRawESBatchEventPlannerCandidate,
		len(candidateInputs),
	)

	plan, planErr := planRawESBatch(settings, candidateInputs)
	if planErr != nil {
		plan = nil
	}

	preGroupCount = len(plan)
	candidateGroups = make([]rawESBatchPlanGroup, 0, len(plan))
	candidateLocations := make(map[rawESBatchMemberLocation]struct{}, len(candidateInputs))
	for _, group := range plan {
		if group.execution != rawESBatchExecutionCandidateGroup {
			continue
		}
		candidateGroups = append(candidateGroups, group)
		for _, plannedMember := range group.members {
			candidateLocations[plannedMember.location] = struct{}{}
		}
	}
	metric.QueryRawESBatchEventAdd(
		ctx,
		metric.QueryRawESBatchEventPlannerPreGroup,
		len(plan),
	)
	metric.QueryRawESBatchEventAdd(
		ctx,
		metric.QueryRawESBatchEventPlannerIneligible,
		esMemberCount-len(candidateInputs),
	)
	metric.QueryRawESBatchEventAdd(
		ctx,
		metric.QueryRawESBatchEventPlannerPreSingle,
		len(candidateInputs)-len(candidateLocations),
	)
	singleCount = esMemberCount - len(candidateLocations)
	metric.QueryRawESBatchEventAdd(
		ctx,
		metric.QueryRawESBatchEventPlannerSingle,
		singleCount,
	)

	phaseOneTasks := make([]func(), 0, len(allMembers))
	for _, member := range allMembers {
		member := member
		if _, isCandidate := candidateLocations[member.location]; isCandidate {
			phaseOneTasks = append(phaseOneTasks, sink.guardTask(func() {
				prepareStartedAt := time.Now()
				defer func() {
					metric.QueryRawESBatchDurationObserve(
						ctx,
						metric.QueryRawESBatchDurationPrepare,
						time.Since(prepareStartedAt),
					)
				}()
				var prefetched *elasticsearch.PreparedFieldMetadata
				if queryTs.HighLight != nil && queryTs.HighLight.Enable {
					fieldMetadata, fieldErr := member.es.PrepareRawFieldMetadata(
						ctx, member.query, qb.Start, qb.End,
					)
					if fieldErr == nil {
						prefetched = fieldMetadata
						sink.mergeFieldsMap(fieldMetadata.FieldsMap())
					}
				}

				prepared, prepareErr := member.es.PrepareRawQuery(
					ctx, member.query, qb.Start, qb.End, prefetched,
				)
				if prepareErr != nil {
					sink.errCh <- prepareErr
					return
				}
				member.prepared = prepared
				if prefetched == nil && queryTs.HighLight != nil && queryTs.HighLight.Enable {
					sink.mergeFieldsMap(prepared.FieldsMap())
				}
			}))
			continue
		}
		phaseOneTasks = append(phaseOneTasks, sink.guardTask(func() {
			executeRawDirectMember(ctx, queryTs, qb, member, sink)
		}))
	}
	dispatcher.run(phaseOneTasks)

	executionTasks := make([]func(), 0)

	for _, preGroup := range candidateGroups {
		type fingerprintGroup struct {
			members []*rawESBatchRuntimeMember
		}
		var (
			fingerprintGroups []fingerprintGroup
			fingerprintIndex  = make(map[string]int)
		)
		for _, plannedMember := range preGroup.members {
			member := runtimeByLoc[plannedMember.location]
			if member.prepared == nil {
				continue
			}
			fingerprint, fingerprintErr := elasticsearch.PreparedRawQueryFingerprint(member.prepared)
			if fingerprintErr != nil {
				singleCount++
				metric.QueryRawESBatchEventInc(
					ctx,
					metric.QueryRawESBatchEventPlannerSingle,
				)
				metric.QueryRawESBatchEventInc(
					ctx,
					metric.QueryRawESBatchEventPlannerFingerprintFallback,
				)
				executionTasks = append(executionTasks, sink.guardTask(func() {
					executeRawPreparedMember(ctx, member, sink)
				}))
				continue
			}
			index, ok := fingerprintIndex[fingerprint]
			if !ok {
				index = len(fingerprintGroups)
				fingerprintIndex[fingerprint] = index
				fingerprintGroups = append(fingerprintGroups, fingerprintGroup{})
			}
			fingerprintGroups[index].members = append(fingerprintGroups[index].members, member)
		}
		finalGroupCount += len(fingerprintGroups)
		metric.QueryRawESBatchEventAdd(
			ctx,
			metric.QueryRawESBatchEventPlannerFinalGroup,
			len(fingerprintGroups),
		)

		for _, finalGroup := range fingerprintGroups {
			if len(finalGroup.members) < 2 {
				member := finalGroup.members[0]
				singleCount++
				metric.QueryRawESBatchEventInc(ctx, metric.QueryRawESBatchEventPlannerSingle)
				metric.QueryRawESBatchEventInc(ctx, metric.QueryRawESBatchEventPlannerFinalSplit)
				executionTasks = append(executionTasks, sink.guardTask(func() {
					executeRawPreparedMember(ctx, member, sink)
				}))
				continue
			}

			rawMembers := make([]elasticsearch.RawBatchMember, 0, len(finalGroup.members))
			membersByOrdinal := make(map[int]*rawESBatchRuntimeMember, len(finalGroup.members))
			for _, member := range finalGroup.members {
				ordinal := len(membersByOrdinal)
				rawMembers = append(rawMembers, elasticsearch.RawBatchMember{
					Ordinal:  ordinal,
					Prepared: member.prepared,
				})
				membersByOrdinal[ordinal] = member
			}
			batches, oversized, packErr := elasticsearch.PackRawBatchMembers(
				rawMembers, settings.maxMembers, settings.maxBodyBytes,
			)
			if packErr != nil {
				singleCount += len(finalGroup.members)
				metric.QueryRawESBatchEventAdd(
					ctx,
					metric.QueryRawESBatchEventPlannerSingle,
					len(finalGroup.members),
				)
				metric.QueryRawESBatchEventAdd(
					ctx,
					metric.QueryRawESBatchEventPlannerPackError,
					len(finalGroup.members),
				)
				for _, member := range finalGroup.members {
					member := member
					executionTasks = append(executionTasks, sink.guardTask(func() {
						executeRawPreparedMember(ctx, member, sink)
					}))
				}
				continue
			}

			for _, batch := range batches {
				batch := batch
				if batch.MemberCount() < 2 {
					member := membersByOrdinal[batch.Members()[0].Ordinal]
					singleCount++
					metric.QueryRawESBatchEventInc(ctx, metric.QueryRawESBatchEventPlannerSingle)
					metric.QueryRawESBatchEventInc(ctx, metric.QueryRawESBatchEventPlannerPackedSingle)
					executionTasks = append(executionTasks, sink.guardTask(func() {
						executeRawPreparedMember(ctx, member, sink)
					}))
					continue
				}
				batchCount++
				metric.QueryRawESBatchEventInc(ctx, metric.QueryRawESBatchEventPlannerBatch)
				executionTasks = append(executionTasks, sink.guardTask(func() {
					results, batchErr := finalGroup.members[0].es.ExecuteRawBatch(
						ctx, batch, settings.maxConcurrentSearches,
					)
					if batchErr != nil {
						sink.errCh <- batchErr
						return
					}
					for _, result := range results {
						member := membersByOrdinal[result.Ordinal]
						if result.Err != nil {
							sink.errCh <- result.Err
							continue
						}
						for _, row := range result.Rows {
							sink.dataCh <- row
						}
						sink.recordSuccess(member.query, result.Total, result.Option)
					}
				}))
			}
			for _, oversizedMember := range oversized {
				member := membersByOrdinal[oversizedMember.Member.Ordinal]
				singleCount++
				metric.QueryRawESBatchEventInc(ctx, metric.QueryRawESBatchEventPlannerSingle)
				metric.QueryRawESBatchEventInc(ctx, metric.QueryRawESBatchEventPlannerBodyOversized)
				executionTasks = append(executionTasks, sink.guardTask(func() {
					executeRawPreparedMember(ctx, member, sink)
				}))
			}
		}
	}

	dispatcher.run(executionTasks)
}

func stableRawESBatchQueries(queries metadata.QueryList) metadata.QueryList {
	ordered := append(metadata.QueryList(nil), queries...)
	sort.SliceStable(ordered, func(leftIndex, rightIndex int) bool {
		left, right := ordered[leftIndex], ordered[rightIndex]
		if left == nil || right == nil {
			return left != nil
		}
		leftKeys := []string{
			left.TableUUID(),
			left.DB,
			left.StorageUUID(),
			left.TableID,
			left.DataLabel,
		}
		rightKeys := []string{
			right.TableUUID(),
			right.DB,
			right.StorageUUID(),
			right.TableID,
			right.DataLabel,
		}
		for index := range leftKeys {
			if leftKeys[index] != rightKeys[index] {
				return leftKeys[index] < rightKeys[index]
			}
		}

		leftFingerprint, leftErr := rawESBatchSemanticFingerprint(left)
		rightFingerprint, rightErr := rawESBatchSemanticFingerprint(right)
		if leftErr != nil || rightErr != nil {
			return leftErr == nil && rightErr != nil
		}
		return string(leftFingerprint[:]) < string(rightFingerprint[:])
	})
	return ordered
}

func executeRawDirectMember(
	ctx context.Context,
	queryTs *structured.QueryTs,
	qb *metadata.QueryParams,
	member *rawESBatchRuntimeMember,
	sink *rawQueryExecutionSink,
) {
	if member.instance == nil {
		sink.errCh <- metadata.NewMessage(
			metadata.MsgQueryRaw,
			"查询实例为空",
		).Error(ctx, nil)
		return
	}

	if queryTs.HighLight != nil && queryTs.HighLight.Enable {
		if fieldsMap, err := member.instance.QueryFieldMap(
			ctx, member.query, qb.Start, qb.End,
		); err == nil {
			sink.mergeFieldsMap(fieldsMap)
		}
	}

	_, total, option, err := member.instance.QueryRawData(
		ctx, member.query, qb.Start, qb.End, sink.dataCh,
	)
	if err != nil {
		sink.errCh <- err
		return
	}
	sink.recordSuccess(member.query, total, option)
}

func executeRawPreparedMember(
	ctx context.Context,
	member *rawESBatchRuntimeMember,
	sink *rawQueryExecutionSink,
) {
	_, total, option, err := member.es.QueryPreparedRawData(ctx, member.prepared, sink.dataCh)
	if err != nil {
		sink.errCh <- err
		return
	}
	sink.recordSuccess(member.query, total, option)
}
