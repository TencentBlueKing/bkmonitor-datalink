// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 日志平台 (BlueKing - Log) available.
// Copyright (C) 2017-2021 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package controllers

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	bluekingv1alpha1 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/api/bk.tencent.com/v1alpha1"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/utils"
)

// BkLogConfigReconciler reconciles a BkLogConfig object
type BkLogConfigReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	BkLogSidecar *BkLogSidecar
}

//+kubebuilder:rbac:groups=bk.tencent.com,resources=bklogconfigs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=bk.tencent.com,resources=bklogconfigs/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=bk.tencent.com,resources=bklogconfigs/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the BkLogConfig object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.9.2/pkg/reconcile
func (r *BkLogConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	log.Info(fmt.Sprintf("handler bklogconfig event [%s]", req.Name))
	var bkLogConfig bluekingv1alpha1.BkLogConfig
	err := r.Client.Get(ctx, req.NamespacedName, &bkLogConfig)
	if utils.NotNil(err) {
		if errors.IsNotFound(err) {
			r.BkLogSidecar.deleteConfigByName(req.Namespace, req.Name)
			utils.CheckErrorFn(r.BkLogSidecar.reloadBkunifylogbeat(), func(err error) {
				log.Error(err, "bklogconfig delete then reload agent failed")
			})

			return ctrl.Result{}, nil
		}
		log.Error(err, "is other error")
		return ctrl.Result{}, nil
	}

	r.BkLogSidecar.deleteConfigByName(req.Namespace, req.Name)
	r.BkLogSidecar.generateActualBkLogConfig()
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *BkLogConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&bluekingv1alpha1.BkLogConfig{}).
		Complete(r)
}
