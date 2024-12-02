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

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	batchv1beta1 "k8s.io/api/batch/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/metadata/metadatainformer"
	"k8s.io/client-go/tools/cache"

	bkversioned "github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/client/clientset/versioned"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/k8sutils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// OwnerRef 代表 Owner 对象引用信息
type OwnerRef struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type ContainerKey struct {
	Name  string
	ID    string
	Image string
}

// Object 代表 workload 对象
type Object struct {
	ID        ObjectID
	OwnerRefs []OwnerRef

	// Pod 属性
	NodeName string
	PodIP    string

	// Metadata 属性
	Labels      map[string]string
	Annotations map[string]string

	// Containers
	Containers []ContainerKey
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

func (o *Objects) GetByNamespace(namespace string) []Object {
	o.mut.Lock()
	defer o.mut.Unlock()

	var ret []Object
	for _, obj := range o.objs {
		if obj.ID.Namespace == namespace {
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
	kindService         = "Service"
	kindEndpoints       = "Endpoints"
	kindIngress         = "Ingress"
	kindSecret          = "Secret"
	kindDeployment      = "Deployment"
	kindReplicaSet      = "ReplicaSet"
	kindStatefulSet     = "StatefulSet"
	kindDaemonSet       = "DaemonSet"
	kindJob             = "Job"
	kindCronJob         = "CronJob"
	kindGameStatefulSet = "GameStatefulSet"
	kindGameDeployment  = "GameDeployment"
	kindBkLogConfig     = "BkLogConfig"
)

const (
	resourceNodes     = "nodes"
	resourcePods      = "pods"
	resourceServices  = "services"
	resourceEndpoints = "endpoints"
	resourceIngresses = "ingresses"
	resourceSecrets   = "secrets"

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

	// logging
	resourceBkLogConfigs = "bklogconfigs"
)

func partialObjectMetadataStrip(obj interface{}) (interface{}, error) {
	partialMeta, ok := obj.(*metav1.PartialObjectMetadata)
	if !ok {
		// Don't do anything if the cast isn't successful.
		// The object might be of type "cache.DeletedFinalStateUnknown".
		return obj, nil
	}

	partialMeta.Annotations = nil
	partialMeta.Labels = nil
	partialMeta.ManagedFields = nil
	partialMeta.Finalizers = nil

	return partialMeta, nil
}

// ObjectsController 负责获取并更新 workload 资源的元信息
type ObjectsController struct {
	ctx    context.Context
	cancel context.CancelFunc

	client kubernetes.Interface

	podObjs             *Objects
	replicaSetObjs      *Objects
	deploymentObjs      *Objects
	daemonSetObjs       *Objects
	statefulSetObjs     *Objects
	jobObjs             *Objects
	cronJobObjs         *Objects
	gameStatefulSetObjs *Objects
	gameDeploymentsObjs *Objects
	secretObjs          *Objects
	nodeObjs            *NodeMap
	serviceObjs         *ServiceMap
	endpointsObjs       *EndpointsMap
	ingressObjs         *IngressMap
	bkLogConfigObjs     *BkLogConfigMap
}

func NewController(ctx context.Context, client kubernetes.Interface, mClient metadata.Interface, bkClient bkversioned.Interface) (*ObjectsController, error) {
	ctx, cancel := context.WithCancel(ctx)
	controller := &ObjectsController{
		client: client,
		ctx:    ctx,
		cancel: cancel,
	}

	var err error
	resources := listServerPreferredResources(client.Discovery())

	// Standard/SharedInformer
	sharedInformer := informers.NewSharedInformerFactoryWithOptions(client, define.ReSyncPeriod, informers.WithNamespace(metav1.NamespaceAll))
	controller.podObjs, err = newPodObjects(ctx, sharedInformer)
	if err != nil {
		return nil, err
	}

	controller.nodeObjs, err = newNodeObjects(ctx, sharedInformer)
	if err != nil {
		return nil, err
	}

	controller.serviceObjs, err = newServiceObjects(ctx, sharedInformer)
	if err != nil {
		return nil, err
	}

	controller.endpointsObjs, err = newEndpointsObjects(ctx, sharedInformer)
	if err != nil {
		return nil, err
	}

	controller.ingressObjs, err = newIngressObjects(ctx, sharedInformer, resources)
	if err != nil {
		return nil, err
	}

	// Metadata/SharedInformer
	metaSharedInformer := metadatainformer.NewFilteredSharedInformerFactory(mClient, define.ReSyncPeriod, metav1.NamespaceAll, nil)
	controller.replicaSetObjs, err = newReplicaSetObjects(ctx, metaSharedInformer)
	if err != nil {
		return nil, err
	}

	controller.deploymentObjs, err = newDeploymentObjects(ctx, metaSharedInformer)
	if err != nil {
		return nil, err
	}

	controller.daemonSetObjs, err = newDaemenSetObjects(ctx, metaSharedInformer)
	if err != nil {
		return nil, err
	}

	controller.statefulSetObjs, err = newStatefulSetObjects(ctx, metaSharedInformer)
	if err != nil {
		return nil, err
	}

	controller.jobObjs, err = newJobObjects(ctx, metaSharedInformer)
	if err != nil {
		return nil, err
	}

	controller.cronJobObjs, err = newCronJobObjects(ctx, metaSharedInformer, resources)
	if err != nil {
		return nil, err
	}

	// configs.G().MonitorNamespace SharedInformer
	monitorSharedInformer := metadatainformer.NewFilteredSharedInformerFactory(mClient, define.ReSyncPeriod, configs.G().MonitorNamespace, nil)
	controller.secretObjs, err = newSecretObjects(ctx, monitorSharedInformer)
	if err != nil {
		return nil, err
	}

	// Extend/Workload
	tkexObjs, err := newTkexObjects(ctx, metaSharedInformer, resources)
	if err != nil {
		return nil, err
	}
	controller.gameStatefulSetObjs = tkexObjs.gamestatefulset
	controller.gameDeploymentsObjs = tkexObjs.gamedeployment

	controller.bkLogConfigObjs, err = newBklogConfigObjects(ctx, bkClient, resources)
	if err != nil {
		return nil, err
	}

	go controller.recordMetrics()

	return controller, nil
}

func (oc *ObjectsController) NodeNames() []string {
	return oc.nodeObjs.Names()
}

func (oc *ObjectsController) NodeIPs() map[string]struct{} {
	return oc.nodeObjs.IPs()
}

func (oc *ObjectsController) NodeCount() int {
	return oc.nodeObjs.Count()
}

func (oc *ObjectsController) NodeNameExists(s string) (string, bool) {
	return oc.nodeObjs.NameExists(s)
}

func (oc *ObjectsController) SecretObjs() []Object {
	return oc.secretObjs.GetAll()
}

func (oc *ObjectsController) NodeObjs() []*corev1.Node {
	return oc.nodeObjs.GetAll()
}

func (oc *ObjectsController) Stop() {
	oc.cancel()
}

func (oc *ObjectsController) recordMetrics() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-oc.ctx.Done():
			return

		case <-ticker.C:
			stats := make(map[string]int)
			for _, count := range oc.podObjs.Counter() {
				stats[kindPod] += count
			}
			for kind, objs := range oc.objsMap() {
				for _, count := range objs.Counter() {
					stats[kind] += count
				}
			}
			stats[kindService] = oc.serviceObjs.Count()
			stats[kindIngress] = oc.ingressObjs.Count()
			stats[kindEndpoints] = oc.endpointsObjs.Count()
			stats[kindBkLogConfig] = oc.bkLogConfigObjs.Count()
			stats[kindNode] = oc.nodeObjs.Count()
			SetWorkloadCount(stats)
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
	err = informer.SetTransform(func(obj interface{}) (interface{}, error) {
		pod, ok := obj.(*corev1.Pod)
		if !ok {
			logger.Errorf("excepted Pod type, got %T", obj)
			return obj, nil // 原路返回
		}
		newObj := &corev1.Pod{}
		newObj.Name = pod.Name
		newObj.Namespace = pod.Namespace
		newObj.OwnerReferences = pod.OwnerReferences
		newObj.Spec.NodeName = pod.Spec.NodeName
		newObj.Labels = pod.Labels
		newObj.Annotations = pod.Annotations
		newObj.Status.PodIP = pod.Status.PodIP
		newObj.Status.ContainerStatuses = pod.Status.ContainerStatuses
		newObj.Spec.Containers = pod.Spec.Containers

		return newObj, nil
	})
	if err != nil {
		return nil, err
	}

	_, err = informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			pod, ok := obj.(*corev1.Pod)
			if !ok {
				logger.Errorf("excepted Pod type, got %T", obj)
				return
			}

			objs.Set(Object{
				ID: ObjectID{
					Name:      pod.Name,
					Namespace: pod.Namespace,
				},
				OwnerRefs:   toRefs(pod.OwnerReferences),
				NodeName:    pod.Spec.NodeName,
				Labels:      pod.Labels,
				Annotations: pod.Annotations,
				PodIP:       pod.Status.PodIP,
				Containers:  toContainerKey(pod),
			})
		},
		UpdateFunc: func(_, newObj interface{}) {
			pod, ok := newObj.(*corev1.Pod)
			if !ok {
				logger.Errorf("excepted Pod type, got %T", newObj)
				return
			}
			objs.Set(Object{
				ID: ObjectID{
					Name:      pod.Name,
					Namespace: pod.Namespace,
				},
				OwnerRefs:   toRefs(pod.OwnerReferences),
				NodeName:    pod.Spec.NodeName,
				Labels:      pod.Labels,
				Annotations: pod.Annotations,
				PodIP:       pod.Status.PodIP,
				Containers:  toContainerKey(pod),
			})
		},
		DeleteFunc: func(obj interface{}) {
			pod, ok := obj.(*corev1.Pod)
			if !ok {
				logger.Errorf("excepted Pod type, got %T", obj)
				return
			}
			objs.Del(ObjectID{
				Name:      pod.Name,
				Namespace: pod.Namespace,
			})
		},
	})
	if err != nil {
		return nil, err
	}

	go informer.Run(ctx.Done())

	synced := k8sutils.WaitForNamedCacheSync(ctx, kindPod, informer)
	if !synced {
		return nil, errors.New("failed to sync Pod caches")
	}
	return objs, nil
}

func newSecretObjects(ctx context.Context, sharedInformer metadatainformer.SharedInformerFactory) (*Objects, error) {
	genericInformer := sharedInformer.ForResource(corev1.SchemeGroupVersion.WithResource(resourceSecrets))
	objs := NewObjects(kindSecret)

	informer := genericInformer.Informer()
	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			secret, ok := obj.(*metav1.PartialObjectMetadata)
			if !ok {
				logger.Errorf("excepted Secret/PartialObjectMetadata type, got %T", obj)
				return
			}
			objs.Set(Object{
				ID: ObjectID{
					Name:      secret.Name,
					Namespace: secret.Namespace,
				},
				Labels: secret.Labels,
			})
		},
		UpdateFunc: func(_, newObj interface{}) {
			secret, ok := newObj.(*metav1.PartialObjectMetadata)
			if !ok {
				logger.Errorf("excepted Secret/PartialObjectMetadata type, got %T", newObj)
				return
			}
			objs.Set(Object{
				ID: ObjectID{
					Name:      secret.Name,
					Namespace: secret.Namespace,
				},
				Labels: secret.Labels,
			})
		},
		DeleteFunc: func(obj interface{}) {
			secret, ok := obj.(*metav1.PartialObjectMetadata)
			if !ok {
				logger.Errorf("excepted Secret/PartialObjectMetadata type, got %T", obj)
				return
			}
			objs.Del(ObjectID{
				Name:      secret.Name,
				Namespace: secret.Namespace,
			})
		},
	})
	if err != nil {
		return nil, err
	}

	go informer.Run(ctx.Done())

	synced := k8sutils.WaitForNamedCacheSync(ctx, kindSecret, informer)
	if !synced {
		return nil, errors.New("failed to sync Secret caches")
	}
	return objs, nil
}

func newReplicaSetObjects(ctx context.Context, sharedInformer metadatainformer.SharedInformerFactory) (*Objects, error) {
	genericInformer := sharedInformer.ForResource(appsv1.SchemeGroupVersion.WithResource(resourceReplicaSets))
	objs := NewObjects(kindReplicaSet)

	informer := genericInformer.Informer()
	if err := informer.SetTransform(partialObjectMetadataStrip); err != nil {
		return nil, err
	}

	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			replicaSet, ok := obj.(*metav1.PartialObjectMetadata)
			if !ok {
				logger.Errorf("excepted ReplicaSet/PartialObjectMetadata type, got %T", obj)
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
			replicaSet, ok := newObj.(*metav1.PartialObjectMetadata)
			if !ok {
				logger.Errorf("excepted ReplicaSet/PartialObjectMetadata type, got %T", newObj)
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
			replicaSet, ok := obj.(*metav1.PartialObjectMetadata)
			if !ok {
				logger.Errorf("excepted ReplicaSet/PartialObjectMetadata type, got %T", obj)
				return
			}
			objs.Del(ObjectID{
				Name:      replicaSet.Name,
				Namespace: replicaSet.Namespace,
			})
		},
	})
	if err != nil {
		return nil, err
	}

	go informer.Run(ctx.Done())

	synced := k8sutils.WaitForNamedCacheSync(ctx, kindReplicaSet, informer)
	if !synced {
		return nil, errors.New("failed to sync ReplicaSet caches")
	}
	return objs, nil
}

func newDeploymentObjects(ctx context.Context, sharedInformer metadatainformer.SharedInformerFactory) (*Objects, error) {
	genericInformer := sharedInformer.ForResource(appsv1.SchemeGroupVersion.WithResource(resourceDeployments))
	objs := NewObjects(kindDeployment)

	informer := genericInformer.Informer()
	if err := informer.SetTransform(partialObjectMetadataStrip); err != nil {
		return nil, err
	}

	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			deployment, ok := obj.(*metav1.PartialObjectMetadata)
			if !ok {
				logger.Errorf("excepted Deployment/PartialObjectMetadata type, got %T", obj)
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
			deployment, ok := newObj.(*metav1.PartialObjectMetadata)
			if !ok {
				logger.Errorf("excepted Deployment/PartialObjectMetadata type, got %T", newObj)
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
			deployment, ok := obj.(*metav1.PartialObjectMetadata)
			if !ok {
				logger.Errorf("excepted Deployment/PartialObjectMetadata type, got %T", obj)
				return
			}
			objs.Del(ObjectID{
				Name:      deployment.Name,
				Namespace: deployment.Namespace,
			})
		},
	})
	if err != nil {
		return nil, err
	}

	go informer.Run(ctx.Done())

	synced := k8sutils.WaitForNamedCacheSync(ctx, kindDeployment, informer)
	if !synced {
		return nil, errors.New("failed to sync Deployment caches")
	}
	return objs, nil
}

func newDaemenSetObjects(ctx context.Context, sharedInformer metadatainformer.SharedInformerFactory) (*Objects, error) {
	genericInformer := sharedInformer.ForResource(appsv1.SchemeGroupVersion.WithResource(resourceDaemonSets))
	objs := NewObjects(kindDaemonSet)

	informer := genericInformer.Informer()
	if err := informer.SetTransform(partialObjectMetadataStrip); err != nil {
		return nil, err
	}

	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			daemonSet, ok := obj.(*metav1.PartialObjectMetadata)
			if !ok {
				logger.Errorf("excepted DaemonSet/PartialObjectMetadata type, got %T", obj)
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
			daemonSet, ok := newObj.(*metav1.PartialObjectMetadata)
			if !ok {
				logger.Errorf("excepted DaemonSet/PartialObjectMetadata type, got %T", newObj)
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
			daemonSet, ok := obj.(*metav1.PartialObjectMetadata)
			if !ok {
				logger.Errorf("excepted DaemonSet/PartialObjectMetadata type, got %T", obj)
				return
			}
			objs.Del(ObjectID{
				Name:      daemonSet.Name,
				Namespace: daemonSet.Namespace,
			})
		},
	})
	if err != nil {
		return nil, err
	}

	go informer.Run(ctx.Done())

	synced := k8sutils.WaitForNamedCacheSync(ctx, kindDaemonSet, informer)
	if !synced {
		return nil, errors.New("failed to sync DaemonSet caches")
	}
	return objs, nil
}

func newStatefulSetObjects(ctx context.Context, sharedInformer metadatainformer.SharedInformerFactory) (*Objects, error) {
	genericInformer := sharedInformer.ForResource(appsv1.SchemeGroupVersion.WithResource(resourceStatefulSets))
	objs := NewObjects(kindStatefulSet)

	informer := genericInformer.Informer()
	if err := informer.SetTransform(partialObjectMetadataStrip); err != nil {
		return nil, err
	}

	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			statefulSet, ok := obj.(*metav1.PartialObjectMetadata)
			if !ok {
				logger.Errorf("excepted StatefulSet/PartialObjectMetadata type, got %T", obj)
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
			statefulSet, ok := newObj.(*metav1.PartialObjectMetadata)
			if !ok {
				logger.Errorf("excepted StatefulSet/PartialObjectMetadata type, got %T", newObj)
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
			statefulSet, ok := obj.(*metav1.PartialObjectMetadata)
			if !ok {
				logger.Errorf("excepted StatefulSet/PartialObjectMetadata type, got %T", obj)
				return
			}
			objs.Del(ObjectID{
				Name:      statefulSet.Name,
				Namespace: statefulSet.Namespace,
			})
		},
	})
	if err != nil {
		return nil, err
	}

	go informer.Run(ctx.Done())

	synced := k8sutils.WaitForNamedCacheSync(ctx, kindStatefulSet, informer)
	if !synced {
		return nil, errors.New("failed to sync StatefulSet caches")
	}
	return objs, nil
}

func newJobObjects(ctx context.Context, sharedInformer metadatainformer.SharedInformerFactory) (*Objects, error) {
	genericInformer := sharedInformer.ForResource(batchv1.SchemeGroupVersion.WithResource(resourceJobs))
	objs := NewObjects(kindJob)

	informer := genericInformer.Informer()
	if err := informer.SetTransform(partialObjectMetadataStrip); err != nil {
		return nil, err
	}

	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			job, ok := obj.(*metav1.PartialObjectMetadata)
			if !ok {
				logger.Errorf("excepted Job/PartialObjectMetadata type, got %T", obj)
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
			job, ok := newObj.(*metav1.PartialObjectMetadata)
			if !ok {
				logger.Errorf("excepted Job/PartialObjectMetadata type, got %T", newObj)
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
			job, ok := obj.(*metav1.PartialObjectMetadata)
			if !ok {
				logger.Errorf("excepted Job/PartialObjectMetadata type, got %T", obj)
				return
			}
			objs.Del(ObjectID{
				Name:      job.Name,
				Namespace: job.Namespace,
			})
		},
	})
	if err != nil {
		return nil, err
	}

	go informer.Run(ctx.Done())

	synced := k8sutils.WaitForNamedCacheSync(ctx, kindJob, informer)
	if !synced {
		return nil, errors.New("failed to sync Job caches")
	}
	return objs, nil
}

func newCronJobObjects(ctx context.Context, sharedInformer metadatainformer.SharedInformerFactory, resources map[GVRK]struct{}) (*Objects, error) {
	gvrk := GVRK{
		Group:    "batch",
		Version:  "v1",
		Resource: "cronjobs",
		Kind:     "CronJob",
	}

	_, ok := resources[gvrk]
	if ok {
		return newCronJobV1Objects(ctx, sharedInformer)
	}

	return newCronJobV1BetaObjects(ctx, sharedInformer)
}

func newCronJobV1BetaObjects(ctx context.Context, sharedInformer metadatainformer.SharedInformerFactory) (*Objects, error) {
	genericInformer := sharedInformer.ForResource(batchv1beta1.SchemeGroupVersion.WithResource(resourceCronJobs))
	objs := NewObjects(kindCronJob)

	informer := genericInformer.Informer()
	if err := informer.SetTransform(partialObjectMetadataStrip); err != nil {
		return nil, err
	}

	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			cronJob, ok := obj.(*metav1.PartialObjectMetadata)
			if !ok {
				logger.Errorf("excepted CronJob/PartialObjectMetadata type, got %T", obj)
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
			cronJob, ok := newObj.(*metav1.PartialObjectMetadata)
			if !ok {
				logger.Errorf("excepted CronJob/PartialObjectMetadata type, got %T", newObj)
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
			cronJob, ok := obj.(*metav1.PartialObjectMetadata)
			if !ok {
				logger.Errorf("excepted CronJob/PartialObjectMetadata type, got %T", obj)
				return
			}
			objs.Del(ObjectID{
				Name:      cronJob.Name,
				Namespace: cronJob.Namespace,
			})
		},
	})
	if err != nil {
		return nil, err
	}

	go informer.Run(ctx.Done())

	synced := k8sutils.WaitForNamedCacheSync(ctx, kindCronJob, informer)
	if !synced {
		return nil, errors.New("failed to sync CronJob caches")
	}
	return objs, nil
}

func newCronJobV1Objects(ctx context.Context, sharedInformer metadatainformer.SharedInformerFactory) (*Objects, error) {
	genericInformer := sharedInformer.ForResource(batchv1.SchemeGroupVersion.WithResource(resourceCronJobs))
	objs := NewObjects(kindCronJob)

	informer := genericInformer.Informer()
	if err := informer.SetTransform(partialObjectMetadataStrip); err != nil {
		return nil, err
	}

	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			cronJob, ok := obj.(*metav1.PartialObjectMetadata)
			if !ok {
				logger.Errorf("excepted CronJob/PartialObjectMetadata type, got %T", obj)
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
			cronJob, ok := newObj.(*metav1.PartialObjectMetadata)
			if !ok {
				logger.Errorf("excepted CronJob/PartialObjectMetadata type, got %T", newObj)
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
			cronJob, ok := obj.(*metav1.PartialObjectMetadata)
			if !ok {
				logger.Errorf("excepted CronJob/PartialObjectMetadata type, got %T", obj)
				return
			}
			objs.Del(ObjectID{
				Name:      cronJob.Name,
				Namespace: cronJob.Namespace,
			})
		},
	})
	if err != nil {
		return nil, err
	}

	go informer.Run(ctx.Done())

	synced := k8sutils.WaitForNamedCacheSync(ctx, kindCronJob, informer)
	if !synced {
		return nil, errors.New("failed to sync CronJob caches")
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

func toContainerKey(pod *corev1.Pod) []ContainerKey {
	var containers []ContainerKey
	for _, sc := range pod.Status.ContainerStatuses {
		containers = append(containers, ContainerKey{
			Name:  sc.Name,
			ID:    sc.ContainerID,
			Image: sc.ImageID,
		})
	}
	return containers
}
