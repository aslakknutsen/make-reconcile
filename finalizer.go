package makereconcile

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// cleanupEntry wraps a type-erased cleanup function registered via OnDelete.
type cleanupEntry struct {
	fn func(*HandlerContext, client.Object) error
}

// OnDelete registers a cleanup callback that runs when a primary resource is
// being deleted (DeletionTimestamp set, finalizer pending). The framework
// automatically manages a finalizer on the primary: it adds the finalizer on
// first reconciliation and removes it after the cleanup callback succeeds.
//
// The callback receives a HandlerContext so it can Fetch resources needed for
// cleanup. Return nil to indicate cleanup succeeded (finalizer will be removed).
// Return an error to retry on the next event (finalizer stays in place).
//
// One registration per primary GVK. Calling OnDelete again for the same primary
// type replaces the previous callback.
//
// Usage:
//
//	platforms := mr.Watch[*Platform](mgr, mr.WithGenerationChanged())
//
//	mr.OnDelete(mgr, platforms, func(hc *mr.HandlerContext, p *Platform) error {
//	    vs := mr.Fetch(hc, virtualServices, mr.FilterName(p.Name+"-vs", p.Namespace))
//	    if vs != nil {
//	        // revert injected route
//	    }
//	    return nil
//	})
func OnDelete[P client.Object](mgr *Manager, primary *Collection[P], fn func(*HandlerContext, P) error) {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if mgr.cleanupFns == nil {
		mgr.cleanupFns = make(map[schema.GroupVersionKind]cleanupEntry)
	}
	mgr.cleanupFns[primary.gvk] = cleanupEntry{
		fn: func(hc *HandlerContext, obj client.Object) error {
			return fn(hc, obj.(P))
		},
	}
}
