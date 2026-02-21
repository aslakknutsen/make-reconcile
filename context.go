package makereconcile

import (
	"context"
	"reflect"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// HandlerContext is passed to sub-reconciler functions. It tracks which
// resources are read via Fetch so the framework can build a dependency graph.
type HandlerContext struct {
	ctx        context.Context
	mgr        *Manager
	narrowDeps []depKey
	broadDeps  []depKeyBroad
}

func newHandlerContext(ctx context.Context, mgr *Manager) *HandlerContext {
	return &HandlerContext{ctx: ctx, mgr: mgr}
}

// Filter narrows the set of resources returned by Fetch/FetchAll.
type Filter struct {
	Name      string
	Namespace string
	Labels    map[string]string
}

// FilterName returns a Filter matching a specific name and namespace.
func FilterName(name, namespace string) Filter {
	return Filter{Name: name, Namespace: namespace}
}

// FilterNamespace returns a Filter matching a namespace.
func FilterNamespace(namespace string) Filter {
	return Filter{Namespace: namespace}
}

// FilterLabel returns a Filter matching labels.
func FilterLabel(lbls map[string]string) Filter {
	return Filter{Labels: lbls}
}

// Fetch reads a single resource from the collection matching the given filters.
// The dependency is recorded so the framework knows to re-run this sub-reconciler
// when the fetched resource changes. Returns the zero value of T if not found.
func Fetch[T client.Object](hc *HandlerContext, col *Collection[T], filters ...Filter) T {
	f := mergeFilters(filters)

	if f.Name != "" {
		nn := types.NamespacedName{Name: f.Name, Namespace: f.Namespace}
		hc.narrowDeps = append(hc.narrowDeps, depKey{GVK: col.gvk, NamespacedName: nn})

		obj := newTypedObject[T]()
		if err := hc.mgr.cache.Get(hc.ctx, nn, obj); err != nil {
			var zero T
			return zero
		}
		return obj
	}

	results := FetchAll(hc, col, filters...)
	if len(results) > 0 {
		return results[0]
	}
	var zero T
	return zero
}

// FetchAll reads all resources from the collection matching the given filters.
// Records a broad dependency so the framework re-runs this sub-reconciler when
// any matching resource in the namespace changes.
func FetchAll[T client.Object](hc *HandlerContext, col *Collection[T], filters ...Filter) []T {
	f := mergeFilters(filters)

	hc.broadDeps = append(hc.broadDeps, depKeyBroad{GVK: col.gvk, Namespace: f.Namespace})

	list := newListForGVK(hc.mgr.scheme, col.gvk)
	var opts []client.ListOption
	if f.Namespace != "" {
		opts = append(opts, client.InNamespace(f.Namespace))
	}
	if len(f.Labels) > 0 {
		opts = append(opts, client.MatchingLabelsSelector{
			Selector: labels.SelectorFromSet(f.Labels),
		})
	}

	if err := hc.mgr.cache.List(hc.ctx, list, opts...); err != nil {
		return nil
	}

	rawItems, err := meta.ExtractList(list)
	if err != nil {
		return nil
	}

	var result []T
	for _, raw := range rawItems {
		if typed, ok := raw.(T); ok {
			result = append(result, typed)
		}
	}
	return result
}

func mergeFilters(filters []Filter) Filter {
	var merged Filter
	for _, f := range filters {
		if f.Name != "" {
			merged.Name = f.Name
		}
		if f.Namespace != "" {
			merged.Namespace = f.Namespace
		}
		if f.Labels != nil {
			if merged.Labels == nil {
				merged.Labels = make(map[string]string)
			}
			for k, v := range f.Labels {
				merged.Labels[k] = v
			}
		}
	}
	return merged
}

// newTypedObject creates a new allocated instance of T.
// T is expected to be a pointer type like *corev1.Pod.
func newTypedObject[T client.Object]() T {
	var zero T
	t := reflect.TypeOf(zero).Elem()
	return reflect.New(t).Interface().(T)
}

// newListForGVK creates an ObjectList for the given GVK using the scheme.
// Falls back to UnstructuredList if the list type isn't registered.
func newListForGVK(s *runtime.Scheme, gvk schema.GroupVersionKind) client.ObjectList {
	listGVK := schema.GroupVersionKind{
		Group:   gvk.Group,
		Version: gvk.Version,
		Kind:    gvk.Kind + "List",
	}
	obj, err := s.New(listGVK)
	if err == nil {
		if list, ok := obj.(client.ObjectList); ok {
			return list
		}
	}
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(listGVK)
	return list
}
