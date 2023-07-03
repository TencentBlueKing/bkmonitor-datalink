// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package esb

// CCSearchHostResponseData :
type CCSearchHostResponseData struct {
	Count int                        `json:"count"`
	Info  []CCSearchHostResponseInfo `json:"info"`
}

// CCSearchHostResponseData :
type CCSearchHostResponseDataV3Monitor struct {
	Count int                              `json:"count"`
	Info  []CCSearchHostResponseInfoV3Topo `json:"info"`
}

// CCSearchHostResponseHostCloudIDInfo :
type CCSearchHostResponseHostCloudIDInfo struct {
	BKObjName  string `json:"bk_obj_name"`
	ID         uint32 `json:"bk_inst_id"`
	BKObjID    string `json:"bk_obj_id"`
	BKObjIcon  string `json:"bk_obj_icon"`
	BKInstName string `json:"bk_inst_name"`
}

type CCSearchHostResponseInfo struct {
	Host CCSearchHostResponseHostInfo `json:"host"`
	Topo []HostTopoV3                 `json:"topo"`
}

type HostTopoV3 struct {
	BKSetID int                  `json:"bk_set_id"`
	Module  []CCHostSearchModule `json:"module"`
}

type CCSearchHostResponseInfoV3Topo struct {
	Host  CCSearchHostResponseHostInfo `json:"host"`
	BizID int                          `json:"BizID"`
	Topo  []map[string]string          `json:"topo"`
}

type CCSearchHostResponseHostInfo struct {
	BKCloudID     int    `json:"bk_cloud_id"`
	BKHostInnerIP string `json:"bk_host_innerip"`
	BKOuterIP     string `json:"bk_host_outerip"`
	DbmMeta       string `json:"dbm_meta"`
}

// CCSearchHostResponseBizInfo :
type CCSearchHostResponseBizInfo struct {
	BKBizID      uint32 `json:"bk_biz_id"`
	BKBizName    string `json:"bk_biz_name"`
	TimeZone     string `json:"time_zone"`
	BKSupplierID uint32 `json:"bk_supplier_id"`
}

// CCSearchHostResponse :
type CCSearchHostResponse struct {
	APIResponse
	CCSearchHostResponseData
}

// CCSearchHostRequestPageInfo :
type CCSearchHostRequestPageInfo struct {
	Start int    `url:"start" json:"start"`
	Limit int    `url:"limit" json:"limit"`
	Sort  string `url:"sort" json:"sort"`
}

// CCSearchHostRequestConditionInfo :
type CCSearchHostRequestConditionInfo struct {
	BKObjID string   `json:"bk_obj_id"`
	Fields  []string `json:"fields"`
}

// CCSearchHostRequest :
type CCSearchHostRequest struct {
	*CommonArgs
	BkBizID int                         `url:"bk_biz_id" json:"bk_biz_id"`
	Page    CCSearchHostRequestPageInfo `url:"page" json:"page"`
	Fields  []string                    `url:"fields" json:"fields"`
	// Condition []CCSearchHostRequestConditionInfo `json:"condition"`
}

// CCSearchBizInstTopoParams :
type CCSearchBizInstTopoParams struct {
	AppCode   string `url:"bk_app_code,omitempty" json:"bk_app_code,omitempty"`
	AppSecret string `url:"bk_app_secret,omitempty" json:"bk_app_secret,omitempty"`
	BKToken   string `url:"bk_token,omitempty" json:"bk_token,omitempty"`
	UserName  string `url:"bk_username,omitempty" json:"bk_username,omitempty"`
	BkBizID   int    `url:"bk_biz_id,omitempty"`
	Level     int    `url:"level,omitempty"`

	Start int `url:"start,omitempty"`
	Limit int `url:"limit,omitempty"`
}

// CCSearchTopoResponseInfo :
type CCSearchBizInstTopoResponseInfo struct {
	Inst      int                                `json:"bk_inst_id"`
	InstName  string                             `json:"bk_inst_name"`
	BkObjID   string                             `json:"bk_obj_id"`
	BkObjName string                             `json:"bk_obj_name"`
	Child     []*CCSearchBizInstTopoResponseInfo `json:"child"`
}

type CCHostSearchModule struct {
	BKModuleID int `json:"bk_module_id"`
}

// CCSearchBusinessResponseData
type CCSearchBusinessResponseData struct {
	Count int                            `json:"count"`
	Info  []CCSearchBusinessResponseInfo `json:"info"`
}

// CCSearchBusinessResponseInfo
type CCSearchBusinessResponseInfo struct {
	BKBizID   int    `json:"bk_biz_id"`
	BKBizName string `json:"bk_biz_name"`
}

// CCSearchBusinessRequest
type CCSearchBusinessRequest struct {
	*CommonArgs
	Fields []string `json:"fields"`
}

// service_instance--------------根据实例id 查询module_id
type CCSearchServiceInstanceResponseData struct {
	Count int                                   `json:"count"`
	Info  []CCSearchServiceInstanceResponseInfo `json:"info"`
}

// CCSearchServiceInstanceResponseInfo
type CCSearchServiceInstanceResponseInfo struct {
	InstanceID int                                    `json:"id"`
	BKModuleID int                                    `json:"bk_module_id"`
	MetaData   CCSearchServiceInstanceRequestMetadata `json:"metadata"`
}

// 实例详情接口参数
type CCSearchServiceInstanceRequest struct {
	*CommonArgs
	Page    CCSearchServiceInstanceRequestMetadataLabelPage `json:"page"`
	BkBizID int                                             `json:"bk_biz_id"`
}

// CCSearchServiceInstanceRequestMetadata
type CCSearchServiceInstanceRequestMetadata struct {
	Label CCSearchServiceInstanceRequestMetadataLabel `json:"label"`
}

// CCSearchServiceInstanceRequestMetadataLabel
type CCSearchServiceInstanceRequestMetadataLabel struct {
	BkBizID string `json:"bk_biz_id"`
}

type CCSearchServiceInstanceRequestMetadataLabelPage struct {
	Start int    `json:"start"`
	Limit int    `json:"limit"`
	Sort  string `json:"sort"`
}

// CCSearchBusinessRequest
type CCGetBusinessLocationRequest struct {
	*CommonArgs
	BkBizIDs []int `json:"bk_biz_ids"`
}

type CCGetBusinessLocationResponse struct {
	APIResponse
	Data []*CCGetBusinessLocationResponseInfo `json:"data"`
}

type CCGetBusinessLocationResponseInfo struct {
	BkBizID    int    `json:"bk_biz_id"`
	BkLocation string `json:"bk_location"`
}
