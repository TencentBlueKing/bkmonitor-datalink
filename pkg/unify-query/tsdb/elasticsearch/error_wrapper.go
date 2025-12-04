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
	"errors"
	"fmt"
	"io"
	"strings"

	elastic "github.com/olivere/elastic/v7"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func handleESSpecificError(elasticErr *elastic.Error) error {
	if elasticErr.Details == nil {
		return elasticErr
	}
	var msgBuilder strings.Builder

	hasValidRootCause := elasticErr.Details.RootCause != nil && len(elasticErr.Details.RootCause) > 0
	hasValidCausedBy := elasticErr.Details.CausedBy != nil && len(elasticErr.Details.CausedBy) > 0
	existValidErr := hasValidRootCause || hasValidCausedBy

	if existValidErr {
		if hasValidRootCause {
			msgBuilder.WriteString("root cause: \n")
			for _, rc := range elasticErr.Details.RootCause {
				msgBuilder.WriteString(fmt.Sprintf("%s: %s \n", rc.Index, rc.Reason))
			}
		}

		if hasValidCausedBy {
			msgBuilder.WriteString("caused by: \n")
			for k, v := range elasticErr.Details.CausedBy {
				msgBuilder.WriteString(fmt.Sprintf("%s: %v \n", k, v))
			}
		}
	} else {
		rawBytes, err := json.Marshal(elasticErr)
		if err != nil {
			return elasticErr
		}
		msgBuilder.WriteString(string(rawBytes))
	}

	return errors.New(msgBuilder.String())
}

func processOnESErr(ctx context.Context, url string, err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return curl.HandleClientError(ctx, metadata.MsgQueryES, url, err)
	}

	var elasticErr *elastic.Error
	if errors.As(err, &elasticErr) {
		return handleESSpecificError(elasticErr)
	}

	if errors.Is(err, io.EOF) {
		return nil
	}

	return curl.HandleClientError(ctx, metadata.MsgQueryES, url, err)
}
