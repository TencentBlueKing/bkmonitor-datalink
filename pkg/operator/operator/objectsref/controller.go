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
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	batchv1beta1 "k8s.io/api/batch/v1beta1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	networkingv1beta "k8s.io/api/networking/v1beta1"
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

	// Pod 属性
	NodeName    string
	PodIP       string
	Labels      map[string]string
	Annotations map[string]string

	// Containers
	Containers []string
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
	resourceNodes     = "nodes"
	resourcePods      = "pods"
	resourceServices  = "services"
	resourceEndpoints = "endpoints"
	resourceIngresses = "ingresses"

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

	client kubernetes.Interface
	mm     *metricMonitor

	podObjs             *Objects
	replicaSetObjs      *Objects
	deploymentObjs      *Objects
	daemonSetObjs       *Objects
	statefulSetObjs     *Objects
	jobObjs             *Objects
	cronJobObjs         *Objects
	gameStatefulSetObjs *Objects
	gameDeploymentsObjs *Objects
	nodeObjs            *NodeMap
	serviceObjs         *ServiceMap
	endpointsObjs       *EndpointsMap
	ingressObjs         *IngressMap
}

func NewController(ctx context.Context, client kubernetes.Interface, tkexClient tkexversiond.Interface) (*ObjectsController, error) {
	ctx, cancel := context.WithCancel(ctx)
	controller := &ObjectsController{
		client: client,
		ctx:    ctx,
		cancel: cancel,
	}

	var err error
	resources := listServerPreferredResources(client.Discovery())

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

	controller.cronJobObjs, err = newCronJobObjects(ctx, sharedInformer, resources)
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

	tkexObjs, err := newTkexObjects(ctx, tkexClient, resources)
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
	defer ticker.Stop()

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
				Containers:  toContainers(pod.Spec.Containers),
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
				Containers:  toContainers(pod.Spec.Containers),
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
	go informer.Run(ctx.Done())

	synced := k8sutils.WaitForNamedCacheSync(ctx, kindPod, informer)
	if !synced {
		return nil, errors.New("failed to sync Pod caches")
	}
	return objs, nil
}

func newServiceObjects(ctx context.Context, sharedInformer informers.SharedInformerFactory) (*ServiceMap, error) {
	objs := NewServiceMap()

	genericInformer, err := sharedInformer.ForResource(corev1.SchemeGroupVersion.WithResource(resourceServices))
	if err != nil {
		return nil, err
	}

	informer := genericInformer.Informer()
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			service, ok := obj.(*corev1.Service)
			if !ok {
				logger.Errorf("excepted Service type, got %T", obj)
				return
			}
			objs.Set(service)
		},
		UpdateFunc: func(_, newObj interface{}) {
			service, ok := newObj.(*corev1.Service)
			if !ok {
				logger.Errorf("excepted Service type, got %T", newObj)
				return
			}
			objs.Set(service)
		},
		DeleteFunc: func(obj interface{}) {
			service, ok := obj.(*corev1.Service)
			if !ok {
				logger.Errorf("excepted Service type, got %T", obj)
				return
			}
			objs.Del(service)
		},
	})
	go informer.Run(ctx.Done())

	synced := k8sutils.WaitForNamedCacheSync(ctx, kindService, informer)
	if !synced {
		return nil, errors.New("failed to sync Service caches")
	}
	return objs, nil
}

func newEndpointsObjects(ctx context.Context, sharedInformer informers.SharedInformerFactory) (*EndpointsMap, error) {
	objs := NewEndpointsMap()

	genericInformer, err := sharedInformer.ForResource(corev1.SchemeGroupVersion.WithResource(resourceEndpoints))
	if err != nil {
		return nil, err
	}

	informer := genericInformer.Informer()
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			endpoints, ok := obj.(*corev1.Endpoints)
			if !ok {
				logger.Errorf("excepted Endpoints type, got %T", obj)
				return
			}
			objs.Set(endpoints)
		},
		UpdateFunc: func(_, newObj interface{}) {
			endpoints, ok := newObj.(*corev1.Endpoints)
			if !ok {
				logger.Errorf("excepted Endpoints type, got %T", newObj)
				return
			}
			objs.Set(endpoints)
		},
		DeleteFunc: func(obj interface{}) {
			endpoints, ok := obj.(*corev1.Endpoints)
			if !ok {
				logger.Errorf("excepted Endpoints type, got %T", obj)
				return
			}
			objs.Del(endpoints)
		},
	})
	go informer.Run(ctx.Done())

	synced := k8sutils.WaitForNamedCacheSync(ctx, kindEndpoints, informer)
	if !synced {
		return nil, errors.New("failed to sync Endpoints caches")
	}
	return objs, nil
}

func newIngressObjects(ctx context.Context, sharedInformer informers.SharedInformerFactory, resources map[GVRK]struct{}) (*IngressMap, error) {
	gvrk := GVRK{
		Group:    "networking.k8s.io",
		Version:  "v1",
		Resource: "ingresses",
		Kind:     "Ingress",
	}

	_, ok := resources[gvrk]
	if ok {
		return newIngressV1Objects(ctx, sharedInformer)
	}

	return newIngressV1BetaObjects(ctx, sharedInformer)
}

func newIngressV1Objects(ctx context.Context, sharedInformer informers.SharedInformerFactory) (*IngressMap, error) {
	objs := NewIngressMap()

	genericInformer, err := sharedInformer.ForResource(networkingv1.SchemeGroupVersion.WithResource(resourceIngresses))
	if err != nil {
		return nil, err
	}

	makeIngress := func(namespace, name string, rules []networkingv1.IngressRule) ingressEntity {
		set := make(map[string]struct{})
		for _, rule := range rules {
			if rule.HTTP == nil {
				continue
			}

			for _, path := range rule.HTTP.Paths {
				svc := path.Backend.Service
				if svc != nil {
					set[svc.Name] = struct{}{}
				}
			}
		}

		services := make([]string, 0, len(set))
		for k := range set {
			services = append(services, k)
		}

		return ingressEntity{
			namespace: namespace,
			name:      name,
			services:  services,
		}
	}

	informer := genericInformer.Informer()
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			ingress, ok := obj.(*networkingv1.Ingress)
			if !ok {
				logger.Errorf("excepted Ingress type, got %T", obj)
				return
			}
			objs.Set(makeIngress(ingress.Namespace, ingress.Name, ingress.Spec.Rules))
		},
		UpdateFunc: func(_, newObj interface{}) {
			ingress, ok := newObj.(*networkingv1.Ingress)
			if !ok {
				logger.Errorf("excepted Ingress type, got %T", newObj)
				return
			}
			objs.Set(makeIngress(ingress.Namespace, ingress.Name, ingress.Spec.Rules))
		},
		DeleteFunc: func(obj interface{}) {
			ingress, ok := obj.(*networkingv1.Ingress)
			if !ok {
				logger.Errorf("excepted Ingress type, got %T", obj)
				return
			}
			objs.Del(ingress.Namespace, ingress.Name)
		},
	})
	go informer.Run(ctx.Done())

	synced := k8sutils.WaitForNamedCacheSync(ctx, kindIngress, informer)
	if !synced {
		return nil, errors.New("failed to sync Ingress caches")
	}
	return objs, nil
}

func newIngressV1BetaObjects(ctx context.Context, sharedInformer informers.SharedInformerFactory) (*IngressMap, error) {
	objs := NewIngressMap()

	genericInformer, err := sharedInformer.ForResource(networkingv1beta.SchemeGroupVersion.WithResource(resourceIngresses))
	if err != nil {
		return nil, err
	}

	makeIngress := func(namespace, name string, rules []networkingv1beta.IngressRule) ingressEntity {
		set := make(map[string]struct{})
		for _, rule := range rules {
			if rule.HTTP == nil {
				continue
			}

			for _, path := range rule.HTTP.Paths {
				set[path.Backend.ServiceName] = struct{}{}
			}
		}

		services := make([]string, 0, len(set))
		for k := range set {
			services = append(services, k)
		}

		return ingressEntity{
			namespace: namespace,
			name:      name,
			services:  services,
		}
	}

	informer := genericInformer.Informer()
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			ingress, ok := obj.(*networkingv1beta.Ingress)
			if !ok {
				logger.Errorf("excepted Ingress type, got %T", obj)
				return
			}
			objs.Set(makeIngress(ingress.Namespace, ingress.Name, ingress.Spec.Rules))
		},
		UpdateFunc: func(_, newObj interface{}) {
			ingress, ok := newObj.(*networkingv1beta.Ingress)
			if !ok {
				logger.Errorf("excepted Ingress type, got %T", newObj)
				return
			}
			objs.Set(makeIngress(ingress.Namespace, ingress.Name, ingress.Spec.Rules))
		},
		DeleteFunc: func(obj interface{}) {
			ingress, ok := obj.(*networkingv1beta.Ingress)
			if !ok {
				logger.Errorf("excepted Ingress type, got %T", obj)
				return
			}
			objs.Del(ingress.Namespace, ingress.Name)
		},
	})
	go informer.Run(ctx.Done())

	synced := k8sutils.WaitForNamedCacheSync(ctx, kindIngress, informer)
	if !synced {
		return nil, errors.New("failed to sync Ingress caches")
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
				logger.Errorf("excepted ReplicaSet type, got %T", obj)
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
				logger.Errorf("excepted ReplicaSet type, got %T", newObj)
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
				logger.Errorf("excepted ReplicaSet type, got %T", obj)
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
				logger.Errorf("excepted Deployment type, got %T", obj)
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
				logger.Errorf("excepted Deployment type, got %T", newObj)
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
				logger.Errorf("excepted Deployment type, got %T", obj)
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
				logger.Errorf("excepted DaemonSet type, got %T", obj)
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
				logger.Errorf("excepted DaemonSet type, got %T", newObj)
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
				logger.Errorf("excepted DaemonSet type, got %T", obj)
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
				logger.Errorf("excepted StatefulSet type, got %T", obj)
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
				logger.Errorf("excepted StatefulSet type, got %T", newObj)
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
				logger.Errorf("excepted StatefulSet type, got %T", obj)
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
				logger.Errorf("excepted Job type, got %T", obj)
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
				logger.Errorf("excepted Job type, got %T", newObj)
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
				logger.Errorf("excepted Job type, got %T", obj)
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

func newCronJobObjects(ctx context.Context, sharedInformer informers.SharedInformerFactory, resources map[GVRK]struct{}) (*Objects, error) {
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

func newCronJobV1BetaObjects(ctx context.Context, sharedInformer informers.SharedInformerFactory) (*Objects, error) {
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
				logger.Errorf("excepted CronJob type, got %T", obj)
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
				logger.Errorf("excepted CronJob type, got %T", newObj)
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
				logger.Errorf("excepted CronJob type, got %T", obj)
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
				logger.Errorf("excepted CronJob type, got %T", obj)
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
				logger.Errorf("excepted CronJob type, got %T", newObj)
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
				logger.Errorf("excepted CronJob type, got %T", obj)
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
				logger.Errorf("excepted Node type, got %T", obj)
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
				logger.Errorf("excepted Node type, got %T", newObj)
				return
			}
			if err := objs.Set(node); err != nil {
				logger.Errorf("failed to set node obj, err: %v", err)
			}
		},
		DeleteFunc: func(obj interface{}) {
			node, ok := obj.(*corev1.Node)
			if !ok {
				logger.Errorf("excepted Node type, got %T", obj)
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

func toContainers(specContainers []corev1.Container) []string {
	containers := make([]string, 0, len(specContainers))
	for _, sc := range specContainers {
		containers = append(containers, sc.Name)
	}
	return containers
}
