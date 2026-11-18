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
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

// Reading or decoding a backend's answer that breaks off is the backend's failure; a cancellation or a
// deadline met while reading keeps its own class.
func TestBackendResponseFailureClass(t *testing.T) {
	require.NoError(t, backendResponseFailure(nil))
	for _, err := range []error{io.ErrUnexpectedEOF, errors.New("invalid character '<' looking for beginning of value")} {
		wrapped := backendResponseFailure(err)
		require.ErrorIs(t, wrapped, err)
		require.Equal(t, http.StatusBadGateway, metadata.FailureStatus(wrapped))
	}
	canceled := backendResponseFailure(fmt.Errorf("read body: %w", context.Canceled))
	var response *metadata.BackendResponseError
	require.False(t, errors.As(canceled, &response))
	require.Equal(t, http.StatusBadRequest, metadata.FailureStatus(canceled))
	timedOut := backendResponseFailure(fmt.Errorf("read body: %w", context.DeadlineExceeded))
	require.False(t, errors.As(timedOut, &response))
	require.Equal(t, http.StatusGatewayTimeout, metadata.FailureStatus(timedOut))
}
