package makereconcile

import (
	"context"
	"fmt"
	"log/slog"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

func TestOnDeleteRegistration(t *testing.T) {
	s := coreScheme()
	mgr := &Manager{
		scheme:          s,
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		managerID:       "default",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)
	OnDelete(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) error {
		return nil
	})

	cmGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	if !mgr.hasCleanup(cmGVK) {
		t.Error("expected cleanup registered for ConfigMap GVK")
	}
}

func TestFinalizerAddedOnFirstReconcile(t *testing.T) {
	s := coreScheme()
	fc := &fakeClient{}
	store := newTestStore(s, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "default", UID: "uid-1"},
	})
	mgr := &Manager{
		scheme:          s,
		cache:           store,
		client:          fc,
		log:             slog.Default(),
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		managerID:       "test",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)

	OnDelete(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) error {
		return nil
	})

	Reconcile(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-deploy", Namespace: cm.Namespace},
		}
	})

	primaryKey := types.NamespacedName{Name: "my-cm", Namespace: "default"}
	mgr.runSubReconciler(context.Background(), mgr.reconcilers[0], primaryKey)

	// The store should now have the ConfigMap with the finalizer set because
	// addFinalizer patches the object via the client (which is fc here).
	// fakeClient.Patch is a no-op, but the in-memory object gets the finalizer
	// added before the Patch call. Let's verify through the store: since
	// addFinalizer calls client.Patch with MergeFrom, and our fakeClient.Patch
	// is a no-op, the store itself is NOT updated. We verify the finalizer was
	// attempted by checking that Patch was called on the client.
	if fc.patches < 1 {
		t.Errorf("expected at least 1 patch call for finalizer add, got %d", fc.patches)
	}
}

func TestCleanupRunsOnDeletion(t *testing.T) {
	s := coreScheme()
	now := metav1.Now()
	store := newTestStore(s, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "my-cm", Namespace: "default", UID: "uid-1",
			DeletionTimestamp: &now,
			Finalizers:        []string{"make-reconcile.io/finalizer-test"},
		},
	})
	fc := &fakeClient{}
	mgr := &Manager{
		scheme:          s,
		cache:           store,
		client:          fc,
		log:             slog.Default(),
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		managerID:       "test",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)

	var cleanupCalled bool
	OnDelete(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) error {
		cleanupCalled = true
		return nil
	})

	Reconcile(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		t.Error("normal reconciler should not run during deletion")
		return nil
	})

	cmGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	handler := &eventHandler{mgr: mgr, gvk: cmGVK}
	handler.handle(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "default"},
	})

	if !cleanupCalled {
		t.Error("cleanup function should have been called")
	}
	// removeFinalizer should have been called via Patch
	if fc.patches < 1 {
		t.Errorf("expected at least 1 patch call for finalizer remove, got %d", fc.patches)
	}
}

func TestCleanupErrorRetainsFinalizerAndEmitsEvent(t *testing.T) {
	s := coreScheme()
	now := metav1.Now()
	store := newTestStore(s, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "my-cm", Namespace: "default", UID: "uid-1",
			DeletionTimestamp: &now,
			Finalizers:        []string{"make-reconcile.io/finalizer-test"},
		},
	})
	fc := &fakeClient{}
	rec := &internalEventRecorder{}
	mgr := &Manager{
		scheme:          s,
		cache:           store,
		client:          fc,
		log:             slog.Default(),
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		managerID:       "test",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
		eventRecorder:   rec,
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)

	OnDelete(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) error {
		return fmt.Errorf("external system unavailable")
	})

	primaryKey := types.NamespacedName{Name: "my-cm", Namespace: "default"}
	primary := mgr.getPrimary(context.Background(), configMaps.gvk, primaryKey)
	mgr.runCleanup(context.Background(), configMaps.gvk, primaryKey, primary)

	// Finalizer should NOT have been removed (no Patch call for remove)
	if fc.patches != 0 {
		t.Errorf("expected 0 patch calls when cleanup fails, got %d", fc.patches)
	}

	// Should emit a Warning event
	var found bool
	for _, ev := range rec.events {
		if ev.EventType == "Warning" && ev.Reason == "CleanupFailed" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected CleanupFailed event, got %+v", rec.events)
	}
}

func TestPredicateBypassForDeletion(t *testing.T) {
	s := coreScheme()
	now := metav1.Now()
	store := newTestStore(s, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "my-cm", Namespace: "default", UID: "uid-1",
			DeletionTimestamp: &now,
			Finalizers:        []string{"make-reconcile.io/finalizer-test"},
		},
	})
	fc := &fakeClient{}
	mgr := &Manager{
		scheme:          s,
		cache:           store,
		client:          fc,
		log:             slog.Default(),
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		managerID:       "test",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr, WithGenerationChanged())

	var cleanupCalled bool
	OnDelete(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) error {
		cleanupCalled = true
		return nil
	})

	Reconcile(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		return nil
	})

	cmGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	handler := &eventHandler{
		mgr:        mgr,
		gvk:        cmGVK,
		predicates: mgr.watchPredicates[cmGVK],
	}

	// Same generation (normally filtered by WithGenerationChanged), but with
	// DeletionTimestamp set. Predicates should be bypassed.
	oldCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "default", Generation: 1},
	}
	newCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "my-cm", Namespace: "default", Generation: 1,
			DeletionTimestamp: &now,
			Finalizers:        []string{"make-reconcile.io/finalizer-test"},
		},
	}
	handler.OnUpdate(oldCM, newCM)

	if !cleanupCalled {
		t.Error("cleanup should have run even though generation didn't change")
	}
}

func TestPredicateNotBypassedWithoutCleanup(t *testing.T) {
	s := coreScheme()
	store := newTestStore(s, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "default", UID: "uid-1"},
	})
	mgr := &Manager{
		scheme:          s,
		cache:           store,
		client:          &fakeClient{},
		log:             slog.Default(),
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		managerID:       "test",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
	}

	Watch[*corev1.ConfigMap](mgr, WithGenerationChanged())

	var invoked bool
	configMaps := Watch[*corev1.ConfigMap](mgr)
	Reconcile(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		invoked = true
		return nil
	})
	// No OnDelete registered

	cmGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	handler := &eventHandler{
		mgr:        mgr,
		gvk:        cmGVK,
		predicates: mgr.watchPredicates[cmGVK],
	}

	now := metav1.Now()
	oldCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "default", Generation: 1},
	}
	newCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "my-cm", Namespace: "default", Generation: 1,
			DeletionTimestamp: &now,
		},
	}
	handler.OnUpdate(oldCM, newCM)

	// No cleanup registered, so predicate should NOT be bypassed.
	// Generation didn't change, so the update should be rejected.
	if invoked {
		t.Error("reconciler should not have been invoked without cleanup registered and same generation")
	}
}

func TestStatusReconcilerSkippedDuringDeletion(t *testing.T) {
	s := coreScheme()
	now := metav1.Now()
	store := newTestStore(s, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "my-cm", Namespace: "default", UID: "uid-1",
			DeletionTimestamp: &now,
			Finalizers:        []string{"make-reconcile.io/finalizer-test"},
		},
	})
	fc := &fakeClient{}
	mgr := &Manager{
		scheme:          s,
		cache:           store,
		client:          fc,
		log:             slog.Default(),
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		managerID:       "test",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)

	OnDelete(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) error {
		return nil
	})

	Reconcile(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		return nil
	})

	type cmStatus struct {
		Ready bool `json:"ready"`
	}
	var statusInvoked bool
	ReconcileStatus(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *cmStatus {
		statusInvoked = true
		return &cmStatus{Ready: true}
	})

	cmGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	handler := &eventHandler{mgr: mgr, gvk: cmGVK}
	handler.handle(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "default"},
	})

	if statusInvoked {
		t.Error("status reconciler should not have been invoked during deletion")
	}
}

func TestCleanupClearsTrackerAndLastOutputs(t *testing.T) {
	s := coreScheme()
	now := metav1.Now()
	store := newTestStore(s, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "my-cm", Namespace: "default", UID: "uid-1",
			DeletionTimestamp: &now,
			Finalizers:        []string{"make-reconcile.io/finalizer-test"},
		},
	})
	fc := &fakeClient{}
	mgr := &Manager{
		scheme:          s,
		cache:           store,
		client:          fc,
		log:             slog.Default(),
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		managerID:       "test",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)

	OnDelete(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) error {
		return nil
	})

	Reconcile(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		return nil
	})

	primaryKey := types.NamespacedName{Name: "my-cm", Namespace: "default"}
	r := mgr.reconcilers[0]
	outputKey := r.ID() + "/" + primaryKey.String()

	// Seed lastOutputs and tracker.
	mgr.mu.Lock()
	mgr.lastOutputs[outputKey] = map[types.NamespacedName]bool{
		{Name: "my-cm-deploy", Namespace: "default"}: true,
	}
	mgr.mu.Unlock()

	deployGVK := schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
	ref := reconcilerRef{ReconcilerID: r.ID(), PrimaryKey: primaryKey}
	mgr.tracker.SetDeps(ref, []depKey{{GVK: deployGVK, NamespacedName: types.NamespacedName{Name: "my-cm-deploy", Namespace: "default"}}}, nil)

	// Run cleanup.
	primary := mgr.getPrimary(context.Background(), configMaps.gvk, primaryKey)
	mgr.runCleanup(context.Background(), configMaps.gvk, primaryKey, primary)

	// lastOutputs should be cleared.
	mgr.mu.Lock()
	if _, ok := mgr.lastOutputs[outputKey]; ok {
		t.Error("expected lastOutputs entry to be cleared after cleanup")
	}
	mgr.mu.Unlock()

	// Tracker should be cleared.
	refs := mgr.tracker.Lookup(deployGVK, types.NamespacedName{Name: "my-cm-deploy", Namespace: "default"})
	if len(refs) != 0 {
		t.Errorf("expected tracker entry to be cleared, got %d refs", len(refs))
	}
}

func TestFullReconcileHandlesDeletingPrimaries(t *testing.T) {
	s := coreScheme()
	now := metav1.Now()
	store := newTestStore(s,
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: "deleting-cm", Namespace: "default", UID: "uid-1",
				DeletionTimestamp: &now,
				Finalizers:        []string{"make-reconcile.io/finalizer-test"},
			},
		},
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: "normal-cm", Namespace: "default", UID: "uid-2",
			},
		},
	)
	fc := &fakeClient{}
	mgr := &Manager{
		scheme:          s,
		cache:           store,
		client:          fc,
		log:             slog.Default(),
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		managerID:       "test",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)

	var cleanupNames []string
	OnDelete(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) error {
		cleanupNames = append(cleanupNames, cm.Name)
		return nil
	})

	var reconciledNames []string
	Reconcile(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		reconciledNames = append(reconciledNames, cm.Name)
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-deploy", Namespace: cm.Namespace},
		}
	})

	mgr.fullReconcile(context.Background())

	if len(cleanupNames) != 1 || cleanupNames[0] != "deleting-cm" {
		t.Errorf("expected cleanup for deleting-cm, got %v", cleanupNames)
	}
	if len(reconciledNames) != 1 || reconciledNames[0] != "normal-cm" {
		t.Errorf("expected reconcile for normal-cm only, got %v", reconciledNames)
	}
}

func TestFinalizerNameIncludesManagerID(t *testing.T) {
	mgr := &Manager{managerID: "my-controller"}
	expected := "make-reconcile.io/finalizer-my-controller"
	if got := mgr.finalizerName(); got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestCleanupCallbackReceivesHandlerContext(t *testing.T) {
	s := coreScheme()
	now := metav1.Now()
	store := newTestStore(s,
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: "my-cm", Namespace: "default", UID: "uid-1",
				DeletionTimestamp: &now,
				Finalizers:        []string{"make-reconcile.io/finalizer-test"},
			},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "related-secret", Namespace: "default"},
			Data:       map[string][]byte{"key": []byte("value")},
		},
	)
	fc := &fakeClient{}
	mgr := &Manager{
		scheme:          s,
		cache:           store,
		client:          fc,
		log:             slog.Default(),
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		managerID:       "test",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)
	secrets := Watch[*corev1.Secret](mgr)

	var fetchedSecret *corev1.Secret
	OnDelete(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) error {
		fetchedSecret = Fetch(hc, secrets, FilterName("related-secret", cm.Namespace))
		return nil
	})

	Reconcile(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		return nil
	})

	primaryKey := types.NamespacedName{Name: "my-cm", Namespace: "default"}
	primary := mgr.getPrimary(context.Background(), configMaps.gvk, primaryKey)
	mgr.runCleanup(context.Background(), configMaps.gvk, primaryKey, primary)

	if fetchedSecret == nil {
		t.Error("expected cleanup to be able to Fetch resources via HandlerContext")
	}
	if fetchedSecret.Name != "related-secret" {
		t.Errorf("expected fetched secret name 'related-secret', got %q", fetchedSecret.Name)
	}
}

func TestRunSubReconcilerSkipsWhenPrimaryIsDeleting(t *testing.T) {
	s := coreScheme()
	now := metav1.Now()
	store := newTestStore(s, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "my-cm", Namespace: "default", UID: "uid-1",
			DeletionTimestamp: &now,
			Finalizers:        []string{"make-reconcile.io/finalizer-test"},
		},
	})
	fc := &fakeClient{}
	mgr := &Manager{
		scheme:          s,
		cache:           store,
		client:          fc,
		log:             slog.Default(),
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		managerID:       "test",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)

	OnDelete(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) error {
		return nil
	})

	var invoked bool
	Reconcile(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		invoked = true
		return nil
	})

	primaryKey := types.NamespacedName{Name: "my-cm", Namespace: "default"}
	mgr.runSubReconciler(context.Background(), mgr.reconcilers[0], primaryKey)

	if invoked {
		t.Error("sub-reconciler should not run when primary is being deleted")
	}
}

func TestNoFinalizerWithoutOnDelete(t *testing.T) {
	s := coreScheme()
	fc := &fakeClient{}
	store := newTestStore(s, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "default", UID: "uid-1"},
	})
	mgr := &Manager{
		scheme:          s,
		cache:           store,
		client:          fc,
		log:             slog.Default(),
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		managerID:       "test",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)
	// No OnDelete registered

	Reconcile(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-deploy", Namespace: cm.Namespace},
		}
	})

	primaryKey := types.NamespacedName{Name: "my-cm", Namespace: "default"}
	mgr.runSubReconciler(context.Background(), mgr.reconcilers[0], primaryKey)

	// No cleanup registered → no finalizer Patch should happen.
	// fakeClient.Patch is called for the output apply, but not for a finalizer.
	// The apply uses client.Apply patch type. If no OnDelete, the finalizer
	// add path is never entered, so only 1 Patch (for the apply).
	if fc.patches != 1 {
		t.Errorf("expected exactly 1 patch (output apply only), got %d", fc.patches)
	}
}

func TestCleanupOnlyRunsOncePerPrimaryInFullReconcile(t *testing.T) {
	s := coreScheme()
	now := metav1.Now()
	store := newTestStore(s, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "my-cm", Namespace: "default", UID: "uid-1",
			DeletionTimestamp: &now,
			Finalizers:        []string{"make-reconcile.io/finalizer-test"},
		},
	})
	fc := &fakeClient{}
	mgr := &Manager{
		scheme:          s,
		cache:           store,
		client:          fc,
		log:             slog.Default(),
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		managerID:       "test",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)

	var cleanupCount int
	OnDelete(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) error {
		cleanupCount++
		return nil
	})

	// Two reconcilers for the same primary GVK.
	Reconcile(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		return nil
	})
	Reconcile(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *corev1.Service {
		return nil
	})

	mgr.fullReconcile(context.Background())

	if cleanupCount != 1 {
		t.Errorf("expected cleanup to run exactly once, got %d", cleanupCount)
	}
}

