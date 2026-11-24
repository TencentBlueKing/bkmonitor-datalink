// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"linkd/internal/onemodel/queryservice"
)

func decodeOneModel(w http.ResponseWriter, r *http.Request, v any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		return err
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return errors.New("trailing JSON")
	}
	return nil
}

func (a *API) queryOneModel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if a.OneModel == nil {
		oneModelFailure(w, &queryservice.Error{Status: 503, Message: "OneModel 查询未配置"})
		return
	}
	var result queryservice.Response
	var err error
	switch r.PathValue("operation") {
	case "search":
		var request queryservice.SearchRequest
		if decodeOneModel(w, r, &request) != nil {
			oneModelFailure(w, &queryservice.Error{Status: 400, Message: "无效的 OneModel 查询参数"})
			return
		}
		result, err = a.OneModel.Search(r.Context(), request)
	case "related":
		var request queryservice.RelatedRequest
		if decodeOneModel(w, r, &request) != nil {
			oneModelFailure(w, &queryservice.Error{Status: 400, Message: "无效的关联查询参数"})
			return
		}
		result, err = a.OneModel.Related(r.Context(), request)
	case "close":
		var request queryservice.CloseRequest
		if decodeOneModel(w, r, &request) != nil {
			oneModelFailure(w, &queryservice.Error{Status: 400, Message: "无效的游标参数"})
			return
		}
		err = a.OneModel.Close(r.Context(), request)
		if err == nil {
			output(w, map[string]bool{"closed": true})
			return
		}
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		oneModelFailure(w, err)
		return
	}
	output(w, result)
}

func oneModelFailure(w http.ResponseWriter, err error) {
	status, message := http.StatusBadGateway, "OneModel 查询失败"
	var queryError *queryservice.Error
	if errors.As(err, &queryError) {
		status, message = queryError.Status, queryError.Message
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	output(w, map[string]any{"error": map[string]string{"message": message}})
}
