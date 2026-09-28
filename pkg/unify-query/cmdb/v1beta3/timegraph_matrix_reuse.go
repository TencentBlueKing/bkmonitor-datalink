// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	pl "github.com/prometheus/prometheus/promql"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

type timeGraphMatrixSlot struct {
	key     string
	matrix  pl.Matrix
	partial bool
}

// A single request-local slot retains at most one Matrix. Preparation still
// runs on every logical query, so route changes cannot hit a stale input key.
func (loader *timeGraphMatrixLoader) loadPrepared(ctx context.Context, query *structured.QueryTs, expression string,
	params *metadata.QueryParams, fetch func() (pl.Matrix, bool, error),
) (pl.Matrix, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	key := ""
	if loader.topology && SharedTopologyReuseMatrix && timeGraphQueryStage(ctx) == "relation-edge" &&
		params != nil && params.StorageType != nil && params.IsDirectQuery() {
		key = loader.matrixReuseKey(ctx, query, expression, params)
	}
	if key != "" && loader.lastMatrix.key == key {
		loader.reusedQueryCount++
		return loader.lastMatrix.matrix, loader.lastMatrix.partial, nil
	}
	// Drop the previous Matrix before fetching a different one, not afterwards.
	loader.lastMatrix = timeGraphMatrixSlot{}
	loader.physicalQueryCount++
	matrix, partial, err := fetch()
	if status := metadata.GetStatus(ctx); status != nil {
		partial = partial || status.Code == metadata.QueryTsPartial
	}
	if err == nil && key != "" {
		loader.lastMatrix = timeGraphMatrixSlot{key: key, matrix: matrix, partial: partial}
	}
	return matrix, partial, err
}

func (loader *timeGraphMatrixLoader) matrixReuseKey(ctx context.Context, query *structured.QueryTs, expression string, params *metadata.QueryParams) string {
	user := *metadata.GetUser(ctx)
	user.HashID = ""
	storage := params.StorageType.ToArray()
	sort.Strings(storage)
	key := struct {
		Query           *structured.QueryTs
		Expression      string
		Expand          *metadata.VmExpand
		User            metadata.User
		Params          *metadata.QueryParams
		Storage         []string
		Start, End      time.Time
		Step, LookBack  time.Duration
		Exact, LeftOpen bool
		BackendBytes    int64
	}{
		Query:        query,
		Expression:   expression,
		Expand:       metadata.GetExpand(ctx),
		User:         user,
		Params:       params,
		Storage:      storage,
		Start:        loader.start,
		End:          loader.end,
		Step:         loader.step,
		LookBack:     loader.lookBack,
		Exact:        metadata.IsExactTimeGrid(ctx),
		LeftOpen:     metadata.IsLeftOpenTimeWindow(ctx),
		BackendBytes: metadata.BackendResponseLimit(ctx),
	}
	encoded, err := json.Marshal(key)
	if err != nil {
		return ""
	}
	return string(encoded)
}
