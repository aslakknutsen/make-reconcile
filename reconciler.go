package makereconcile

import (
	"context"
	"fmt"
	"reflect"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// subReconciler is the internal interface for a registered sub-reconciler.
// Type erasure lets us store heterogeneous sub-reconcilers in the manager.
type subReconciler interface {
	ID() string
	PrimaryGVK() schema.GroupVersionKind
	OutputGVK() schema.GroupVersionKind
	// Reconcile runs the sub-reconciler for a specific primary resource instance.
	// Returns desired output objects. Empty/nil means "nothing should exist."
	Reconcile(ctx context.Context, mgr *Manager, primaryKey types.NamespacedName) ([]client.Object, error)
}

// ReconcileOption configures a Reconcile, ReconcileMany, or ReconcileStatus call.
type ReconcileOption[P client.Object] func(*reconcileConfig[P])

type reconcileConfig[P client.Object] struct {
	predicate func(P) bool
}

// WithPredicate adds a filter that runs after fetching the primary but before
// calling the reconciler function. If the predicate returns false, the
// reconciler is skipped for that primary instance (equivalent to returning nil).
// Stale dependencies and outputs are cleaned up automatically.
func WithPredicate[P client.Object](fn func(P) bool) ReconcileOption[P] {
	return func(cfg *reconcileConfig[P]) {
		cfg.predicate = fn
	}
}

// typedSubReconciler implements subReconciler for a one-to-one mapping.
type typedSubReconciler[P client.Object, T client.Object] struct {
	id         string
	primaryGVK schema.GroupVersionKind
	outputGVK  schema.GroupVersionKind
	fn         func(*HandlerContext, P) T
	predicate  func(P) bool
}

func (r *typedSubReconciler[P, T]) ID() string                          { return r.id }
func (r *typedSubReconciler[P, T]) PrimaryGVK() schema.GroupVersionKind { return r.primaryGVK }
func (r *typedSubReconciler[P, T]) OutputGVK() schema.GroupVersionKind  { return r.outputGVK }

func (r *typedSubReconciler[P, T]) Reconcile(ctx context.Context, mgr *Manager, primaryKey types.NamespacedName) ([]client.Object, error) {
	primary := newTypedObject[P]()
	if err := mgr.cache.Get(ctx, primaryKey, primary); err != nil {
		return nil, fmt.Errorf("get primary %v: %w", primaryKey, err)
	}

	if r.predicate != nil && !r.predicate(primary) {
		ref := reconcilerRef{ReconcilerID: r.id, PrimaryKey: primaryKey}
		mgr.tracker.SetDeps(ref, nil, nil)
		return nil, nil
	}

	hc := newHandlerContext(ctx, mgr, primary)
	result := r.fn(hc, primary)

	ref := reconcilerRef{ReconcilerID: r.id, PrimaryKey: primaryKey}
	mgr.tracker.SetDeps(ref, hc.narrowDeps, hc.broadDeps)

	if isNilObject(result) {
		return nil, nil
	}
	return []client.Object{result}, nil
}

// typedManySubReconciler implements subReconciler for a one-to-many mapping.
type typedManySubReconciler[P client.Object, T client.Object] struct {
	id         string
	primaryGVK schema.GroupVersionKind
	outputGVK  schema.GroupVersionKind
	fn         func(*HandlerContext, P) []T
	predicate  func(P) bool
}

func (r *typedManySubReconciler[P, T]) ID() string                          { return r.id }
func (r *typedManySubReconciler[P, T]) PrimaryGVK() schema.GroupVersionKind { return r.primaryGVK }
func (r *typedManySubReconciler[P, T]) OutputGVK() schema.GroupVersionKind  { return r.outputGVK }

func (r *typedManySubReconciler[P, T]) Reconcile(ctx context.Context, mgr *Manager, primaryKey types.NamespacedName) ([]client.Object, error) {
	primary := newTypedObject[P]()
	if err := mgr.cache.Get(ctx, primaryKey, primary); err != nil {
		return nil, fmt.Errorf("get primary %v: %w", primaryKey, err)
	}

	if r.predicate != nil && !r.predicate(primary) {
		ref := reconcilerRef{ReconcilerID: r.id, PrimaryKey: primaryKey}
		mgr.tracker.SetDeps(ref, nil, nil)
		return nil, nil
	}

	hc := newHandlerContext(ctx, mgr, primary)
	results := r.fn(hc, primary)

	ref := reconcilerRef{ReconcilerID: r.id, PrimaryKey: primaryKey}
	mgr.tracker.SetDeps(ref, hc.narrowDeps, hc.broadDeps)

	if results == nil {
		return nil, nil
	}
	out := make([]client.Object, 0, len(results))
	for _, item := range results {
		if !isNilObject(item) {
			out = append(out, item)
		}
	}
	return out, nil
}

// Reconcile registers a sub-reconciler that produces a single output resource
// for each primary resource instance. Return nil to indicate the output should
// not exist (the framework will delete it if present).
//
// Usage:
//
//	makereconcile.Reconcile(mgr, apps, func(ctx *makereconcile.HandlerContext, app *MyApp) *appsv1.Deployment {
//	    return &appsv1.Deployment{...}
//	})
func Reconcile[P client.Object, T client.Object](mgr *Manager, primary *Collection[P], fn func(*HandlerContext, P) T, opts ...ReconcileOption[P]) {
	var cfg reconcileConfig[P]
	for _, o := range opts {
		o(&cfg)
	}

	outputGVK := gvkFor[T](mgr)
	id := fmt.Sprintf("%s->%s", primary.gvk.Kind, outputGVK.Kind)

	mgr.mu.Lock()
	count := 0
	for _, r := range mgr.reconcilers {
		if r.PrimaryGVK() == primary.gvk && r.OutputGVK() == outputGVK {
			count++
		}
	}
	if count > 0 {
		id = fmt.Sprintf("%s#%d", id, count)
	}

	r := &typedSubReconciler[P, T]{
		id:         id,
		primaryGVK: primary.gvk,
		outputGVK:  outputGVK,
		fn:         fn,
		predicate:  cfg.predicate,
	}
	mgr.reconcilers = append(mgr.reconcilers, r)
	mgr.watchedGVKs[outputGVK] = true
	mgr.mu.Unlock()
}

// ReconcileMany registers a sub-reconciler that produces multiple output resources
// for each primary resource instance. The framework manages the full set: resources
// returned are applied; previously-returned resources no longer in the set are deleted.
func ReconcileMany[P client.Object, T client.Object](mgr *Manager, primary *Collection[P], fn func(*HandlerContext, P) []T, opts ...ReconcileOption[P]) {
	var cfg reconcileConfig[P]
	for _, o := range opts {
		o(&cfg)
	}

	outputGVK := gvkFor[T](mgr)
	id := fmt.Sprintf("%s->[]%s", primary.gvk.Kind, outputGVK.Kind)

	mgr.mu.Lock()
	count := 0
	for _, r := range mgr.reconcilers {
		if r.PrimaryGVK() == primary.gvk && r.OutputGVK() == outputGVK {
			count++
		}
	}
	if count > 0 {
		id = fmt.Sprintf("%s#%d", id, count)
	}

	r := &typedManySubReconciler[P, T]{
		id:         id,
		primaryGVK: primary.gvk,
		outputGVK:  outputGVK,
		fn:         fn,
		predicate:  cfg.predicate,
	}
	mgr.reconcilers = append(mgr.reconcilers, r)
	mgr.watchedGVKs[outputGVK] = true
	mgr.mu.Unlock()
}

// statusSubReconciler is the internal interface for status reconcilers.
// These run after output reconcilers and write to the primary's status subresource.
type statusSubReconciler interface {
	ID() string
	PrimaryGVK() schema.GroupVersionKind
	ReconcileStatus(ctx context.Context, mgr *Manager, primaryKey types.NamespacedName) error
}

type typedStatusSubReconciler[P client.Object, S any] struct {
	id         string
	primaryGVK schema.GroupVersionKind
	fn         func(*HandlerContext, P) *S
	predicate  func(P) bool
}

func (r *typedStatusSubReconciler[P, S]) ID() string                          { return r.id }
func (r *typedStatusSubReconciler[P, S]) PrimaryGVK() schema.GroupVersionKind { return r.primaryGVK }

func (r *typedStatusSubReconciler[P, S]) ReconcileStatus(ctx context.Context, mgr *Manager, primaryKey types.NamespacedName) error {
	primary := newTypedObject[P]()
	if err := mgr.cache.Get(ctx, primaryKey, primary); err != nil {
		return fmt.Errorf("get primary %v: %w", primaryKey, err)
	}

	if r.predicate != nil && !r.predicate(primary) {
		ref := reconcilerRef{ReconcilerID: r.id, PrimaryKey: primaryKey}
		mgr.tracker.SetDeps(ref, nil, nil)
		return nil
	}

	hc := newHandlerContext(ctx, mgr, primary)
	status := r.fn(hc, primary)

	ref := reconcilerRef{ReconcilerID: r.id, PrimaryKey: primaryKey}
	mgr.tracker.SetDeps(ref, hc.narrowDeps, hc.broadDeps)

	if status == nil {
		return nil
	}
	return applyStatus(ctx, mgr.client, r.primaryGVK, primaryKey, status)
}

// ReconcileStatus registers a status reconciler that computes a status struct
// for the primary resource. The framework applies it to the primary's status
// subresource via server-side apply. Return nil to skip the status write.
//
// Status reconcilers run after all output reconcilers for the same primary,
// so Fetch calls on output resources will reflect the latest applied state
// (subject to informer cache lag).
//
// Usage:
//
//	makereconcile.ReconcileStatus(mgr, apps, func(hc *makereconcile.HandlerContext, app *MyApp) *MyAppStatus {
//	    deploy := makereconcile.Fetch(hc, deployments, makereconcile.FilterName(app.Name+"-deploy", app.Namespace))
//	    if deploy != nil && deploy.Status.ReadyReplicas > 0 {
//	        return &MyAppStatus{Ready: true}
//	    }
//	    return &MyAppStatus{Ready: false}
//	})
func ReconcileStatus[P client.Object, S any](mgr *Manager, primary *Collection[P], fn func(*HandlerContext, P) *S, opts ...ReconcileOption[P]) {
	var cfg reconcileConfig[P]
	for _, o := range opts {
		o(&cfg)
	}

	id := fmt.Sprintf("%s.status", primary.gvk.Kind)

	mgr.mu.Lock()
	count := 0
	for _, r := range mgr.statusReconcilers {
		if r.PrimaryGVK() == primary.gvk {
			count++
		}
	}
	if count > 0 {
		id = fmt.Sprintf("%s#%d", id, count)
	}

	r := &typedStatusSubReconciler[P, S]{
		id:         id,
		primaryGVK: primary.gvk,
		fn:         fn,
		predicate:  cfg.predicate,
	}
	mgr.statusReconcilers = append(mgr.statusReconcilers, r)
	mgr.mu.Unlock()
}

func gvkFor[T client.Object](mgr *Manager) schema.GroupVersionKind {
	var zero T
	t := reflect.TypeOf(zero).Elem()
	obj := reflect.New(t).Interface().(client.Object)
	gvks, _, err := mgr.scheme.ObjectKinds(obj)
	if err != nil || len(gvks) == 0 {
		panic(fmt.Sprintf("makereconcile: type %T not registered in scheme", zero))
	}
	return gvks[0]
}

// isNilObject checks if a client.Object is nil, handling the typed-nil-in-interface case.
func isNilObject(obj client.Object) bool {
	if obj == nil {
		return true
	}
	v := reflect.ValueOf(obj)
	return v.Kind() == reflect.Ptr && v.IsNil()
}
