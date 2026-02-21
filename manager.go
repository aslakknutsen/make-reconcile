package makereconcile

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	toolscache "k8s.io/client-go/tools/cache"
)

// Manager is the central coordinator. It holds the kubernetes client, cache,
// registered sub-reconcilers, and the dependency tracker. Call NewManager to
// create one, register sub-reconcilers via Reconcile/ReconcileMany, then Start.
type Manager struct {
	client      client.Client
	cache       cache.Cache
	scheme      *runtime.Scheme
	restMapper  meta.RESTMapper
	restConfig  *rest.Config
	log         *slog.Logger

	mu          sync.Mutex
	reconcilers []subReconciler
	watchedGVKs map[schema.GroupVersionKind]bool
	tracker     *dependencyTracker

	resyncPeriod time.Duration

	// lastOutputs tracks which output resource keys were produced by each
	// (reconciler, primary) pair on the last run. Used to detect deletions
	// when the set of outputs shrinks.
	lastOutputs map[string]map[types.NamespacedName]bool
}

// ManagerOption configures a Manager.
type ManagerOption func(*Manager)

// WithResyncPeriod sets the periodic full resync interval. Default is 5 minutes.
func WithResyncPeriod(d time.Duration) ManagerOption {
	return func(m *Manager) { m.resyncPeriod = d }
}

// WithLogger sets the logger. Default is slog.Default().
func WithLogger(l *slog.Logger) ManagerOption {
	return func(m *Manager) { m.log = l }
}

// NewManager creates a new Manager from a rest.Config and scheme.
func NewManager(cfg *rest.Config, scheme *runtime.Scheme, opts ...ManagerOption) (*Manager, error) {
	httpClient, err := rest.HTTPClientFor(cfg)
	if err != nil {
		return nil, fmt.Errorf("create http client: %w", err)
	}
	mapper, err := apiutil.NewDynamicRESTMapper(cfg, httpClient)
	if err != nil {
		return nil, fmt.Errorf("create rest mapper: %w", err)
	}

	c, err := cache.New(cfg, cache.Options{Scheme: scheme, Mapper: mapper})
	if err != nil {
		return nil, fmt.Errorf("create cache: %w", err)
	}

	cl, err := client.New(cfg, client.Options{Scheme: scheme, Mapper: mapper})
	if err != nil {
		return nil, fmt.Errorf("create client: %w", err)
	}

	m := &Manager{
		client:       cl,
		cache:        c,
		scheme:       scheme,
		restMapper:   mapper,
		restConfig:   cfg,
		log:          slog.Default(),
		watchedGVKs:  make(map[schema.GroupVersionKind]bool),
		tracker:      newDependencyTracker(),
		resyncPeriod: 5 * time.Minute,
		lastOutputs:  make(map[string]map[types.NamespacedName]bool),
	}
	for _, o := range opts {
		o(m)
	}
	return m, nil
}

// NewManagerForTest creates a Manager without a real cluster connection.
// Intended for unit tests and Go testable examples only. The returned manager
// supports Watch and Reconcile registration but cannot Start.
func NewManagerForTest(scheme *runtime.Scheme) *Manager {
	return &Manager{
		scheme:       scheme,
		log:          slog.Default(),
		watchedGVKs:  make(map[schema.GroupVersionKind]bool),
		tracker:      newDependencyTracker(),
		resyncPeriod: 5 * time.Minute,
		lastOutputs:  make(map[string]map[types.NamespacedName]bool),
	}
}

// Start begins watching all registered GVKs, routing events to sub-reconcilers,
// and running periodic full resyncs. Blocks until ctx is cancelled.
func (m *Manager) Start(ctx context.Context) error {
	// Register informer event handlers before starting the cache.
	for gvk := range m.watchedGVKs {
		if err := m.registerHandler(ctx, gvk); err != nil {
			return fmt.Errorf("register handler for %v: %w", gvk, err)
		}
	}

	// Start the cache (blocks until informers are synced).
	go func() {
		if err := m.cache.Start(ctx); err != nil {
			m.log.Error("cache stopped with error", "error", err)
		}
	}()
	if !m.cache.WaitForCacheSync(ctx) {
		return fmt.Errorf("cache sync failed")
	}
	m.log.Info("cache synced, running initial full reconcile")

	// Initial full reconcile.
	m.fullReconcile(ctx)

	// Periodic full resync.
	ticker := time.NewTicker(m.resyncPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			m.log.Info("periodic full reconcile")
			m.fullReconcile(ctx)
		}
	}
}

func (m *Manager) registerHandler(ctx context.Context, gvk schema.GroupVersionKind) error {
	obj, err := m.scheme.New(gvk)
	if err != nil {
		return fmt.Errorf("scheme.New(%v): %w", gvk, err)
	}
	cObj, ok := obj.(client.Object)
	if !ok {
		return fmt.Errorf("type for %v does not implement client.Object", gvk)
	}

	informer, err := m.cache.GetInformer(ctx, cObj)
	if err != nil {
		return fmt.Errorf("get informer for %v: %w", gvk, err)
	}

	handler := &eventHandler{mgr: m, gvk: gvk}
	informer.AddEventHandler(handler)
	return nil
}

// eventHandler routes informer events to the appropriate sub-reconcilers.
type eventHandler struct {
	mgr *Manager
	gvk schema.GroupVersionKind
}

func (h *eventHandler) OnAdd(obj interface{}, _ bool) {
	h.handle(obj)
}

func (h *eventHandler) OnUpdate(_, newObj interface{}) {
	h.handle(newObj)
}

func (h *eventHandler) OnDelete(obj interface{}) {
	if d, ok := obj.(toolscache.DeletedFinalStateUnknown); ok {
		obj = d.Obj
	}
	h.handle(obj)
}

func (h *eventHandler) handle(obj interface{}) {
	cObj, ok := obj.(client.Object)
	if !ok {
		return
	}
	nn := types.NamespacedName{Name: cObj.GetName(), Namespace: cObj.GetNamespace()}

	// Is this a primary resource for any reconciler? If so, run all its sub-reconcilers.
	for _, r := range h.mgr.reconcilers {
		if r.PrimaryGVK() == h.gvk {
			h.mgr.runSubReconciler(context.Background(), r, nn)
		}
	}

	// Is this a dependency of any sub-reconciler? If so, re-run those.
	refs := h.mgr.tracker.Lookup(h.gvk, nn)
	for _, ref := range refs {
		for _, r := range h.mgr.reconcilers {
			if r.ID() == ref.ReconcilerID {
				h.mgr.runSubReconciler(context.Background(), r, ref.PrimaryKey)
				break
			}
		}
	}
}

func (m *Manager) runSubReconciler(ctx context.Context, r subReconciler, primaryKey types.NamespacedName) {
	outputKey := fmt.Sprintf("%s/%s", r.ID(), primaryKey.String())

	desired, err := r.Reconcile(ctx, m, primaryKey)
	if err != nil {
		m.log.Error("sub-reconciler failed",
			"reconciler", r.ID(),
			"primary", primaryKey,
			"error", err,
		)
		return
	}

	// Determine which output keys we want now.
	wantKeys := make(map[types.NamespacedName]bool)
	for _, obj := range desired {
		if obj == nil {
			continue
		}
		onn := types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}
		wantKeys[onn] = true

		// Retrieve the primary to set owner reference.
		primaryObj := m.getPrimary(ctx, r.PrimaryGVK(), primaryKey)

		var uid types.UID
		if primaryObj != nil {
			uid = primaryObj.GetUID()
		}
		if err := applyDesired(ctx, m.client, obj, r.PrimaryGVK(), primaryKey, uid); err != nil {
			m.log.Error("apply failed",
				"reconciler", r.ID(),
				"resource", onn,
				"error", err,
			)
		}
	}

	// Register outputs as tracked dependencies so external modifications
	// trigger the sub-reconciler to re-run and restore desired state.
	var outputDeps []depKey
	for onn := range wantKeys {
		outputDeps = append(outputDeps, depKey{GVK: r.OutputGVK(), NamespacedName: onn})
	}
	m.tracker.AppendNarrow(
		reconcilerRef{ReconcilerID: r.ID(), PrimaryKey: primaryKey},
		outputDeps,
	)

	// Delete outputs from previous run that are no longer desired.
	m.mu.Lock()
	prevKeys := m.lastOutputs[outputKey]
	m.lastOutputs[outputKey] = wantKeys
	m.mu.Unlock()

	for prev := range prevKeys {
		if !wantKeys[prev] {
			if err := deleteIfExists(ctx, m.client, m.scheme, r.OutputGVK(), prev); err != nil {
				m.log.Error("delete stale output failed",
					"reconciler", r.ID(),
					"resource", prev,
					"error", err,
				)
			}
		}
	}
}

func (m *Manager) getPrimary(ctx context.Context, gvk schema.GroupVersionKind, nn types.NamespacedName) client.Object {
	obj, err := m.scheme.New(gvk)
	if err != nil {
		return nil
	}
	cObj, ok := obj.(client.Object)
	if !ok {
		return nil
	}
	if err := m.cache.Get(ctx, nn, cObj); err != nil {
		return nil
	}
	return cObj
}

func (m *Manager) fullReconcile(ctx context.Context) {
	for _, r := range m.reconcilers {
		// List all instances of the primary resource.
		list := newListForGVK(m.scheme, r.PrimaryGVK())
		if err := m.cache.List(ctx, list); err != nil {
			m.log.Error("full reconcile list failed",
				"reconciler", r.ID(),
				"error", err,
			)
			continue
		}
		items, err := meta.ExtractList(list)
		if err != nil {
			continue
		}
		for _, item := range items {
			cObj, ok := item.(client.Object)
			if !ok {
				continue
			}
			nn := types.NamespacedName{Name: cObj.GetName(), Namespace: cObj.GetNamespace()}
			m.runSubReconciler(ctx, r, nn)
		}
	}
}
