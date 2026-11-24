// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metadata

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A backend answer that breaks off is the backend's failure; the same error from decoding the caller's
// truncated request body stays the caller's.
func TestFailureStatusOfABrokenBackendAnswer(t *testing.T) {
	require.Equal(t, http.StatusBadGateway, FailureStatus(&BackendResponseError{Err: io.ErrUnexpectedEOF}))
	require.Equal(t, http.StatusBadGateway, FailureStatus(ErrorWithCauses("query failed", &BackendResponseError{Err: io.ErrUnexpectedEOF})))

	var request map[string]any
	err := json.NewDecoder(strings.NewReader(`{"query":`)).Decode(&request)
	require.True(t, errors.Is(err, io.ErrUnexpectedEOF))
	require.Equal(t, http.StatusBadRequest, FailureStatus(err))

	// A failure stated in a 200 body is not classified yet: the query answer keeps the other-failure status.
	require.Equal(t, http.StatusBadRequest, FailureStatus(&BackendBodyError{Code: "1500005", Message: "failed"}))
}
