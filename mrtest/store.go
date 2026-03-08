package mrtest

import (
	"context"
	"fmt"
	"reflect"
	"sync"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ObjectStore is an in-memory store that implements both cache.Cache (for reads
// via Fetch/FetchAll) and client.Client (for writes via apply/delete). Outputs
// written via Patch are fed back into the store so that status reconcilers and
// Settle can see them.
type ObjectStore struct {
	mu      sync.RWMutex
	scheme  *runtime.Scheme
	objects map[objectKey]client.Object

	Applied       []client.Object
	Deleted       []types.NamespacedName
	StatusPatches []client.Object
}

type objectKey struct {
	GVK schema.GroupVersionKind
	Key types.NamespacedName
}

// NewObjectStore creates an ObjectStore seeded with the given objects.
func NewObjectStore(scheme *runtime.Scheme, objs ...client.Object) *ObjectStore {
	s := &ObjectStore{
		scheme:  scheme,
		objects: make(map[objectKey]client.Object),
	}
	for _, obj := range objs {
		s.SetObject(obj)
	}
	return s
}

// SetObject adds or updates an object in the store.
func (s *ObjectStore) SetObject(obj client.Object) {
	gvk := s.resolveGVK(obj)
	nn := types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[objectKey{GVK: gvk, Key: nn}] = obj.DeepCopyObject().(client.Object)
}

// DeleteObject removes an object from the store.
func (s *ObjectStore) DeleteObject(obj client.Object) {
	gvk := s.resolveGVK(obj)
	nn := types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, objectKey{GVK: gvk, Key: nn})
}

// GetObject retrieves a typed object from the store. Returns nil if not found.
func (s *ObjectStore) GetObject(gvk schema.GroupVersionKind, key types.NamespacedName) client.Object {
	s.mu.RLock()
	defer s.mu.RUnlock()
	obj, ok := s.objects[objectKey{GVK: gvk, Key: key}]
	if !ok {
		return nil
	}
	return obj.DeepCopyObject().(client.Object)
}

// Reset clears the Applied, Deleted, and StatusPatches slices.
func (s *ObjectStore) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Applied = nil
	s.Deleted = nil
	s.StatusPatches = nil
}

func (s *ObjectStore) resolveGVK(obj client.Object) schema.GroupVersionKind {
	gvks, _, err := s.scheme.ObjectKinds(obj)
	if err != nil || len(gvks) == 0 {
		panic(fmt.Sprintf("mrtest: type %T not registered in scheme", obj))
	}
	return gvks[0]
}

// --- cache.Cache implementation ---

func (s *ObjectStore) Get(_ context.Context, key client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
	gvks, _, err := s.scheme.ObjectKinds(obj)
	if err != nil {
		return fmt.Errorf("unknown type %T: %w", obj, err)
	}
	gvk := gvks[0]
	s.mu.RLock()
	stored, ok := s.objects[objectKey{GVK: gvk, Key: key}]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("not found: %v %v", gvk, key)
	}
	src := reflect.ValueOf(stored)
	dst := reflect.ValueOf(obj)
	if src.Type() != dst.Type() {
		return fmt.Errorf("type mismatch: stored %T, target %T", stored, obj)
	}
	dst.Elem().Set(src.Elem())
	return nil
}

func (s *ObjectStore) List(_ context.Context, list client.ObjectList, opts ...client.ListOption) error {
	listOpts := &client.ListOptions{}
	for _, o := range opts {
		o.ApplyToList(listOpts)
	}

	listGVK := list.GetObjectKind().GroupVersionKind()
	if listGVK.Kind == "" {
		gvks, _, err := s.scheme.ObjectKinds(list)
		if err == nil && len(gvks) > 0 {
			listGVK = gvks[0]
		}
	}

	itemKind := listGVK.Kind
	if len(itemKind) > 4 && itemKind[len(itemKind)-4:] == "List" {
		itemKind = itemKind[:len(itemKind)-4]
	}
	itemGVK := schema.GroupVersionKind{
		Group:   listGVK.Group,
		Version: listGVK.Version,
		Kind:    itemKind,
	}

	s.mu.RLock()
	var items []runtime.Object
	for k, obj := range s.objects {
		if k.GVK != itemGVK {
			continue
		}
		if listOpts.Namespace != "" && k.Key.Namespace != listOpts.Namespace {
			continue
		}
		if listOpts.LabelSelector != nil && !listOpts.LabelSelector.Matches(labels.Set(obj.GetLabels())) {
			continue
		}
		items = append(items, obj.DeepCopyObject())
	}
	s.mu.RUnlock()

	return meta.SetList(list, items)
}

func (s *ObjectStore) GetInformer(_ context.Context, _ client.Object, _ ...cache.InformerGetOption) (cache.Informer, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *ObjectStore) GetInformerForKind(_ context.Context, _ schema.GroupVersionKind, _ ...cache.InformerGetOption) (cache.Informer, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *ObjectStore) RemoveInformer(_ context.Context, _ client.Object) error {
	return fmt.Errorf("not implemented")
}
func (s *ObjectStore) Start(_ context.Context) error           { return nil }
func (s *ObjectStore) WaitForCacheSync(_ context.Context) bool { return true }
func (s *ObjectStore) IndexField(_ context.Context, _ client.Object, _ string, _ client.IndexerFunc) error {
	return fmt.Errorf("not implemented")
}

// --- client.Client implementation ---

func (s *ObjectStore) Scheme() *runtime.Scheme { return s.scheme }

func (s *ObjectStore) RESTMapper() meta.RESTMapper { return nil }

func (s *ObjectStore) GroupVersionKindFor(_ runtime.Object) (schema.GroupVersionKind, error) {
	return schema.GroupVersionKind{}, fmt.Errorf("not implemented")
}

func (s *ObjectStore) IsObjectNamespaced(_ runtime.Object) (bool, error) {
	return false, fmt.Errorf("not implemented")
}

func (s *ObjectStore) Apply(_ context.Context, _ runtime.ApplyConfiguration, _ ...client.ApplyOption) error {
	return nil
}

func (s *ObjectStore) Create(_ context.Context, _ client.Object, _ ...client.CreateOption) error {
	return fmt.Errorf("not implemented")
}

func (s *ObjectStore) Update(_ context.Context, _ client.Object, _ ...client.UpdateOption) error {
	return fmt.Errorf("not implemented")
}

func (s *ObjectStore) Delete(_ context.Context, obj client.Object, _ ...client.DeleteOption) error {
	nn := types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}
	s.mu.Lock()
	s.Deleted = append(s.Deleted, nn)
	gvk := s.resolveGVK(obj)
	delete(s.objects, objectKey{GVK: gvk, Key: nn})
	s.mu.Unlock()
	return nil
}

func (s *ObjectStore) DeleteAllOf(_ context.Context, _ client.Object, _ ...client.DeleteAllOfOption) error {
	return fmt.Errorf("not implemented")
}

func (s *ObjectStore) Patch(_ context.Context, obj client.Object, patch client.Patch, _ ...client.PatchOption) error {
	s.mu.Lock()
	if patch.Type() == types.ApplyPatchType {
		s.Applied = append(s.Applied, obj)
	}
	gvk := s.resolveGVK(obj)
	nn := types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}
	s.objects[objectKey{GVK: gvk, Key: nn}] = obj.DeepCopyObject().(client.Object)
	s.mu.Unlock()
	return nil
}

func (s *ObjectStore) SubResource(_ string) client.SubResourceClient {
	return nil
}

func (s *ObjectStore) Status() client.SubResourceWriter {
	return &storeStatusWriter{store: s}
}

type storeStatusWriter struct {
	store *ObjectStore
}

func (w *storeStatusWriter) Create(_ context.Context, _ client.Object, _ client.Object, _ ...client.SubResourceCreateOption) error {
	return nil
}

func (w *storeStatusWriter) Update(_ context.Context, _ client.Object, _ ...client.SubResourceUpdateOption) error {
	return nil
}

func (w *storeStatusWriter) Patch(_ context.Context, obj client.Object, _ client.Patch, _ ...client.SubResourcePatchOption) error {
	w.store.mu.Lock()
	w.store.StatusPatches = append(w.store.StatusPatches, obj)
	w.store.mu.Unlock()
	return nil
}

func (w *storeStatusWriter) Apply(_ context.Context, _ runtime.ApplyConfiguration, _ ...client.SubResourceApplyOption) error {
	return nil
}
