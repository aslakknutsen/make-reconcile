package makereconcile

import (
	"sync"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

// depKey identifies a specific Kubernetes resource instance.
type depKey struct {
	GVK       schema.GroupVersionKind
	NamespacedName types.NamespacedName
}

// depKeyBroad identifies a broad dependency (e.g. "all ConfigMaps in namespace X").
type depKeyBroad struct {
	GVK       schema.GroupVersionKind
	Namespace string
}

// reconcilerRef identifies a specific sub-reconciler invocation for a specific primary resource.
type reconcilerRef struct {
	ReconcilerID string
	PrimaryKey   types.NamespacedName
}

type dependencyTracker struct {
	mu sync.RWMutex

	// narrow maps a specific resource to the set of sub-reconciler invocations that depend on it.
	narrow map[depKey]map[reconcilerRef]struct{}

	// broad maps a (GVK, namespace) pair to sub-reconciler invocations that did a list/filter
	// in that namespace. An empty namespace means cluster-scoped.
	broad map[depKeyBroad]map[reconcilerRef]struct{}

	// reverse maps a reconciler ref back to its dependencies, so we can clean them up on re-run.
	reverseNarrow map[reconcilerRef]map[depKey]struct{}
	reverseBroad  map[reconcilerRef]map[depKeyBroad]struct{}
}

func newDependencyTracker() *dependencyTracker {
	return &dependencyTracker{
		narrow:        make(map[depKey]map[reconcilerRef]struct{}),
		broad:         make(map[depKeyBroad]map[reconcilerRef]struct{}),
		reverseNarrow: make(map[reconcilerRef]map[depKey]struct{}),
		reverseBroad:  make(map[reconcilerRef]map[depKeyBroad]struct{}),
	}
}

// clearDeps removes all recorded dependencies for a given reconciler ref.
func (dt *dependencyTracker) clearDeps(ref reconcilerRef) {
	if keys, ok := dt.reverseNarrow[ref]; ok {
		for k := range keys {
			if refs, ok := dt.narrow[k]; ok {
				delete(refs, ref)
				if len(refs) == 0 {
					delete(dt.narrow, k)
				}
			}
		}
		delete(dt.reverseNarrow, ref)
	}
	if keys, ok := dt.reverseBroad[ref]; ok {
		for k := range keys {
			if refs, ok := dt.broad[k]; ok {
				delete(refs, ref)
				if len(refs) == 0 {
					delete(dt.broad, k)
				}
			}
		}
		delete(dt.reverseBroad, ref)
	}
}

// recordNarrow records that ref depends on a specific resource.
func (dt *dependencyTracker) recordNarrow(ref reconcilerRef, key depKey) {
	if dt.narrow[key] == nil {
		dt.narrow[key] = make(map[reconcilerRef]struct{})
	}
	dt.narrow[key][ref] = struct{}{}

	if dt.reverseNarrow[ref] == nil {
		dt.reverseNarrow[ref] = make(map[depKey]struct{})
	}
	dt.reverseNarrow[ref][key] = struct{}{}
}

// recordBroad records that ref depends on a broad set of resources.
func (dt *dependencyTracker) recordBroad(ref reconcilerRef, key depKeyBroad) {
	if dt.broad[key] == nil {
		dt.broad[key] = make(map[reconcilerRef]struct{})
	}
	dt.broad[key][ref] = struct{}{}

	if dt.reverseBroad[ref] == nil {
		dt.reverseBroad[ref] = make(map[depKeyBroad]struct{})
	}
	dt.reverseBroad[ref][key] = struct{}{}
}

// SetDeps atomically replaces all dependencies for a reconciler ref.
func (dt *dependencyTracker) SetDeps(ref reconcilerRef, narrowKeys []depKey, broadKeys []depKeyBroad) {
	dt.mu.Lock()
	defer dt.mu.Unlock()

	dt.clearDeps(ref)
	for _, k := range narrowKeys {
		dt.recordNarrow(ref, k)
	}
	for _, k := range broadKeys {
		dt.recordBroad(ref, k)
	}
}

// AppendNarrow adds narrow dependencies to an existing set for a reconciler ref
// without clearing what's already there. Used to register output resources as
// tracked dependencies after apply.
func (dt *dependencyTracker) AppendNarrow(ref reconcilerRef, keys []depKey) {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	for _, k := range keys {
		dt.recordNarrow(ref, k)
	}
}

// RemoveDeps removes all dependencies for a reconciler ref.
func (dt *dependencyTracker) RemoveDeps(ref reconcilerRef) {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	dt.clearDeps(ref)
}

// Lookup returns all reconciler refs affected by a change to the given resource.
func (dt *dependencyTracker) Lookup(gvk schema.GroupVersionKind, nn types.NamespacedName) []reconcilerRef {
	dt.mu.RLock()
	defer dt.mu.RUnlock()

	seen := make(map[reconcilerRef]struct{})
	var result []reconcilerRef

	// Check narrow matches.
	key := depKey{GVK: gvk, NamespacedName: nn}
	for ref := range dt.narrow[key] {
		if _, ok := seen[ref]; !ok {
			seen[ref] = struct{}{}
			result = append(result, ref)
		}
	}

	// Check broad matches (same GVK + namespace).
	broadKey := depKeyBroad{GVK: gvk, Namespace: nn.Namespace}
	for ref := range dt.broad[broadKey] {
		if _, ok := seen[ref]; !ok {
			seen[ref] = struct{}{}
			result = append(result, ref)
		}
	}

	return result
}
