// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package structured

import (
	"context"
	"regexp"
	"sync"
)

// RouteCandidates 在一次请求内复用全空间扫表中的字段匹配：不带 table_id 的查询按字段名在空间的
// 全部结果表中找候选，每次扫描都要逐表读取详情，空间有数千张表时单次在十毫秒以上。
// TimeGraph 的一次拓扑请求对同一关系指标会解析多次（取数编译和逐跳各一次），条件不同但候选相同。
//
// 复用的只有「哪些表含这个字段」，按空间、字段、是否正则、是否跳过空间区分；容器过滤、应用归属、
// 独立指标 RT 等依赖查询条件的判定仍对候选逐次执行，结果与逐次扫描相同。复用范围是安装它的请求：
// 请求中途到达的路由更新从下一个请求起生效。
type RouteCandidates struct {
	mu    sync.Mutex
	byKey map[routeCandidateKey][]string
	scans int
	hits  int
}

type routeCandidateKey struct {
	spaceUID    string
	field       string
	isRegexp    bool
	isSkipSpace bool
}

type routeCandidatesContextKey struct{}

// WithRouteCandidates 返回带候选复用的上下文；同一上下文派生出的查询共用它。
func WithRouteCandidates(ctx context.Context) (context.Context, *RouteCandidates) {
	if existing := routeCandidatesFrom(ctx); existing != nil {
		return ctx, existing
	}
	reuse := &RouteCandidates{byKey: make(map[routeCandidateKey][]string)}
	return context.WithValue(ctx, routeCandidatesContextKey{}, reuse), reuse
}

func routeCandidatesFrom(ctx context.Context) *RouteCandidates {
	if ctx == nil {
		return nil
	}
	reuse, _ := ctx.Value(routeCandidatesContextKey{}).(*RouteCandidates)
	return reuse
}

// Counts 返回全空间扫描次数与复用次数。
func (r *RouteCandidates) Counts() (scans, hits int) {
	if r == nil {
		return 0, 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.scans, r.hits
}

func (r *RouteCandidates) get(key routeCandidateKey) ([]string, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	candidates, ok := r.byKey[key]
	if ok {
		r.hits++
	}
	return candidates, ok
}

func (r *RouteCandidates) put(key routeCandidateKey, candidates []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.scans++
	r.byKey[key] = candidates
}

// fieldCandidates 是 tIDs 中字段列表含有该字段（或匹配该正则）的结果表，保持 tIDs 的顺序。
// 字段不匹配的表在 NewTsDBs 中不产出任何 TsDB（全空间扫表时没有字段缺失兜底），因此只对候选
// 执行 NewTsDBs 与对全部表执行的结果相同。
func (s *SpaceFilter) fieldCandidates(tIDs []string, fieldName string, fieldNameExp *regexp.Regexp) []string {
	candidates := make([]string, 0)
	for _, tID := range tIDs {
		rtDetail := s.router.GetResultTable(s.ctx, tID, false)
		if rtDetail == nil {
			continue
		}
		for _, f := range rtDetail.Fields {
			if fieldMatches(f, fieldName, fieldNameExp) {
				candidates = append(candidates, tID)
				break
			}
		}
	}
	return candidates
}

// fieldMatches 是 NewTsDBs 判断结果表字段是否为目标指标的规则。
func fieldMatches(field, fieldName string, fieldNameExp *regexp.Regexp) bool {
	return field == fieldName || (fieldNameExp != nil && fieldNameExp.Match([]byte(field)))
}
