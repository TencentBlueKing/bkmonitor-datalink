// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
)

type failingTopologySchemaProvider struct {
	SchemaProvider
	err error
}

func (p failingTopologySchemaProvider) ValidateSchema(string, ...ResourceType) error {
	return p.err
}

func TestQuerySharedTopologyRejectsSchemaProviderError(t *testing.T) {
	model := &Model{
		schemaProvider: failingTopologySchemaProvider{
			SchemaProvider: sharedTopologyQueryProvider(),
			err:            errors.New("schema backend unavailable"),
		},
	}

	_, err := model.QuerySharedTopology(context.Background(), cmdb.SharedTopologyQuery{
		SpaceUID:   "space",
		Timestamp:  1700000000,
		SourceType: "source",
	})

	require.Error(t, err)
	require.ErrorContains(t, err, "schema backend unavailable")
}
