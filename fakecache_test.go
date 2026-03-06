package makereconcile

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// fakeCache implements cache.Cache for testing. Only Get is implemented;
// other methods panic to surface unexpected usage.
type fakeCache struct {
	objects map[types.NamespacedName]runtime.Object
}

func (f *fakeCache) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	stored, ok := f.objects[key]
	if !ok {
		return fmt.Errorf("not found: %v", key)
	}
	// Copy via reflect to populate the target obj.
	src := reflect.ValueOf(stored)
	dst := reflect.ValueOf(obj)
	if src.Type() != dst.Type() {
		return fmt.Errorf("type mismatch: stored %T, target %T", stored, obj)
	}
	dst.Elem().Set(src.Elem())
	return nil
}

func (f *fakeCache) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	return fmt.Errorf("fakeCache.List not implemented")
}

// Stubs to satisfy the cache.Cache interface.

func (f *fakeCache) GetInformer(ctx context.Context, obj client.Object, opts ...cache.InformerGetOption) (cache.Informer, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *fakeCache) GetInformerForKind(ctx context.Context, gvk schema.GroupVersionKind, opts ...cache.InformerGetOption) (cache.Informer, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *fakeCache) RemoveInformer(ctx context.Context, obj client.Object) error {
	return fmt.Errorf("not implemented")
}
func (f *fakeCache) Start(ctx context.Context) error                         { return nil }
func (f *fakeCache) WaitForCacheSync(ctx context.Context) bool               { return true }
func (f *fakeCache) IndexField(ctx context.Context, obj client.Object, field string, extractValue client.IndexerFunc) error {
	return fmt.Errorf("not implemented")
}

// fakeClient implements client.Client with no-op writes for testing.
// It supports List (for gcOrphans) and tracks Delete calls.
type fakeClient struct {
	client.Client
	statusPatches  int
	statusPatchErr error

	// listObjects are returned by List, keyed by item GVK (not the "List" GVK).
	listObjects map[schema.GroupVersionKind][]unstructured.Unstructured

	// deleted records which objects were deleted, for test assertions.
	deleted []types.NamespacedName
}

func (f *fakeClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	return nil
}

func (f *fakeClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	f.deleted = append(f.deleted, types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()})
	return nil
}

func (f *fakeClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	listOpts := &client.ListOptions{}
	for _, o := range opts {
		o.ApplyToList(listOpts)
	}

	uList, ok := list.(*unstructured.UnstructuredList)
	if !ok {
		return fmt.Errorf("fakeClient.List only supports UnstructuredList")
	}

	gvk := uList.GetObjectKind().GroupVersionKind()
	itemGVK := schema.GroupVersionKind{
		Group:   gvk.Group,
		Version: gvk.Version,
		Kind:    strings.TrimSuffix(gvk.Kind, "List"),
	}

	var filtered []unstructured.Unstructured
	for _, obj := range f.listObjects[itemGVK] {
		if listOpts.LabelSelector != nil && !listOpts.LabelSelector.Matches(labels.Set(obj.GetLabels())) {
			continue
		}
		filtered = append(filtered, obj)
	}
	uList.Items = filtered
	return nil
}

func (f *fakeClient) Status() client.SubResourceWriter {
	return &fakeStatusWriter{fc: f}
}

type fakeStatusWriter struct {
	fc *fakeClient
}

func (f *fakeStatusWriter) Create(ctx context.Context, obj client.Object, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	return nil
}

func (f *fakeStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	return nil
}

func (f *fakeStatusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	f.fc.statusPatches++
	return f.fc.statusPatchErr
}

func (f *fakeStatusWriter) Apply(ctx context.Context, obj runtime.ApplyConfiguration, opts ...client.SubResourceApplyOption) error {
	return nil
}
