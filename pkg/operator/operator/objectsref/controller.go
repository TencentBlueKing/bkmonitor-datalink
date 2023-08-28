// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package objectsref

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	tkexversiond "github.com/Tencent/bk-bcs/bcs-scenarios/kourse/pkg/client/clientset/versioned"
	gover "github.com/hashicorp/go-version"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	batchv1beta1 "k8s.io/api/batch/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/k8sutils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// OwnerRef 代表 Owner 对象引用信息
type OwnerRef struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// Object 代表 workload 对象
type Object struct {
	ID        ObjectID
	OwnerRefs []OwnerRef
	NodeName  string
}

// ObjectID 代表 workload 对象标识
type ObjectID struct {
	Name      string
	Namespace string
}

func (oid ObjectID) String() string {
	return fmt.Sprintf("%s/%s", oid.Namespace, oid.Name)
}

type Objects struct {
	kind string
	mut  sync.Mutex
	objs map[string]Object
}

func (o *Objects) Counter() map[string]int {
	o.mut.Lock()
	defer o.mut.Unlock()

	ret := make(map[string]int)
	for _, obj := range o.objs {
		ret[obj.ID.Namespace]++
	}
	return ret
}

func (o *Objects) Kind() string {
	return o.kind
}

func (o *Objects) Set(obj Object) {
	o.mut.Lock()
	defer o.mut.Unlock()

	o.objs[obj.ID.String()] = obj
}

func (o *Objects) Del(oid ObjectID) {
	o.mut.Lock()
	defer o.mut.Unlock()

	delete(o.objs, oid.String())
}

func (o *Objects) GetByNodeName(nodeName string) []Object {
	o.mut.Lock()
	defer o.mut.Unlock()

	var ret []Object
	for _, obj := range o.objs {
		if obj.NodeName == nodeName {
			ret = append(ret, obj)
		}
	}
	return ret
}

func (o *Objects) GetAll() []Object {
	o.mut.Lock()
	defer o.mut.Unlock()

	ret := make([]Object, 0, len(o.objs))
	for _, obj := range o.objs {
		ret = append(ret, obj)
	}
	return ret
}

func (o *Objects) Get(oid ObjectID) (Object, bool) {
	o.mut.Lock()
	defer o.mut.Unlock()

	obj, ok := o.objs[oid.String()]
	return obj, ok
}

func (o *Objects) GetRefs(oid ObjectID) ([]OwnerRef, bool) {
	o.mut.Lock()
	defer o.mut.Unlock()

	obj, ok := o.objs[oid.String()]
	return obj.OwnerRefs, ok
}

func NewObjects(kind string) *Objects {
	return &Objects{kind: kind, objs: make(map[string]Object)}
}

const (
	kindNode            = "Node"
	kindPod             = "Pod"
	kindDeployment      = "Deployment"
	kindReplicaSet      = "ReplicaSet"
	kindStatefulSet     = "StatefulSet"
	kindDaemonSet       = "DaemonSet"
	kindJob             = "Job"
	kindCronJob         = "CronJob"
	kindGameStatefulSet = "GameStatefulSet"
	kindGameDeployment  = "GameDeployment"
)

const (
	resourceNodes = "nodes"
	resourcePods  = "pods"

	// builtin workload
	resourceReplicaSets  = "replicasets"
	resourceDeployments  = "deployments"
	resourceDaemonSets   = "daemonsets"
	resourceStatefulSets = "statefulsets"
	resourceJobs         = "jobs"
	resourceCronJobs     = "cronjobs"

	// extend workload
	resourceGameStatefulSets = "gamestatefulsets"
	resourceGameDeployments  = "gamedeployments"
)

// ObjectsController 负责获取并更新 workload 资源的元信息
type ObjectsController struct {
	ctx    context.Context
	cancel context.CancelFunc

	client              kubernetes.Interface
	podObjs             *Objects
	replicaSetObjs      *Objects
	deploymentObjs      *Objects
	daemonSetObjs       *Objects
	statefulSetObjs     *Objects
	jobObjs             *Objects
	cronJobObjs         *Objects
	gameStatefulSetObjs *Objects // tkex gameStatefulSetObjs 资源监听
	gameDeploymentsObjs *Objects // tkex gameDeploymentsObjs 资源监听
	nodeObjs            *NodeMap

	mm *metricMonitor
}

func NewController(ctx context.Context, client kubernetes.Interface, tkexClient tkexversiond.Interface) (*ObjectsController, error) {
	ctx, cancel := context.WithCancel(ctx)
	controller := &ObjectsController{
		client: client,
		ctx:    ctx,
		cancel: cancel,
	}

	version, err := client.Discovery().ServerVersion()
	if err != nil {
		return nil, err
	}
	KubernetesServerVersion = version.String()
	setClusterVersion(KubernetesServerVersion)

	sharedInformer := informers.NewSharedInformerFactoryWithOptions(client, define.ReSyncPeriod, informers.WithNamespace(metav1.NamespaceAll))
	controller.podObjs, err = newPodObjects(ctx, sharedInformer)
	if err != nil {
		return nil, err
	}

	controller.replicaSetObjs, err = newReplicaSetObjects(ctx, sharedInformer)
	if err != nil {
		return nil, err
	}

	controller.deploymentObjs, err = newDeploymentObjects(ctx, sharedInformer)
	if err != nil {
		return nil, err
	}

	controller.daemonSetObjs, err = newDaemenSetObjects(ctx, sharedInformer)
	if err != nil {
		return nil, err
	}

	controller.statefulSetObjs, err = newStatefulSetObjects(ctx, sharedInformer)
	if err != nil {
		return nil, err
	}

	controller.jobObjs, err = newJobObjects(ctx, sharedInformer)
	if err != nil {
		return nil, err
	}

	controller.cronJobObjs, err = newCronJobObjects(ctx, sharedInformer, KubernetesServerVersion)
	if err != nil {
		return nil, err
	}

	controller.nodeObjs, err = newNodeObjects(ctx, sharedInformer)
	if err != nil {
		return nil, err
	}

	tkexObjs, err := newTkexObjects(ctx, tkexClient, client.Discovery())
	if err != nil {
		return nil, err
	}
	controller.gameStatefulSetObjs = tkexObjs.gamestatefulset
	controller.gameDeploymentsObjs = tkexObjs.gamedeployment

	controller.mm = newMetricMonitor()
	go controller.recordMetrics()

	return controller, nil
}

func (oc *ObjectsController) NodeNames() []string {
	return oc.nodeObjs.Names()
}

func (oc *ObjectsController) NodeCount() int {
	return oc.nodeObjs.Count()
}

func (oc *ObjectsController) NodeNameExists(s string) (string, bool) {
	return oc.nodeObjs.NameExists(s)
}

func (oc *ObjectsController) Stop() {
	oc.cancel()
}

func (oc *ObjectsController) recordMetrics() {
	ticker := time.NewTicker(10 * time.Second)
	for {
		select {
		case <-oc.ctx.Done():
			return

		case <-ticker.C:
			for ns, count := range oc.podObjs.Counter() {
				oc.mm.SetWorkloadCount(count, ns, kindPod)
			}
			for kind, objs := range oc.objsMap() {
				for ns, count := range objs.Counter() {
					oc.mm.SetWorkloadCount(count, ns, kind)
				}
			}
		}
	}
}

func newPodObjects(ctx context.Context, sharedInformer informers.SharedInformerFactory) (*Objects, error) {
	genericInformer, err := sharedInformer.ForResource(corev1.SchemeGroupVersion.WithResource(resourcePods))
	if err != nil {
		return nil, err
	}
	objs := NewObjects(kindPod)

	informer := genericInformer.Informer()
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			pod, ok := obj.(*corev1.Pod)
			if !ok {
				logger.Errorf("excepted type Pod, got %T", obj)
				return
			}
			objs.Set(Object{
				ID: ObjectID{
					Name:      pod.Name,
					Namespace: pod.Namespace,
				},
				OwnerRefs: toRefs(pod.OwnerReferences),
				NodeName:  pod.Spec.NodeName,
			})
		},
		UpdateFunc: func(_, newObj interface{}) {
			pod, ok := newObj.(*corev1.Pod)
			if !ok {
				logger.Errorf("excepted type Pod, got %T", newObj)
				return
			}
			objs.Set(Object{
				ID: ObjectID{
					Name:      pod.Name,
					Namespace: pod.Namespace,
				},
				OwnerRefs: toRefs(pod.OwnerReferences),
				NodeName:  pod.Spec.NodeName,
			})
		},
		DeleteFunc: func(obj interface{}) {
			pod, ok := obj.(*corev1.Pod)
			if !ok {
				logger.Errorf("excepted type Pod, got %T", obj)
				return
			}
			objs.Del(ObjectID{
				Name:      pod.Name,
				Namespace: pod.Namespace,
			})
		},
	})
	go informer.Run(ctx.Done())

	synced := k8sutils.WaitForNamedCacheSync(ctx, kindPod, informer)
	if !synced {
		return nil, errors.New("failed to sync Pod caches")
	}
	return objs, nil
}

func newReplicaSetObjects(ctx context.Context, sharedInformer informers.SharedInformerFactory) (*Objects, error) {
	genericInformer, err := sharedInformer.ForResource(appsv1.SchemeGroupVersion.WithResource(resourceReplicaSets))
	if err != nil {
		return nil, err
	}
	objs := NewObjects(kindReplicaSet)

	informer := genericInformer.Informer()
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			replicaSet, ok := obj.(*appsv1.ReplicaSet)
			if !ok {
				logger.Errorf("excepted type ReplicaSet, got %T", obj)
				return
			}
			objs.Set(Object{
				ID: ObjectID{
					Name:      replicaSet.Name,
					Namespace: replicaSet.Namespace,
				},
				OwnerRefs: toRefs(replicaSet.OwnerReferences),
			})
		},
		UpdateFunc: func(_, newObj interface{}) {
			replicaSet, ok := newObj.(*appsv1.ReplicaSet)
			if !ok {
				logger.Errorf("excepted type ReplicaSet, got %T", newObj)
				return
			}
			objs.Set(Object{
				ID: ObjectID{
					Name:      replicaSet.Name,
					Namespace: replicaSet.Namespace,
				},
				OwnerRefs: toRefs(replicaSet.OwnerReferences),
			})
		},
		DeleteFunc: func(obj interface{}) {
			replicaSet, ok := obj.(*appsv1.ReplicaSet)
			if !ok {
				logger.Errorf("excepted type ReplicaSet, got %T", obj)
				return
			}
			objs.Del(ObjectID{
				Name:      replicaSet.Name,
				Namespace: replicaSet.Namespace,
			})
		},
	})
	go informer.Run(ctx.Done())

	synced := k8sutils.WaitForNamedCacheSync(ctx, kindReplicaSet, informer)
	if !synced {
		return nil, errors.New("failed to sync ReplicaSet caches")
	}
	return objs, nil
}

func newDeploymentObjects(ctx context.Context, sharedInformer informers.SharedInformerFactory) (*Objects, error) {
	genericInformer, err := sharedInformer.ForResource(appsv1.SchemeGroupVersion.WithResource(resourceDeployments))
	if err != nil {
		return nil, err
	}
	objs := NewObjects(kindDeployment)

	informer := genericInformer.Informer()
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			deployment, ok := obj.(*appsv1.Deployment)
			if !ok {
				logger.Errorf("excepted type Deployment, got %T", obj)
				return
			}
			objs.Set(Object{
				ID: ObjectID{
					Name:      deployment.Name,
					Namespace: deployment.Namespace,
				},
				OwnerRefs: toRefs(deployment.OwnerReferences),
			})
		},
		UpdateFunc: func(_, newObj interface{}) {
			deployment, ok := newObj.(*appsv1.Deployment)
			if !ok {
				logger.Errorf("excepted type Deployment, got %T", newObj)
				return
			}
			objs.Set(Object{
				ID: ObjectID{
					Name:      deployment.Name,
					Namespace: deployment.Namespace,
				},
				OwnerRefs: toRefs(deployment.OwnerReferences),
			})
		},
		DeleteFunc: func(obj interface{}) {
			deployment, ok := obj.(*appsv1.Deployment)
			if !ok {
				logger.Errorf("excepted type Deployment, got %T", obj)
				return
			}
			objs.Del(ObjectID{
				Name:      deployment.Name,
				Namespace: deployment.Namespace,
			})
		},
	})
	go informer.Run(ctx.Done())

	synced := k8sutils.WaitForNamedCacheSync(ctx, kindDeployment, informer)
	if !synced {
		return nil, errors.New("failed to sync Deployment caches")
	}
	return objs, nil
}

func newDaemenSetObjects(ctx context.Context, sharedInformer informers.SharedInformerFactory) (*Objects, error) {
	genericInformer, err := sharedInformer.ForResource(appsv1.SchemeGroupVersion.WithResource(resourceDaemonSets))
	if err != nil {
		return nil, err
	}
	objs := NewObjects(kindDaemonSet)

	informer := genericInformer.Informer()
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			daemonSet, ok := obj.(*appsv1.DaemonSet)
			if !ok {
				logger.Errorf("excepted type DaemonSet, got %T", obj)
				return
			}
			objs.Set(Object{
				ID: ObjectID{
					Name:      daemonSet.Name,
					Namespace: daemonSet.Namespace,
				},
				OwnerRefs: toRefs(daemonSet.OwnerReferences),
			})
		},
		UpdateFunc: func(_, newObj interface{}) {
			daemonSet, ok := newObj.(*appsv1.DaemonSet)
			if !ok {
				logger.Errorf("excepted type DaemonSet, got %T", newObj)
				return
			}
			objs.Set(Object{
				ID: ObjectID{
					Name:      daemonSet.Name,
					Namespace: daemonSet.Namespace,
				},
				OwnerRefs: toRefs(daemonSet.OwnerReferences),
			})
		},
		DeleteFunc: func(obj interface{}) {
			daemonSet, ok := obj.(*appsv1.DaemonSet)
			if !ok {
				logger.Errorf("excepted type DaemonSet, got %T", obj)
				return
			}
			objs.Del(ObjectID{
				Name:      daemonSet.Name,
				Namespace: daemonSet.Namespace,
			})
		},
	})
	go informer.Run(ctx.Done())

	synced := k8sutils.WaitForNamedCacheSync(ctx, kindDaemonSet, informer)
	if !synced {
		return nil, errors.New("failed to sync DaemonSet caches")
	}
	return objs, nil
}

func newStatefulSetObjects(ctx context.Context, sharedInformer informers.SharedInformerFactory) (*Objects, error) {
	genericInformer, err := sharedInformer.ForResource(appsv1.SchemeGroupVersion.WithResource(resourceStatefulSets))
	if err != nil {
		return nil, err
	}
	objs := NewObjects(kindStatefulSet)

	informer := genericInformer.Informer()
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			statefulSet, ok := obj.(*appsv1.StatefulSet)
			if !ok {
				logger.Errorf("excepted type StatefulSet, got %T", obj)
				return
			}
			objs.Set(Object{
				ID: ObjectID{
					Name:      statefulSet.Name,
					Namespace: statefulSet.Namespace,
				},
				OwnerRefs: toRefs(statefulSet.OwnerReferences),
			})
		},
		UpdateFunc: func(_, newObj interface{}) {
			statefulSet, ok := newObj.(*appsv1.StatefulSet)
			if !ok {
				logger.Errorf("excepted type StatefulSet, got %T", newObj)
				return
			}
			objs.Set(Object{
				ID: ObjectID{
					Name:      statefulSet.Name,
					Namespace: statefulSet.Namespace,
				},
				OwnerRefs: toRefs(statefulSet.OwnerReferences),
			})
		},
		DeleteFunc: func(obj interface{}) {
			statefulSet, ok := obj.(*appsv1.StatefulSet)
			if !ok {
				logger.Errorf("excepted type StatefulSet, got %T", obj)
				return
			}
			objs.Del(ObjectID{
				Name:      statefulSet.Name,
				Namespace: statefulSet.Namespace,
			})
		},
	})
	go informer.Run(ctx.Done())

	synced := k8sutils.WaitForNamedCacheSync(ctx, kindStatefulSet, informer)
	if !synced {
		return nil, errors.New("failed to sync StatefulSet caches")
	}
	return objs, nil
}

func newJobObjects(ctx context.Context, sharedInformer informers.SharedInformerFactory) (*Objects, error) {
	genericInformer, err := sharedInformer.ForResource(batchv1.SchemeGroupVersion.WithResource(resourceJobs))
	if err != nil {
		return nil, err
	}
	objs := NewObjects(kindJob)

	informer := genericInformer.Informer()
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			job, ok := obj.(*batchv1.Job)
			if !ok {
				logger.Errorf("excepted type Job, got %T", obj)
				return
			}
			objs.Set(Object{
				ID: ObjectID{
					Name:      job.Name,
					Namespace: job.Namespace,
				},
				OwnerRefs: toRefs(job.OwnerReferences),
			})
		},
		UpdateFunc: func(_, newObj interface{}) {
			job, ok := newObj.(*batchv1.Job)
			if !ok {
				logger.Errorf("excepted type Job, got %T", newObj)
				return
			}
			objs.Set(Object{
				ID: ObjectID{
					Name:      job.Name,
					Namespace: job.Namespace,
				},
				OwnerRefs: toRefs(job.OwnerReferences),
			})
		},
		DeleteFunc: func(obj interface{}) {
			job, ok := obj.(*batchv1.Job)
			if !ok {
				logger.Errorf("excepted type Job, got %T", obj)
				return
			}
			objs.Del(ObjectID{
				Name:      job.Name,
				Namespace: job.Namespace,
			})
		},
	})
	go informer.Run(ctx.Done())

	synced := k8sutils.WaitForNamedCacheSync(ctx, kindJob, informer)
	if !synced {
		return nil, errors.New("failed to sync Job caches")
	}
	return objs, nil
}

func newCronJobObjects(ctx context.Context, sharedInformer informers.SharedInformerFactory, version string) (*Objects, error) {
	v, err := gover.NewVersion(version)
	if err != nil {
		return nil, err
	}

	v125, _ := gover.NewVersion("1.25")
	if v.GreaterThanOrEqual(v125) {
		return newCronJobV1Objects(ctx, sharedInformer)
	}
	return newCronJobBetaV1Objects(ctx, sharedInformer)
}

func newCronJobBetaV1Objects(ctx context.Context, sharedInformer informers.SharedInformerFactory) (*Objects, error) {
	genericInformer, err := sharedInformer.ForResource(batchv1beta1.SchemeGroupVersion.WithResource(resourceCronJobs))
	if err != nil {
		return nil, err
	}
	objs := NewObjects(kindCronJob)

	informer := genericInformer.Informer()
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			cronJob, ok := obj.(*batchv1beta1.CronJob)
			if !ok {
				logger.Errorf("excepted type CronJob, got %T", obj)
				return
			}
			objs.Set(Object{
				ID: ObjectID{
					Name:      cronJob.Name,
					Namespace: cronJob.Namespace,
				},
				OwnerRefs: toRefs(cronJob.OwnerReferences),
			})
		},
		UpdateFunc: func(_, newObj interface{}) {
			cronJob, ok := newObj.(*batchv1beta1.CronJob)
			if !ok {
				logger.Errorf("excepted type CronJob, got %T", newObj)
				return
			}
			objs.Set(Object{
				ID: ObjectID{
					Name:      cronJob.Name,
					Namespace: cronJob.Namespace,
				},
				OwnerRefs: toRefs(cronJob.OwnerReferences),
			})
		},
		DeleteFunc: func(obj interface{}) {
			cronJob, ok := obj.(*batchv1beta1.CronJob)
			if !ok {
				logger.Errorf("excepted type CronJob, got %T", obj)
				return
			}
			objs.Del(ObjectID{
				Name:      cronJob.Name,
				Namespace: cronJob.Namespace,
			})
		},
	})
	go informer.Run(ctx.Done())

	synced := k8sutils.WaitForNamedCacheSync(ctx, kindCronJob, informer)
	if !synced {
		return nil, errors.New("failed to sync CronJob caches")
	}
	return objs, nil
}

func newCronJobV1Objects(ctx context.Context, sharedInformer informers.SharedInformerFactory) (*Objects, error) {
	genericInformer, err := sharedInformer.ForResource(batchv1.SchemeGroupVersion.WithResource(resourceCronJobs))
	if err != nil {
		return nil, err
	}
	objs := NewObjects(kindCronJob)

	informer := genericInformer.Informer()
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			cronJob, ok := obj.(*batchv1.CronJob)
			if !ok {
				logger.Errorf("excepted type CronJob, got %T", obj)
				return
			}
			objs.Set(Object{
				ID: ObjectID{
					Name:      cronJob.Name,
					Namespace: cronJob.Namespace,
				},
				OwnerRefs: toRefs(cronJob.OwnerReferences),
			})
		},
		UpdateFunc: func(_, newObj interface{}) {
			cronJob, ok := newObj.(*batchv1.CronJob)
			if !ok {
				logger.Errorf("excepted type CronJob, got %T", newObj)
				return
			}
			objs.Set(Object{
				ID: ObjectID{
					Name:      cronJob.Name,
					Namespace: cronJob.Namespace,
				},
				OwnerRefs: toRefs(cronJob.OwnerReferences),
			})
		},
		DeleteFunc: func(obj interface{}) {
			cronJob, ok := obj.(*batchv1.CronJob)
			if !ok {
				logger.Errorf("excepted type CronJob, got %T", obj)
				return
			}
			objs.Del(ObjectID{
				Name:      cronJob.Name,
				Namespace: cronJob.Namespace,
			})
		},
	})
	go informer.Run(ctx.Done())

	synced := k8sutils.WaitForNamedCacheSync(ctx, kindCronJob, informer)
	if !synced {
		return nil, errors.New("failed to sync CronJob caches")
	}
	return objs, nil
}

func newNodeObjects(ctx context.Context, sharedInformer informers.SharedInformerFactory) (*NodeMap, error) {
	genericInformer, err := sharedInformer.ForResource(corev1.SchemeGroupVersion.WithResource(resourceNodes))
	if err != nil {
		return nil, err
	}
	objs := NewNodeMap()

	informer := genericInformer.Informer()
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			node, ok := obj.(*corev1.Node)
			if !ok {
				logger.Errorf("excepted type Node, got %T", obj)
				return
			}
			incClusterNodeCount()
			if err := objs.Set(node); err != nil {
				logger.Errorf("failed to set node obj, err: %v", err)
			}
		},
		UpdateFunc: func(_, newObj interface{}) {
			node, ok := newObj.(*corev1.Node)
			if !ok {
				logger.Errorf("excepted type Node, got %T", newObj)
				return
			}
			if err := objs.Set(node); err != nil {
				logger.Errorf("failed to set node obj, err: %v", err)
			}
		},
		DeleteFunc: func(obj interface{}) {
			node, ok := obj.(*corev1.Node)
			if !ok {
				logger.Errorf("excepted type Node, got %T", obj)
				return
			}
			decClusterNodeCount()
			objs.Del(node.Name)
		},
	})
	go informer.Run(ctx.Done())

	synced := k8sutils.WaitForNamedCacheSync(ctx, kindNode, informer)
	if !synced {
		return nil, errors.New("failed to sync Node caches")
	}
	return objs, nil
}

func toRefs(refs []metav1.OwnerReference) []OwnerRef {
	ret := make([]OwnerRef, 0, len(refs))
	for _, ref := range refs {
		ret = append(ret, OwnerRef{
			Kind: ref.Kind,
			Name: ref.Name,
		})
	}
	return ret
}
