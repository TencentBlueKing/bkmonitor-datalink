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
)

func Test_Storage_Normal(t *testing.T) {
	var err error

	err = Init("abc.db", nil)
	if err != nil {
		t.Fatal(err)
	}

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

	Close()

	if err := Destory(); err != nil {
		t.Fatal(err)
	}
}

func Test_Storage_DoubleClose(t *testing.T) {
	err := Init("abc.db", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer Destory()
	Close()
	Close()
}

func Test_Storage_NonExistValue(t *testing.T) {
	err := Init("abc.db", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer Destory()

	if _, err := Get("not_existed"); err == nil {
		t.Fatal(err)
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

	if err := Destory(); err != nil {
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

	if err := Destory(); err != nil {
		t.Fatal(err)
	}
}
