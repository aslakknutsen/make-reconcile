package makereconcile

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	toolscache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
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

	mu                sync.Mutex
	reconcilers       []subReconciler
	statusReconcilers []statusSubReconciler
	watchedGVKs       map[schema.GroupVersionKind]bool
	watchPredicates   map[schema.GroupVersionKind][]EventPredicate
	tracker           *dependencyTracker

	resyncPeriod  time.Duration
	managerID     string
	eventRecorder record.EventRecorder

	// lastOutputs tracks which output resource keys were produced by each
	// (reconciler, primary) pair on the last run. Used to detect deletions
	// when the set of outputs shrinks.
	lastOutputs map[string]map[types.NamespacedName]bool

	// pendingWatches holds GVKs whose CRDs were not available at startup.
	// A background goroutine retries registration periodically.
	pendingWatches             []schema.GroupVersionKind
	pendingWatchRetryInterval  time.Duration
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

// WithManagerID sets the manager identity used to label output resources.
// This scopes orphan GC so multiple independent managers in the same cluster
// don't interfere with each other. Default is "default".
func WithManagerID(id string) ManagerOption {
	return func(m *Manager) { m.managerID = id }
}

// WithPendingWatchRetryInterval sets how often the manager retries registering
// watches for GVKs whose CRDs were not available at startup. Default is 10s.
func WithPendingWatchRetryInterval(d time.Duration) ManagerOption {
	return func(m *Manager) { m.pendingWatchRetryInterval = d }
}

// WithEventRecorder sets the Kubernetes event recorder used to emit events
// on primary resources. When set, the framework automatically emits events
// for successful applies, stale output deletions, and reconciliation errors.
// Reconciler functions can also emit custom events via HandlerContext.RecordEvent.
func WithEventRecorder(recorder record.EventRecorder) ManagerOption {
	return func(m *Manager) { m.eventRecorder = recorder }
}

// WithCache sets the cache implementation. Intended for testing.
func WithCache(c cache.Cache) ManagerOption {
	return func(m *Manager) { m.cache = c }
}

// WithClient sets the client implementation. Intended for testing.
func WithClient(c client.Client) ManagerOption {
	return func(m *Manager) { m.client = c }
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
		client:          cl,
		cache:           c,
		scheme:          scheme,
		restMapper:      mapper,
		restConfig:      cfg,
		log:             slog.Default(),
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		resyncPeriod:    5 * time.Minute,
		managerID:       "default",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
	}
	for _, o := range opts {
		o(m)
	}
	return m, nil
}

// NewManagerForTest creates a Manager without a real cluster connection.
// Intended for unit tests and Go testable examples. The returned manager
// supports Watch and Reconcile registration. Use WithCache and WithClient
// to inject fakes for reconciler invocation, or use the mrtest.TestHarness.
func NewManagerForTest(scheme *runtime.Scheme, opts ...ManagerOption) *Manager {
	m := &Manager{
		scheme:          scheme,
		log:             slog.Default(),
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		resyncPeriod:    5 * time.Minute,
		managerID:       "default",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

// Start begins watching all registered GVKs, routing events to sub-reconcilers,
// and running periodic full resyncs. Blocks until ctx is cancelled.
//
// If a watched GVK's CRD is not yet installed on the cluster, Start does not
// fail. Instead it skips that watch and retries periodically in the background.
// When the CRD becomes available the handler is registered and a full reconcile
// runs so that all reconcilers see the newly available resources.
func (m *Manager) Start(ctx context.Context) error {
	// Register informer event handlers before starting the cache.
	// GVKs whose CRD is missing are collected for background retry.
	var pending []schema.GroupVersionKind
	for gvk := range m.watchedGVKs {
		registered, err := m.registerHandler(ctx, gvk)
		if err != nil {
			return fmt.Errorf("register handler for %v: %w", gvk, err)
		}
		if !registered {
			m.log.Warn("CRD not found, will retry periodically", "gvk", gvk)
			pending = append(pending, gvk)
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

	// Initial full reconcile + orphan GC.
	m.fullReconcile(ctx)
	m.gcOrphans(ctx)

	// Start background retry for GVKs whose CRDs were missing at startup.
	if len(pending) > 0 {
		m.mu.Lock()
		m.pendingWatches = pending
		m.mu.Unlock()
		go m.retryPendingWatches(ctx)
	}

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
			m.gcOrphans(ctx)
		}
	}
}

// PendingWatchCount returns the number of GVKs still awaiting CRD availability.
func (m *Manager) PendingWatchCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.pendingWatches)
}

// retryPendingWatches periodically attempts to register event handlers for
// GVKs whose CRDs were missing at startup. When a CRD becomes available the
// handler is registered, the cache is synced, and a full reconcile runs.
// Exits when all pending watches are resolved or the context is cancelled.
func (m *Manager) retryPendingWatches(ctx context.Context) {
	interval := m.pendingWatchRetryInterval
	if interval == 0 {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.mu.Lock()
			pending := make([]schema.GroupVersionKind, len(m.pendingWatches))
			copy(pending, m.pendingWatches)
			m.mu.Unlock()

			if len(pending) == 0 {
				return
			}

			var stillPending []schema.GroupVersionKind
			resolved := false
			for _, gvk := range pending {
				registered, err := m.registerHandler(ctx, gvk)
				if err != nil {
					m.log.Error("retry register handler failed", "gvk", gvk, "error", err)
					stillPending = append(stillPending, gvk)
					continue
				}
				if !registered {
					stillPending = append(stillPending, gvk)
					continue
				}
				m.log.Info("CRD now available, watch registered", "gvk", gvk)
				resolved = true
			}

			m.mu.Lock()
			m.pendingWatches = stillPending
			m.mu.Unlock()

			if resolved {
				if !m.cache.WaitForCacheSync(ctx) {
					m.log.Error("cache sync failed after registering pending watch")
					continue
				}
				m.fullReconcile(ctx)
			}
		}
	}
}

// registerHandler sets up an informer event handler for the given GVK.
// Returns (true, nil) on success, (false, nil) when the CRD is not yet
// installed on the cluster, or (false, err) for other failures.
func (m *Manager) registerHandler(ctx context.Context, gvk schema.GroupVersionKind) (bool, error) {
	obj, err := m.scheme.New(gvk)
	if err != nil {
		return false, fmt.Errorf("scheme.New(%v): %w", gvk, err)
	}
	cObj, ok := obj.(client.Object)
	if !ok {
		return false, fmt.Errorf("type for %v does not implement client.Object", gvk)
	}

	informer, err := m.cache.GetInformer(ctx, cObj)
	if err != nil {
		if meta.IsNoMatchError(err) {
			return false, nil
		}
		return false, fmt.Errorf("get informer for %v: %w", gvk, err)
	}

	handler := &eventHandler{mgr: m, gvk: gvk, predicates: m.watchPredicates[gvk]}
	informer.AddEventHandler(handler)
	return true, nil
}

// eventHandler routes informer events to the appropriate sub-reconcilers.
type eventHandler struct {
	mgr        *Manager
	gvk        schema.GroupVersionKind
	predicates []EventPredicate
}

func (h *eventHandler) OnAdd(obj interface{}, _ bool) {
	cObj, ok := obj.(client.Object)
	if !ok {
		return
	}
	for _, p := range h.predicates {
		if p.Create != nil && !p.Create(cObj) {
			return
		}
	}
	h.handle(cObj)
}

func (h *eventHandler) OnUpdate(oldObj, newObj interface{}) {
	newCObj, ok := newObj.(client.Object)
	if !ok {
		return
	}
	oldCObj, _ := oldObj.(client.Object)
	for _, p := range h.predicates {
		if p.Update != nil && !p.Update(oldCObj, newCObj) {
			return
		}
	}
	h.handle(newCObj)
}

func (h *eventHandler) OnDelete(obj interface{}) {
	if d, ok := obj.(toolscache.DeletedFinalStateUnknown); ok {
		obj = d.Obj
	}
	cObj, ok := obj.(client.Object)
	if !ok {
		return
	}
	for _, p := range h.predicates {
		if p.Delete != nil && !p.Delete(cObj) {
			return
		}
	}
	h.handle(cObj)
}

func (h *eventHandler) handle(obj client.Object) {
	nn := types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}
	ctx := context.Background()

	// Track which primaries were affected so we can run status reconcilers after.
	affectedPrimaries := make(map[schema.GroupVersionKind]map[types.NamespacedName]bool)
	recordAffected := func(gvk schema.GroupVersionKind, key types.NamespacedName) {
		if affectedPrimaries[gvk] == nil {
			affectedPrimaries[gvk] = make(map[types.NamespacedName]bool)
		}
		affectedPrimaries[gvk][key] = true
	}

	// Phase 1: run output reconcilers.

	// Is this a primary resource for any output reconciler?
	for _, r := range h.mgr.reconcilers {
		if r.PrimaryGVK() == h.gvk {
			h.mgr.runSubReconciler(ctx, r, nn)
			recordAffected(r.PrimaryGVK(), nn)
		}
	}

	// Is this a dependency of any output reconciler?
	refs := h.mgr.tracker.Lookup(h.gvk, nn)
	for _, ref := range refs {
		for _, r := range h.mgr.reconcilers {
			if r.ID() == ref.ReconcilerID {
				h.mgr.runSubReconciler(ctx, r, ref.PrimaryKey)
				recordAffected(r.PrimaryGVK(), ref.PrimaryKey)
				break
			}
		}
	}

	// Phase 2: run status reconcilers for affected primaries + direct deps.

	// Is this a primary resource for any status reconciler?
	for _, sr := range h.mgr.statusReconcilers {
		if sr.PrimaryGVK() == h.gvk {
			recordAffected(sr.PrimaryGVK(), nn)
		}
	}

	// Is this a dependency of any status reconciler?
	for _, ref := range refs {
		for _, sr := range h.mgr.statusReconcilers {
			if sr.ID() == ref.ReconcilerID {
				recordAffected(sr.PrimaryGVK(), ref.PrimaryKey)
				break
			}
		}
	}

	for _, sr := range h.mgr.statusReconcilers {
		keys := affectedPrimaries[sr.PrimaryGVK()]
		for key := range keys {
			h.mgr.runStatusReconciler(ctx, sr, key)
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
		m.recordEvent(m.getPrimary(ctx, r.PrimaryGVK(), primaryKey),
			"Warning", "ReconcileFailed",
			"Reconciler %s failed: %v", r.ID(), err)
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
		if err := applyDesired(ctx, m.client, obj, r.PrimaryGVK(), primaryKey, uid, m.managerID); err != nil {
			m.log.Error("apply failed",
				"reconciler", r.ID(),
				"resource", onn,
				"error", err,
			)
			m.recordEvent(primaryObj, "Warning", "ApplyFailed",
				"Failed to apply %s %s: %v", r.OutputGVK().Kind, onn, err)
		} else {
			m.recordEvent(primaryObj, "Normal", "Applied",
				"Applied %s %s", r.OutputGVK().Kind, onn)
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
				m.recordEvent(m.getPrimary(ctx, r.PrimaryGVK(), primaryKey),
					"Warning", "DeleteFailed",
					"Failed to delete stale %s %s: %v", r.OutputGVK().Kind, prev, err)
			} else {
				m.recordEvent(m.getPrimary(ctx, r.PrimaryGVK(), primaryKey),
					"Normal", "Deleted",
					"Deleted stale %s %s", r.OutputGVK().Kind, prev)
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

func (m *Manager) runStatusReconciler(ctx context.Context, sr statusSubReconciler, primaryKey types.NamespacedName) {
	if err := sr.ReconcileStatus(ctx, m, primaryKey); err != nil {
		m.log.Error("status reconciler failed",
			"reconciler", sr.ID(),
			"primary", primaryKey,
			"error", err,
		)
		m.recordEvent(m.getPrimary(ctx, sr.PrimaryGVK(), primaryKey),
			"Warning", "ReconcileFailed",
			"Status reconciler %s failed: %v", sr.ID(), err)
	}
}

// recordEvent emits a Kubernetes event on the given object if an event
// recorder has been configured. Safe to call with a nil object or nil recorder.
func (m *Manager) recordEvent(obj client.Object, eventType, reason, messageFmt string, args ...any) {
	if m.eventRecorder == nil || obj == nil {
		return
	}
	m.eventRecorder.Eventf(obj, eventType, reason, messageFmt, args...)
}

func (m *Manager) fullReconcile(ctx context.Context) {
	// Phase 1: all output reconcilers.
	for _, r := range m.reconcilers {
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

	// Phase 2: all status reconcilers (after outputs are applied).
	for _, sr := range m.statusReconcilers {
		list := newListForGVK(m.scheme, sr.PrimaryGVK())
		if err := m.cache.List(ctx, list); err != nil {
			m.log.Error("full reconcile status list failed",
				"reconciler", sr.ID(),
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
			m.runStatusReconciler(ctx, sr, nn)
		}
	}
}

// HandleForTest simulates an informer event for the given GVK and key.
// It runs the same two-phase logic as the real event handler: output
// reconcilers first, then status reconcilers for affected primaries.
// Intended for testing only.
func (m *Manager) HandleForTest(ctx context.Context, gvk schema.GroupVersionKind, key types.NamespacedName) {
	h := &eventHandler{mgr: m, gvk: gvk}
	obj, err := m.scheme.New(gvk)
	if err != nil {
		return
	}
	cObj, ok := obj.(client.Object)
	if !ok {
		return
	}
	cObj.SetName(key.Name)
	cObj.SetNamespace(key.Namespace)
	h.handle(cObj)
}

// FullReconcileForTest runs all output reconcilers then all status reconcilers
// for every primary resource in the cache. Intended for testing only.
func (m *Manager) FullReconcileForTest(ctx context.Context) {
	m.fullReconcile(ctx)
}

// gcOrphans deletes output resources that carry the managed-by label for this
// manager but are not claimed by any sub-reconciler in the current lastOutputs.
// This catches resources orphaned by a crash+restart when lastOutputs was lost.
func (m *Manager) gcOrphans(ctx context.Context) {
	m.mu.Lock()

	// Build wanted set per output GVK from current lastOutputs.
	wanted := make(map[schema.GroupVersionKind]map[types.NamespacedName]bool)
	for _, r := range m.reconcilers {
		gvk := r.OutputGVK()
		if wanted[gvk] == nil {
			wanted[gvk] = make(map[types.NamespacedName]bool)
		}
		prefix := r.ID() + "/"
		for outputKey, nns := range m.lastOutputs {
			if strings.HasPrefix(outputKey, prefix) {
				for nn := range nns {
					wanted[gvk][nn] = true
				}
			}
		}
	}

	m.mu.Unlock()

	for gvk, wantNNs := range wanted {
		list := &unstructured.UnstructuredList{}
		list.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   gvk.Group,
			Version: gvk.Version,
			Kind:    gvk.Kind + "List",
		})

		if err := m.client.List(ctx, list, client.MatchingLabels{managedByLabel: m.managerID}); err != nil {
			m.log.Error("gc orphans list failed", "gvk", gvk, "error", err)
			continue
		}

		for _, item := range list.Items {
			nn := types.NamespacedName{Name: item.GetName(), Namespace: item.GetNamespace()}
			if !wantNNs[nn] {
				m.log.Info("deleting orphan resource", "gvk", gvk, "resource", nn)
				if err := deleteIfExists(ctx, m.client, m.scheme, gvk, nn); err != nil {
					m.log.Error("gc orphan delete failed", "gvk", gvk, "resource", nn, "error", err)
				}
			}
		}
	}
}
