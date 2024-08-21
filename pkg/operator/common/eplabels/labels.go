// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package eplabels

func EndpointNodeName(endpointslice bool) string {
	if endpointslice {
		return "__meta_kubernetes_endpointslice_node_name"
	}
	return "__meta_kubernetes_endpoint_node_name"
}

func EndpointAddressTargetKind(endpointslice bool) string {
	if endpointslice {
		return "__meta_kubernetes_endpointslice_address_target_kind"
	}
	return "__meta_kubernetes_endpoint_address_target_kind"
}

func EndpointAddressTargetName(endpointslice bool) string {
	if endpointslice {
		return "__meta_kubernetes_endpointslice_address_target_name"
	}
	return "__meta_kubernetes_endpoint_address_target_name"
}

func EndpointPortName(endpointslice bool) string {
	if endpointslice {
		return "__meta_kubernetes_endpointslice_port_name"
	}
	return "__meta_kubernetes_endpoint_port_name"
}
