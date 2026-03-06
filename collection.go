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

// EventPredicate filters informer events before they reach the dependency
// tracker or any reconciler. Nil function pointers default to "pass".
type EventPredicate struct {
	Create func(obj client.Object) bool
	Update func(oldObj, newObj client.Object) bool
	Delete func(obj client.Object) bool
}

// WatchOption configures a Watch call.
type WatchOption func(*watchConfig)

type watchConfig struct {
	predicates []EventPredicate
}

// WithGenerationChanged returns a WatchOption that filters out events where
// the object's metadata.generation has not changed. This suppresses
// status-only updates and breaks the status write feedback loop.
func WithGenerationChanged() WatchOption {
	return WithEventPredicate(EventPredicate{
		Update: func(oldObj, newObj client.Object) bool {
			return oldObj.GetGeneration() != newObj.GetGeneration()
		},
	})
}

// WithEventPredicate adds a custom EventPredicate to a Watch call. When
// multiple predicates exist for the same GVK, all must pass (AND semantics).
func WithEventPredicate(p EventPredicate) WatchOption {
	return func(cfg *watchConfig) {
		cfg.predicates = append(cfg.predicates, p)
	}
}

// Watch registers a Kubernetes resource type with the manager and returns a
// Collection handle. The manager's cache will watch this GVK. The returned
// Collection is used with Fetch/FetchAll inside sub-reconciler functions.
//
// Optional WatchOptions (e.g. WithGenerationChanged) add informer-level
// predicates that filter events before they reach any reconciler for this GVK.
// Predicates are AND'd per-GVK across all Watch calls.
func Watch[T client.Object](mgr *Manager, opts ...WatchOption) *Collection[T] {
	obj := newTypedObject[T]()
	gvks, _, err := mgr.scheme.ObjectKinds(obj)
	if err != nil || len(gvks) == 0 {
		panic("makereconcile.Watch: type not registered in scheme: " + err.Error())
	}
	gvk := gvks[0]

	var cfg watchConfig
	for _, o := range opts {
		o(&cfg)
	}

	mgr.mu.Lock()
	mgr.watchedGVKs[gvk] = true
	if len(cfg.predicates) > 0 {
		mgr.watchPredicates[gvk] = append(mgr.watchPredicates[gvk], cfg.predicates...)
	}
	mgr.mu.Unlock()

	return &Collection[T]{mgr: mgr, gvk: gvk}
}
