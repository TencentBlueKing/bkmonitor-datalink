// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/blang/semver"
	elastic "github.com/olivere/elastic/v7"
)

// serverVersion 查询当前实例连接的 ES 版本，使用与查询相同的认证、请求头和超时。
func (i *Instance) serverVersion(ctx context.Context) (semver.Version, error) {
	ctx, cancel := context.WithTimeout(ctx, i.timeout)
	defer cancel()
	client, err := i.getClient(ctx, i.connect)
	if err != nil {
		return semver.Version{}, fmt.Errorf("get Elasticsearch version client: %w", err)
	}
	defer client.Stop()

	response, err := client.PerformRequest(ctx, elastic.PerformRequestOptions{Method: http.MethodGet, Path: "/"})
	if err != nil {
		return semver.Version{}, fmt.Errorf("get Elasticsearch version: %w", err)
	}
	var info elastic.PingResult
	if err := json.Unmarshal(response.Body, &info); err != nil {
		return semver.Version{}, fmt.Errorf("decode Elasticsearch version: %w", err)
	}
	version, err := semver.Parse(info.Version.Number)
	if err != nil {
		return semver.Version{}, fmt.Errorf("invalid Elasticsearch version %q: %w", info.Version.Number, err)
	}
	return version, nil
}
