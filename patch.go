package makereconcile

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// patchSubReconciler is the internal interface for a registered foreign-patch
// sub-reconciler. Separate from subReconciler because the semantics differ:
// no OwnerRef, no managed-by label, partial contribution instead of full
// desired state.
type patchSubReconciler interface {
	ID() string
	PrimaryGVK() schema.GroupVersionKind
	TargetGVK() schema.GroupVersionKind
	Reconcile(ctx context.Context, mgr *Manager, primaryKey types.NamespacedName) ([]patchOutput, error)
	Strategy() patchStrategy
}

// patchOutput represents a single contribution to a foreign resource.
type patchOutput struct {
	TargetKey    types.NamespacedName
	Contribution client.Object
}

// PatchOption configures a ReconcilePatch or ReconcilePatchMany call.
type PatchOption func(*patchConfig)

type patchConfig struct {
	strategy patchStrategy
}

// WithSSAPatch selects the SSA field manager strategy. Use when the target CRD
// has x-kubernetes-list-type: map on all contributed list fields. Each
// contribution gets a unique field manager so multiple controllers can
// contribute to the same target without conflict.
func WithSSAPatch() PatchOption {
	return func(cfg *patchConfig) {
		cfg.strategy = &ssaPatchStrategy{}
	}
}

type typedPatchSubReconciler[P client.Object, T client.Object] struct {
	id         string
	primaryGVK schema.GroupVersionKind
	targetGVK  schema.GroupVersionKind
	fn         func(*HandlerContext, P) T
	strategy   patchStrategy
}

func (r *typedPatchSubReconciler[P, T]) ID() string                          { return r.id }
func (r *typedPatchSubReconciler[P, T]) PrimaryGVK() schema.GroupVersionKind { return r.primaryGVK }
func (r *typedPatchSubReconciler[P, T]) TargetGVK() schema.GroupVersionKind  { return r.targetGVK }
func (r *typedPatchSubReconciler[P, T]) Strategy() patchStrategy             { return r.strategy }

func (r *typedPatchSubReconciler[P, T]) Reconcile(ctx context.Context, mgr *Manager, primaryKey types.NamespacedName) ([]patchOutput, error) {
	primary := newTypedObject[P]()
	if err := mgr.cache.Get(ctx, primaryKey, primary); err != nil {
		return nil, fmt.Errorf("get primary %v: %w", primaryKey, err)
	}

	hc := newHandlerContext(ctx, mgr, primary)
	result := r.fn(hc, primary)

	ref := reconcilerRef{ReconcilerID: r.id, PrimaryKey: primaryKey}
	mgr.tracker.SetDeps(ref, hc.narrowDeps, hc.broadDeps)

	if isNilObject(result) {
		return nil, nil
	}
	nn := types.NamespacedName{Name: result.GetName(), Namespace: result.GetNamespace()}
	return []patchOutput{{TargetKey: nn, Contribution: result}}, nil
}

type typedPatchManySubReconciler[P client.Object, T client.Object] struct {
	id         string
	primaryGVK schema.GroupVersionKind
	targetGVK  schema.GroupVersionKind
	fn         func(*HandlerContext, P) []T
	strategy   patchStrategy
}

func (r *typedPatchManySubReconciler[P, T]) ID() string                          { return r.id }
func (r *typedPatchManySubReconciler[P, T]) PrimaryGVK() schema.GroupVersionKind { return r.primaryGVK }
func (r *typedPatchManySubReconciler[P, T]) TargetGVK() schema.GroupVersionKind  { return r.targetGVK }
func (r *typedPatchManySubReconciler[P, T]) Strategy() patchStrategy             { return r.strategy }

func (r *typedPatchManySubReconciler[P, T]) Reconcile(ctx context.Context, mgr *Manager, primaryKey types.NamespacedName) ([]patchOutput, error) {
	primary := newTypedObject[P]()
	if err := mgr.cache.Get(ctx, primaryKey, primary); err != nil {
		return nil, fmt.Errorf("get primary %v: %w", primaryKey, err)
	}

	hc := newHandlerContext(ctx, mgr, primary)
	results := r.fn(hc, primary)

	ref := reconcilerRef{ReconcilerID: r.id, PrimaryKey: primaryKey}
	mgr.tracker.SetDeps(ref, hc.narrowDeps, hc.broadDeps)

	if results == nil {
		return nil, nil
	}
	var out []patchOutput
	seen := make(map[types.NamespacedName]bool, len(results))
	for _, item := range results {
		if !isNilObject(item) {
			nn := types.NamespacedName{Name: item.GetName(), Namespace: item.GetNamespace()}
			if seen[nn] {
				return nil, fmt.Errorf("duplicate target key %v returned from patch reconciler %s", nn, r.id)
			}
			seen[nn] = true
			out = append(out, patchOutput{TargetKey: nn, Contribution: item})
		}
	}
	return out, nil
}

// ReconcilePatch registers a sub-reconciler that contributes fields to an
// existing foreign resource using scoped SSA. The function returns a partial
// object representing the contribution; its metadata (Name, Namespace)
// identifies the target. Return nil to remove the contribution.
//
// The target Collection must already be watched (via Watch). No OwnerReference
// or managed-by label is set on the target — only the contributed fields are
// managed via a reconciler-specific SSA field manager.
//
// On primary deletion, the framework automatically reverts contributions
// (releases field ownership) before removing the finalizer.
func ReconcilePatch[P client.Object, T client.Object](
	mgr *Manager,
	primary *Collection[P],
	target *Collection[T],
	fn func(*HandlerContext, P) T,
	opts ...PatchOption,
) {
	var cfg patchConfig
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.strategy == nil {
		cfg.strategy = &ssaPatchStrategy{}
	}

	id := fmt.Sprintf("%s~>%s", primary.gvk.Kind, target.gvk.Kind)

	mgr.mu.Lock()
	count := 0
	for _, r := range mgr.patchReconcilers {
		if r.PrimaryGVK() == primary.gvk && r.TargetGVK() == target.gvk {
			count++
		}
	}
	if count > 0 {
		id = fmt.Sprintf("%s#%d", id, count)
	}

	r := &typedPatchSubReconciler[P, T]{
		id:         id,
		primaryGVK: primary.gvk,
		targetGVK:  target.gvk,
		fn:         fn,
		strategy:   cfg.strategy,
	}
	mgr.patchReconcilers = append(mgr.patchReconcilers, r)
	mgr.mu.Unlock()
}

// ReconcilePatchMany registers a sub-reconciler that contributes fields to
// multiple foreign resources. Same semantics as ReconcilePatch but for the
// one-to-many case.
func ReconcilePatchMany[P client.Object, T client.Object](
	mgr *Manager,
	primary *Collection[P],
	target *Collection[T],
	fn func(*HandlerContext, P) []T,
	opts ...PatchOption,
) {
	var cfg patchConfig
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.strategy == nil {
		cfg.strategy = &ssaPatchStrategy{}
	}

	id := fmt.Sprintf("%s~>[]%s", primary.gvk.Kind, target.gvk.Kind)

	mgr.mu.Lock()
	count := 0
	for _, r := range mgr.patchReconcilers {
		if r.PrimaryGVK() == primary.gvk && r.TargetGVK() == target.gvk {
			count++
		}
	}
	if count > 0 {
		id = fmt.Sprintf("%s#%d", id, count)
	}

	r := &typedPatchManySubReconciler[P, T]{
		id:         id,
		primaryGVK: primary.gvk,
		targetGVK:  target.gvk,
		fn:         fn,
		strategy:   cfg.strategy,
	}
	mgr.patchReconcilers = append(mgr.patchReconcilers, r)
	mgr.mu.Unlock()
}
