// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package core

import (
	"context"
	"strings"
	"testing"
)

func TestMetadataCenterGetDataIDInfo(t *testing.T) {
	client := &fakeConsulClient{value: []byte(`{
		"bk_tenant_id":"test-tenant",
		"bk_biz_id":1,
		"bk_data_id":42,
		"kafka_info":{
			"host":"kafka.example.invalid:9092",
			"username":"",
			"password":"",
			"topic":"test-topic"
		}
	}`)}
	center, err := NewMetadataCenter(client, "/test/active_profiling/data_id/")
	if err != nil {
		t.Fatal(err)
	}

	info, err := center.GetDataIDInfo(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if client.getKey != "test/active_profiling/data_id/42" {
		t.Fatalf("Get() key = %q", client.getKey)
	}
	if info.TenantID != "test-tenant" || info.BizID != 1 || info.DataID != 42 {
		t.Fatalf("unexpected DataID info: %+v", info)
	}
	if info.KafkaInfo.Host != "kafka.example.invalid:9092" || info.KafkaInfo.Topic != "test-topic" {
		t.Fatalf("unexpected Kafka info: %+v", info.KafkaInfo)
	}
}

func TestMetadataCenterListDataIDs(t *testing.T) {
	client := &fakeConsulClient{keys: []string{
		"prefix/active_profiling/data_id/42",
		"prefix/active_profiling/data_id/123",
	}}
	center, err := NewMetadataCenter(client, "prefix/active_profiling/data_id")
	if err != nil {
		t.Fatal(err)
	}

	got, err := center.ListDataIDs()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != 42 || got[1] != 123 {
		t.Fatalf("ListDataIDs() = %v, want [42 123]", got)
	}
	if client.listPrefix != "prefix/active_profiling/data_id/" {
		t.Fatalf("ListKeysWithPrefix() prefix = %q", client.listPrefix)
	}
}

func TestDecodeDataIDInfoRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name  string
		id    uint64
		value string
		want  string
	}{
		{
			name: "mismatched DataID",
			id:   1,
			value: `{"bk_tenant_id":"test-tenant","bk_biz_id":1,"bk_data_id":2,` +
				`"kafka_info":{"host":"kafka.example.invalid:9092","topic":"test-topic"}}`,
			want: "does not match",
		},
		{
			name: "partial credentials",
			id:   1,
			value: `{"bk_tenant_id":"test-tenant","bk_biz_id":1,"bk_data_id":1,` +
				`"kafka_info":{"host":"kafka.example.invalid:9092","username":"test-user","topic":"test-topic"}}`,
			want: "both Kafka username and password",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := decodeDataIDInfo(test.id, []byte(test.value))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected error containing %q, got %v", test.want, err)
			}
		})
	}
}

type fakeConsulClient struct {
	keys       []string
	value      []byte
	getKey     string
	listPrefix string
}

func (c *fakeConsulClient) Get(key string) (uint64, []byte, error) {
	c.getKey = key
	return 0, c.value, nil
}

func (c *fakeConsulClient) ListKeysWithPrefix(prefix string) ([]string, error) {
	c.listPrefix = prefix
	return c.keys, nil
}
