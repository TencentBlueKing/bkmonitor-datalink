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

func Test_Local_Normal(t *testing.T) {
	var c Storage
	var err error

	c, err = NewLocalStorage("abc.db")
	if err != nil {
		t.Fatal(err)
	}

	key := "key"
	val := "value"
	if err = c.Set(key, val, 0); err != nil {
		t.Fatal(err)
	}

	v, err := c.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	if v != val {
		t.Fatal("value is not correct")
	}

	err = c.Del(key)
	if err != nil {
		t.Fatal(err)
	}

	err = c.Close()
	if err != nil {
		t.Fatal(err)
	}

	// clear db
	err = c.Destroy()
	if err != nil {
		t.Fatal(err)
	}
}

func Test_Local_DoubleClose(t *testing.T) {
	var c Storage
	var err error

	c, err = NewLocalStorage("abc.db")
	if err != nil {
		t.Fatal(err)
	}

	err = c.Close()
	if err != nil {
		t.Fatal(err)
	}

	err = c.Close()
	if err != nil {
		t.Fatal(err)
	}

	// clear db
	err = c.Destroy()
	if err != nil {
		t.Fatal(err)
	}
}

func Test_Local_NonExistValue(t *testing.T) {
	var c Storage
	var err error

	c, err = NewLocalStorage("abc.db")
	if err != nil {
		t.Fatal(err)
	}

	_, err = c.Get("not_existed")
	if err == nil {
		t.Fatal(err)
	}

	err = c.Del("not_existed")
	if err != nil {
		t.Fatal(err)
	}

	// clear db
	err = c.Destroy()
	if err != nil {
		t.Fatal(err)
	}
}

func Test_Local_CoverSet(t *testing.T) {
	var c Storage
	var err error
	var v string

	c, err = NewLocalStorage("abc.db")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	key := "same"
	if err = c.Set(key, "v1", 0); err != nil {
		t.Fatal(err)
	}

	if err = c.Set(key, "v2", 0); err != nil {
		t.Fatal(err)
	}

	v, err = c.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	if v != "v2" {
		t.Fatal(err)
	}

	// clear db
	err = c.Destroy()
	if err != nil {
		t.Fatal(err)
	}
}
