// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BK-Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package remote

import (
	"context"
	"errors"
	"testing"

	"github.com/prometheus/prometheus/prompb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockPrometheusWriteClient struct {
	tokens   []string
	requests []prompb.WriteRequest
}

func (c *mockPrometheusWriteClient) Close(context.Context) error {
	return nil
}

func (c *mockPrometheusWriteClient) WriteBatch(_ context.Context, token string, request prompb.WriteRequest) error {
	c.tokens = append(c.tokens, token)
	c.requests = append(c.requests, request)
	return nil
}

func TestRelationDataIDReporterUsesConfiguredDataIDToken(t *testing.T) {
	writer := &mockPrometheusWriteClient{}
	fetchCount := 0
	reporter := newRelationDataIDReporter(1573946, writer, func(dataID uint) (string, error) {
		fetchCount++
		assert.Equal(t, uint(1573946), dataID)
		return "relation-token", nil
	})

	series := prompb.TimeSeries{Labels: []prompb.Label{{Name: "__name__", Value: "node_with_system"}}}
	require.NoError(t, reporter.Write(context.Background(), series))
	require.NoError(t, reporter.Write(context.Background(), series))

	assert.Equal(t, 1, fetchCount)
	assert.Equal(t, []string{"relation-token", "relation-token"}, writer.tokens)
	assert.Len(t, writer.requests, 2)
}

func TestRelationDataIDReporterDoesNotWriteWhenTokenLookupFails(t *testing.T) {
	writer := &mockPrometheusWriteClient{}
	reporter := newRelationDataIDReporter(1573946, writer, func(uint) (string, error) {
		return "", errors.New("time series group not found")
	})

	err := reporter.Write(context.Background(), prompb.TimeSeries{})
	require.Error(t, err)
	assert.Empty(t, writer.requests)
}

func TestNewRelationDataIDReporterRequiresDataID(t *testing.T) {
	reporter, err := NewRelationDataIDReporter(0, "http://remote-write", nil)
	assert.Nil(t, reporter)
	require.Error(t, err)
}
