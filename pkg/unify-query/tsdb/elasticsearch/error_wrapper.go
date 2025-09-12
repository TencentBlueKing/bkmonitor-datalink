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
	"errors"
	"fmt"
	"strings"

	elastic "github.com/olivere/elastic/v7"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/client_errors"
)

func processOnEsErr(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return client_errors.HandleClientError(ctx, err)
	}

	var elasticErr *elastic.Error
	if errors.As(err, &elasticErr) {
		return handleElasticSpecificError(elasticErr)
	}

	if err.Error() == "EOF" {
		return nil
	}

	return client_errors.HandleClientError(ctx, err)
}

func handleElasticSpecificError(elasticErr *elastic.Error) error {
	var msgBuilder strings.Builder

	if elasticErr.Details != nil {
		if len(elasticErr.Details.RootCause) > 0 {
			msgBuilder.WriteString("root cause: \n")
			for _, rc := range elasticErr.Details.RootCause {
				msgBuilder.WriteString(fmt.Sprintf("%s: %s \n", rc.Index, rc.Reason))
			}
		}

		if elasticErr.Details.CausedBy != nil {
			msgBuilder.WriteString("caused by: \n")
			for k, v := range elasticErr.Details.CausedBy {
				msgBuilder.WriteString(fmt.Sprintf("%s: %v \n", k, v))
			}
		}
	}

	return errors.New(msgBuilder.String())
}
