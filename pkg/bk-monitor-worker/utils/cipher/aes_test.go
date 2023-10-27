// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cipher

import (
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

func TestAESDecrypt(t *testing.T) {
	var encryptedAndPlainMap = map[string]string{
		"abcde": "abcde",
		"aes_str:::AMfpKHYB8nEMAbS/4x4MPzg5watAX8JpPDSQMkltziE=":                     "",
		"aes_str:::QdT4DdT038nMxHdJ4T3vho2IMhAQhwVDf3f970qXc4o=":                     "",
		"aes_str:::srCvsNoBIUsCtBfqASIAcTlQThp3GVHqu726bvhpVjo=":                     "5gYTZqvd7Z7s",
		"aes_str:::dDFXjpGztB6DGLl6XzbKFStZF4WT4BXQMX8Edm/RAysSfG4OmtpI8OgyDH+EJG6L": "zRD6AqbG5XSBKzz0Flxf",
		"aes_str:::X91jZcJtY5Yq3Y9oVZlHMqKwDakt950rV3IFY26YOXk=":                     "5gYTZqvd7Z7s",
	}
	viper.Set(AESKeyPath, "81be7fc6-5476-4934-9417-6d4d593728db")
	assert.Equal(t, "", AESDecrypt(""))
	for encrypetd, plain := range encryptedAndPlainMap {
		assert.Equal(t, plain, AESDecrypt(encrypetd))
	}

}
