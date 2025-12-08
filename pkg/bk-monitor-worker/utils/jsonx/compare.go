// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package jsonx

import (
	jd "github.com/josephburnett/jd/lib"
	"github.com/pkg/errors"
)

func CompareObjects(objA, objB any) (bool, error) {
	jStrA, err := MarshalString(objA)
	if err != nil {
		return false, errors.Wrapf(err, "marshal obj [%#v] failed", objA)
	}
	jStrB, err := MarshalString(objB)
	if err != nil {
		return false, errors.Wrapf(err, "marshal obj [%#v] failed", objB)
	}

	return CompareJson(jStrA, jStrB)
}

func CompareJson(jStrA, jStrB string) (bool, error) {
	nodeA, err := jd.ReadJsonString(jStrA)
	if err != nil {
		return false, errors.Wrapf(err, "read json string [%#v] failed", jStrA)
	}
	nodeB, err := jd.ReadJsonString(jStrB)
	if err != nil {
		return false, errors.Wrapf(err, "read json string [%#v] failed", jStrB)
	}
	return nodeA.Equals(nodeB, jd.SET), nil
}

func CompareJsonRender(jStrA, jStrB string) (string, error) {
	nodeA, err := jd.ReadJsonString(jStrA)
	if err != nil {
		return "", errors.Wrapf(err, "read json string [%#v] failed", jStrA)
	}
	nodeB, err := jd.ReadJsonString(jStrB)
	if err != nil {
		return "", errors.Wrapf(err, "read json string [%#v] failed", jStrB)
	}
	return nodeA.Diff(nodeB, jd.SET).Render(), nil
}
