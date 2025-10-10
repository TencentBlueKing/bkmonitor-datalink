// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package curl

import (
	"context"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

type ClientErr struct {
	OriginalError error
	Message       string
}

func (e *ClientErr) Error() string {
	return e.Message
}

func (e *ClientErr) Unwrap() error {
	return e.OriginalError
}

func HandleClientError(ctx context.Context, url string, err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		metadata.Sprintf(metadata.MsgQueryTs,
			"查询 %s 超时",
			url,
		).Status(ctx, metadata.StorageTimeout)
		return nil
	}

	return metadata.Sprintf(metadata.MsgQueryTs,
		"查询 %s 报错",
		url,
	).Error(ctx, err)
}
