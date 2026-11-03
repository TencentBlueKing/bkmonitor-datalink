// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package objectsref

type Refer interface {
	GetRefs(oid ObjectID) ([]OwnerRef, bool)
}

func Lookup(oid ObjectID, objs Refer, objsMap map[string]*Objects) *OwnerRef {
	return doLookup(oid, objs, objsMap, false)
}

func LookupOnce(oid ObjectID, objs Refer, objsMap map[string]*Objects) *OwnerRef {
	return doLookup(oid, objs, objsMap, true)
}

// LookupController follows the controller owner chain to the topmost workload.
// A broken or ambiguous known chain is rejected instead of reporting an intermediate owner.
func LookupController(oid ObjectID, objs Refer, objsMap map[string]*Objects) *OwnerRef {
	refs, ok := objs.GetRefs(oid)
	if !ok {
		return nil
	}

	ref, count := controllerRef(refs)
	if count != 1 {
		return nil
	}
	return lookupController(oid.Namespace, ref, objsMap, make(map[string]struct{}))
}

func controllerRef(refs []OwnerRef) (OwnerRef, int) {
	var controller OwnerRef
	count := 0
	for _, ref := range refs {
		if !ref.Controller {
			continue
		}
		count++
		if count == 1 {
			controller = ref
		}
	}
	return controller, count
}

func lookupController(namespace string, ref OwnerRef, objsMap map[string]*Objects, visited map[string]struct{}) *OwnerRef {
	objs, ok := objsMap[ref.Kind]
	if !ok {
		return nil
	}

	oid := ObjectID{Name: ref.Name, Namespace: namespace}
	key := ref.Kind + "/" + oid.String()
	if _, ok := visited[key]; ok {
		return nil
	}
	visited[key] = struct{}{}

	found, ok := objs.Get(oid)
	if !ok {
		return nil
	}
	parent := &OwnerRef{Kind: objs.Kind(), Name: ref.Name}

	next, count := controllerRef(found.OwnerRefs)
	if count == 0 {
		return parent
	}
	if count > 1 {
		return nil
	}
	if _, known := objsMap[next.Kind]; !known {
		return parent
	}
	return lookupController(namespace, next, objsMap, visited)
}

func doLookup(oid ObjectID, objs Refer, objsMap map[string]*Objects, once bool) *OwnerRef {
	refs, ok := objs.GetRefs(oid)
	if !ok {
		return nil
	}

	for _, ref := range refs {
		parent := lookup(oid.Namespace, ref, objsMap, once)
		if parent != nil {
			return parent
		}
	}

	return &OwnerRef{
		Kind: kindPod,
		Name: oid.Name,
	}
}

func lookup(namespace string, ref OwnerRef, objsMap map[string]*Objects, once bool) *OwnerRef {
	parent := &OwnerRef{}
	recursiveLookup(namespace, ref, objsMap, parent, once)
	if parent.Kind == "" && parent.Name == "" {
		return nil
	}
	return parent
}

func recursiveLookup(namespace string, ref OwnerRef, objsMap map[string]*Objects, parent *OwnerRef, once bool) {
	objs, ok := objsMap[ref.Kind]
	if !ok {
		return
	}

	found, ok := objs.Get(ObjectID{
		Name:      ref.Name,
		Namespace: namespace,
	})
	if !ok {
		return
	}
	parent.Kind = objs.Kind()
	parent.Name = ref.Name
	if once {
		return
	}

	for _, childRef := range found.OwnerRefs {
		recursiveLookup(namespace, childRef, objsMap, parent, once)
	}
}
