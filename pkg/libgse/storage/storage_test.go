// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_Storage_Normal(t *testing.T) {
	var err error

	err = Init("abc.db", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer Close()

	key := "key"
	val := "value"
	if err = Set(key, val, 0); err != nil {
		t.Fatal(err)
	}

	v, err := Get(key)
	if err != nil {
		t.Fatal(err)
	}
	if v != val {
		t.Fatal("value is not correct")
	}

	if err := Del(key); err != nil {
		t.Fatal(err)
	}

	_, err = Get(key)
	assert.Error(t, err)

	if err := Destroy(); err != nil {
		t.Fatal(err)
	}
}

func Test_Storage_DoubleClose(t *testing.T) {
	err := Init("abc.db", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer Destroy()
	Close()
	Close()
}

func Test_Storage_NonExistValue(t *testing.T) {
	err := Init("abc.db", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer Destroy()

	if _, err := Get("not_existed"); err == nil {
		t.Fatal("key found")
	}

	if err := Del("not_existed"); err != nil {
		t.Fatal(err)
	}
}

func Test_Storage_CoverSet(t *testing.T) {
	if err := Init("abc.db", nil); err != nil {
		t.Fatal(err)
	}
	defer Close()

	key := "same"
	if err := Set(key, "v1", 0); err != nil {
		t.Fatal(err)
	}

	if err := Set(key, "v2", 0); err != nil {
		t.Fatal(err)
	}

	v, err := Get(key)
	if err != nil {
		t.Fatal(err)
	}
	if v != "v2" {
		t.Fatal(err)
	}

	if err := Destroy(); err != nil {
		t.Fatal(err)
	}
}

func Test_Storage_SetNull(t *testing.T) {
	err := Init("abc.db", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer Close()

	// set value empty
	key := "same"
	if err := Set(key, "", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := Get(key); err != ErrNotFound {
		t.Fatal(err)
	}

	// set key empty
	if err := Set("", key, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := Get(key); err != ErrNotFound {
		t.Fatal(err)
	}

	if err := Destroy(); err != nil {
		t.Fatal(err)
	}
}

func Test_Storage_List(t *testing.T) {
	if err := Init("abc.db", nil); err != nil {
		t.Fatal(err)
	}
	defer Close()

	if err := Set("state:1|2", "v1", 0); err != nil {
		t.Fatal(err)
	}

	if err := Set("state:2|3", "v2", 0); err != nil {
		t.Fatal(err)
	}

	if err := Set("others:test", "v3", 0); err != nil {
		t.Fatal(err)
	}

	values, err := List("state")
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, map[string]string{
		"state:1|2": "v1",
		"state:2|3": "v2",
	}, values)

	values, err = List("others")
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, map[string]string{
		"others:test": "v3",
	}, values)

	values, err = List("notexist")
	if err != nil {
		t.Fatal(err)
	}

	assert.Empty(t, values)

	if err := Destroy(); err != nil {
		t.Fatal(err)
	}
}

func Test_Storage_Flush(t *testing.T) {
	storage, err := NewLocalStorage("abc.db")
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()

	if err := storage.Set("state:1|2", "v1", 0); err != nil {
		t.Fatal(err)
	}

	if err := storage.Set("state:2|3", "v2", 0); err != nil {
		t.Fatal(err)
	}

	storage.Flush()

	values, err := storage.List("state")
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, map[string]string{
		"state:1|2": "v1",
		"state:2|3": "v2",
	}, values)

	v, err := storage.Get("state:1|2")
	if err != nil {
		t.Fatal(err)
	}
	if v != "v1" {
		t.Fatal("value is not correct")
	}

	v, err = storage.Get("state:2|3")
	if err != nil {
		t.Fatal(err)
	}
	if v != "v2" {
		t.Fatal("value is not correct")
	}

	storage.Del("state:1|2")

	values, err = storage.List("state")
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, map[string]string{
		"state:2|3": "v2",
	}, values)

	storage.Flush()

	v, err = storage.Get("state:1|2")
	if err == nil {
		t.Fatal("key should be not exist")
	}

	v, err = storage.Get("state:2|3")
	if err != nil {
		t.Fatal(err)
	}
	if v != "v2" {
		t.Fatal("value is not correct")
	}

	values, err = storage.List("state")
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, map[string]string{
		"state:2|3": "v2",
	}, values)

	if err := storage.Destroy(); err != nil {
		t.Fatal(err)
	}
}
