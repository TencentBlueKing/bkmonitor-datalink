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
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func TestHandleClientError(t *testing.T) {
	metadata.InitMetadata()

	tests := []struct {
		name             string
		url              string
		inputError       error
		expectedContains string
		expectedStatus   string
		shouldReturnNil  bool
	}{
		{
			name:            "nil error should return nil",
			url:             "http://elasticsearch.example.com:9200",
			inputError:      nil,
			shouldReturnNil: true,
		},
		{
			name:             "context canceled for ES",
			url:              "http://elasticsearch.example.com:9200/logs/_search",
			inputError:       context.Canceled,
			expectedContains: "Query Timeout: the request to http://elasticsearch.example.com:9200/logs/_search timed out",
			expectedStatus:   metadata.StorageTimeout,
		},
		{
			name:             "context deadline exceeded for Doris",
			url:              "http://doris.example.com:8030/api/query",
			inputError:       context.DeadlineExceeded,
			expectedContains: "Query Timeout: the request to http://doris.example.com:8030/api/query timed out",
			expectedStatus:   metadata.StorageTimeout,
		},
		{
			name:             "network connection error for InfluxDB",
			url:              "http://influxdb.example.com:8086/query",
			inputError:       errors.New("dial tcp: connection refused"),
			expectedContains: "Query Error: failed to connect to http://influxdb.example.com:8086/query",
			expectedStatus:   metadata.StorageError,
		},
		{
			name:             "HTTP 404 error for BkSQL",
			url:              "http://bksql.example.com/api/v1/query",
			inputError:       errors.New("404 Not Found"),
			expectedContains: "Query Error: failed to connect to http://bksql.example.com/api/v1/query",
			expectedStatus:   metadata.StorageError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := metadata.InitHashID(context.Background())
			err := HandleClientError(ctx, tt.url, tt.inputError)

			if tt.shouldReturnNil {
				if err != nil {
					t.Errorf("expected nil error, got %v", err)
				}
				return
			}

			if err == nil {
				t.Errorf("expected error, got nil")
				return
			}

			// Check error message content
			if tt.expectedContains != "" {
				if !strings.Contains(err.Error(), tt.expectedContains) {
					t.Errorf("expected error message to contain '%s', got '%s'", tt.expectedContains, err.Error())
				}
			}

			// Check if the status was set correctly
			status := metadata.GetStatus(ctx)
			if status != nil && tt.expectedStatus != "" {
				if status.Code != tt.expectedStatus {
					t.Errorf("expected status code '%s', got '%s'", tt.expectedStatus, status.Code)
				}
			}

			// Check if it's a ClientErr
			var clientErr *ClientErr
			if errors.As(err, &clientErr) {
				if clientErr.OriginalError != tt.inputError {
					t.Errorf("expected original error to be preserved")
				}
			} else {
				t.Errorf("expected ClientErr, got %T", err)
			}
		})
	}
}
