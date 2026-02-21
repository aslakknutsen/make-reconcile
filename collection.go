package makereconcile

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Collection is a typed handle to a watched set of Kubernetes resources.
// It is the primary building block: you Watch a type to get a Collection,
// then pass it to Fetch inside sub-reconcilers to read state.
type Collection[T client.Object] struct {
	mgr *Manager
	gvk schema.GroupVersionKind
}

// GVK returns the GroupVersionKind this collection watches.
func (c *Collection[T]) GVK() schema.GroupVersionKind {
	return c.gvk
}

// Watch registers a Kubernetes resource type with the manager and returns a
// Collection handle. The manager's cache will watch this GVK. The returned
// Collection is used with Fetch/FetchAll inside sub-reconciler functions.
func Watch[T client.Object](mgr *Manager) *Collection[T] {
	obj := newTypedObject[T]()
	gvks, _, err := mgr.scheme.ObjectKinds(obj)
	if err != nil || len(gvks) == 0 {
		panic("makereconcile.Watch: type not registered in scheme: " + err.Error())
	}
	gvk := gvks[0]

	mgr.mu.Lock()
	mgr.watchedGVKs[gvk] = true
	mgr.mu.Unlock()

	return &Collection[T]{mgr: mgr, gvk: gvk}
}
