package makereconcile

import (
	"context"
	"log/slog"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// --- Informer-level predicate tests ---

func TestWatchPredicatesRegistered(t *testing.T) {
	s := coreScheme()
	mgr := &Manager{
		scheme:          s,
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
	}

	Watch[*corev1.Pod](mgr, WithGenerationChanged())

	podGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"}
	if len(mgr.watchPredicates[podGVK]) != 1 {
		t.Fatalf("expected 1 predicate for Pod, got %d", len(mgr.watchPredicates[podGVK]))
	}
}

func TestWatchPredicatesANDMultipleCalls(t *testing.T) {
	s := coreScheme()
	mgr := &Manager{
		scheme:          s,
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
	}

	Watch[*corev1.Pod](mgr, WithGenerationChanged())
	Watch[*corev1.Pod](mgr, WithEventPredicate(EventPredicate{
		Create: func(obj client.Object) bool { return obj.GetNamespace() == "test" },
	}))

	podGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"}
	if len(mgr.watchPredicates[podGVK]) != 2 {
		t.Fatalf("expected 2 predicates for Pod, got %d", len(mgr.watchPredicates[podGVK]))
	}
}

func TestWatchNoPredicateDoesNotAddEntry(t *testing.T) {
	s := coreScheme()
	mgr := &Manager{
		scheme:          s,
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
	}

	Watch[*corev1.Pod](mgr)

	podGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"}
	if len(mgr.watchPredicates[podGVK]) != 0 {
		t.Fatalf("expected 0 predicates for Pod, got %d", len(mgr.watchPredicates[podGVK]))
	}
}

func TestEventHandlerOnAddPredicateRejects(t *testing.T) {
	s := coreScheme()
	fc := &fakeCache{
		objects: map[types.NamespacedName]runtime.Object{
			{Name: "my-cm", Namespace: "default"}: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "default", UID: "uid-1"},
			},
		},
	}
	mgr := &Manager{
		scheme:          s,
		cache:           fc,
		client:          &fakeClient{},
		log:             slog.Default(),
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		managerID:       "default",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)

	var invoked bool
	Reconcile(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		invoked = true
		return nil
	})

	cmGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	handler := &eventHandler{
		mgr: mgr,
		gvk: cmGVK,
		predicates: []EventPredicate{{
			Create: func(obj client.Object) bool { return false },
		}},
	}
	handler.OnAdd(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "default"},
	}, false)

	if invoked {
		t.Error("reconciler should not have been invoked when create predicate rejects")
	}
}

func TestEventHandlerOnUpdatePredicateRejects(t *testing.T) {
	s := coreScheme()
	fc := &fakeCache{
		objects: map[types.NamespacedName]runtime.Object{
			{Name: "my-cm", Namespace: "default"}: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "default", UID: "uid-1"},
			},
		},
	}
	mgr := &Manager{
		scheme:          s,
		cache:           fc,
		client:          &fakeClient{},
		log:             slog.Default(),
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		managerID:       "default",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)

	var invoked bool
	Reconcile(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		invoked = true
		return nil
	})

	cmGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	handler := &eventHandler{
		mgr: mgr,
		gvk: cmGVK,
		predicates: []EventPredicate{{
			Update: func(oldObj, newObj client.Object) bool {
				return oldObj.GetGeneration() != newObj.GetGeneration()
			},
		}},
	}

	oldCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "default", Generation: 1},
	}
	newCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "default", Generation: 1},
	}
	handler.OnUpdate(oldCM, newCM)

	if invoked {
		t.Error("reconciler should not have been invoked when update predicate rejects (same generation)")
	}
}

func TestEventHandlerOnUpdatePredicatePasses(t *testing.T) {
	s := coreScheme()
	fc := &fakeCache{
		objects: map[types.NamespacedName]runtime.Object{
			{Name: "my-cm", Namespace: "default"}: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "default", UID: "uid-1"},
			},
		},
	}
	mgr := &Manager{
		scheme:          s,
		cache:           fc,
		client:          &fakeClient{},
		log:             slog.Default(),
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		managerID:       "default",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)

	var invoked bool
	Reconcile(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		invoked = true
		return nil
	})

	cmGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	handler := &eventHandler{
		mgr: mgr,
		gvk: cmGVK,
		predicates: []EventPredicate{{
			Update: func(oldObj, newObj client.Object) bool {
				return oldObj.GetGeneration() != newObj.GetGeneration()
			},
		}},
	}

	oldCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "default", Generation: 1},
	}
	newCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "default", Generation: 2},
	}
	handler.OnUpdate(oldCM, newCM)

	if !invoked {
		t.Error("reconciler should have been invoked when generation changed")
	}
}

func TestEventHandlerNilPredicateFuncDefaultsToPass(t *testing.T) {
	s := coreScheme()
	fc := &fakeCache{
		objects: map[types.NamespacedName]runtime.Object{
			{Name: "my-cm", Namespace: "default"}: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "default", UID: "uid-1"},
			},
		},
	}
	mgr := &Manager{
		scheme:          s,
		cache:           fc,
		client:          &fakeClient{},
		log:             slog.Default(),
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		managerID:       "default",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)

	var invoked bool
	Reconcile(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		invoked = true
		return nil
	})

	cmGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	// Predicate with only Update set — Create and Delete are nil (should pass).
	handler := &eventHandler{
		mgr: mgr,
		gvk: cmGVK,
		predicates: []EventPredicate{{
			Update: func(oldObj, newObj client.Object) bool {
				return oldObj.GetGeneration() != newObj.GetGeneration()
			},
		}},
	}

	handler.OnAdd(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "default"},
	}, false)

	if !invoked {
		t.Error("OnAdd should pass when the predicate has nil Create func")
	}
}

func TestEventHandlerOnDeletePredicateRejects(t *testing.T) {
	s := coreScheme()
	mgr := &Manager{
		scheme:          s,
		client:          &fakeClient{},
		log:             slog.Default(),
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		managerID:       "default",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)

	var invoked bool
	Reconcile(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		invoked = true
		return nil
	})

	cmGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	handler := &eventHandler{
		mgr: mgr,
		gvk: cmGVK,
		predicates: []EventPredicate{{
			Delete: func(obj client.Object) bool { return false },
		}},
	}

	handler.OnDelete(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "default"},
	})

	if invoked {
		t.Error("reconciler should not have been invoked when delete predicate rejects")
	}
}

// --- Reconciler-level predicate tests ---

func TestReconcilePredicateSkipsNonMatching(t *testing.T) {
	s := coreScheme()
	fc := &fakeCache{
		objects: map[types.NamespacedName]runtime.Object{
			{Name: "my-cm", Namespace: "default"}: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: "my-cm", Namespace: "default",
					Labels: map[string]string{"role": "other"},
				},
			},
		},
	}
	mgr := &Manager{
		scheme:          s,
		cache:           fc,
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		managerID:       "default",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)

	var invoked bool
	Reconcile(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		invoked = true
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-deploy", Namespace: cm.Namespace},
		}
	}, WithPredicate(func(cm *corev1.ConfigMap) bool {
		return cm.Labels["role"] == "primary"
	}))

	r := mgr.reconcilers[0]
	desired, err := r.Reconcile(context.Background(), mgr, types.NamespacedName{Name: "my-cm", Namespace: "default"})
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}
	if invoked {
		t.Error("reconciler function should not have been called when predicate rejects")
	}
	if desired != nil {
		t.Error("expected nil desired when predicate rejects")
	}
}

func TestReconcilePredicatePassesMatching(t *testing.T) {
	s := coreScheme()
	fc := &fakeCache{
		objects: map[types.NamespacedName]runtime.Object{
			{Name: "my-cm", Namespace: "default"}: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: "my-cm", Namespace: "default",
					Labels: map[string]string{"role": "primary"},
				},
			},
		},
	}
	mgr := &Manager{
		scheme:          s,
		cache:           fc,
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		managerID:       "default",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)

	Reconcile(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-deploy", Namespace: cm.Namespace},
		}
	}, WithPredicate(func(cm *corev1.ConfigMap) bool {
		return cm.Labels["role"] == "primary"
	}))

	r := mgr.reconcilers[0]
	desired, err := r.Reconcile(context.Background(), mgr, types.NamespacedName{Name: "my-cm", Namespace: "default"})
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}
	if len(desired) != 1 {
		t.Fatalf("expected 1 desired object, got %d", len(desired))
	}
	if desired[0].GetName() != "my-cm-deploy" {
		t.Errorf("unexpected output name: %s", desired[0].GetName())
	}
}

func TestReconcilePredicateRejectClearsDeps(t *testing.T) {
	s := coreScheme()
	fc := &fakeCache{
		objects: map[types.NamespacedName]runtime.Object{
			{Name: "my-cm", Namespace: "default"}: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "default"},
			},
		},
	}
	mgr := &Manager{
		scheme:          s,
		cache:           fc,
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		managerID:       "default",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)
	secrets := Watch[*corev1.Secret](mgr)
	_ = secrets

	Reconcile(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		Fetch(hc, secrets, FilterName("some-secret", "default"))
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-deploy", Namespace: cm.Namespace},
		}
	}, WithPredicate(func(cm *corev1.ConfigMap) bool {
		return false
	}))

	r := mgr.reconcilers[0]
	primaryKey := types.NamespacedName{Name: "my-cm", Namespace: "default"}

	// Seed some deps as if a previous run matched.
	ref := reconcilerRef{ReconcilerID: r.ID(), PrimaryKey: primaryKey}
	secretGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Secret"}
	mgr.tracker.SetDeps(ref, []depKey{{GVK: secretGVK, NamespacedName: types.NamespacedName{Name: "some-secret", Namespace: "default"}}}, nil)

	refs := mgr.tracker.Lookup(secretGVK, types.NamespacedName{Name: "some-secret", Namespace: "default"})
	if len(refs) != 1 {
		t.Fatal("pre-condition: expected seeded dep")
	}

	_, err := r.Reconcile(context.Background(), mgr, primaryKey)
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	refs = mgr.tracker.Lookup(secretGVK, types.NamespacedName{Name: "some-secret", Namespace: "default"})
	if len(refs) != 0 {
		t.Errorf("expected deps to be cleared after predicate reject, got %d", len(refs))
	}
}

func TestReconcileManyPredicateSkips(t *testing.T) {
	s := coreScheme()
	fc := &fakeCache{
		objects: map[types.NamespacedName]runtime.Object{
			{Name: "my-cm", Namespace: "default"}: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "default"},
			},
		},
	}
	mgr := &Manager{
		scheme:          s,
		cache:           fc,
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		managerID:       "default",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)

	var invoked bool
	ReconcileMany(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) []*corev1.Service {
		invoked = true
		return []*corev1.Service{
			{ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-svc"}},
		}
	}, WithPredicate(func(cm *corev1.ConfigMap) bool {
		return false
	}))

	r := mgr.reconcilers[0]
	desired, err := r.Reconcile(context.Background(), mgr, types.NamespacedName{Name: "my-cm", Namespace: "default"})
	if err != nil {
		t.Fatal(err)
	}
	if invoked {
		t.Error("reconciler function should not have been called")
	}
	if desired != nil {
		t.Error("expected nil desired when predicate rejects")
	}
}

func TestReconcileStatusPredicateSkips(t *testing.T) {
	s := coreScheme()
	fc := &fakeCache{
		objects: map[types.NamespacedName]runtime.Object{
			{Name: "my-cm", Namespace: "default"}: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "default"},
			},
		},
	}
	fakeC := &fakeClient{}
	mgr := &Manager{
		scheme:          s,
		cache:           fc,
		client:          fakeC,
		log:             slog.Default(),
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		managerID:       "default",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)

	type cmStatus struct {
		Ready bool `json:"ready"`
	}

	var invoked bool
	ReconcileStatus(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *cmStatus {
		invoked = true
		return &cmStatus{Ready: true}
	}, WithPredicate(func(cm *corev1.ConfigMap) bool {
		return false
	}))

	sr := mgr.statusReconcilers[0]
	err := sr.ReconcileStatus(context.Background(), mgr, types.NamespacedName{Name: "my-cm", Namespace: "default"})
	if err != nil {
		t.Fatalf("status reconcile error: %v", err)
	}
	if invoked {
		t.Error("status reconciler function should not have been called")
	}
	if fakeC.statusPatches != 0 {
		t.Errorf("expected 0 status patches, got %d", fakeC.statusPatches)
	}
}

func TestReconcileStatusPredicateRejectClearsDeps(t *testing.T) {
	s := coreScheme()
	fc := &fakeCache{
		objects: map[types.NamespacedName]runtime.Object{
			{Name: "my-cm", Namespace: "default"}: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "default"},
			},
		},
	}
	fakeC := &fakeClient{}
	mgr := &Manager{
		scheme:          s,
		cache:           fc,
		client:          fakeC,
		log:             slog.Default(),
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		managerID:       "default",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)

	type cmStatus struct {
		Ready bool `json:"ready"`
	}

	ReconcileStatus(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *cmStatus {
		return &cmStatus{Ready: true}
	}, WithPredicate(func(cm *corev1.ConfigMap) bool {
		return false
	}))

	sr := mgr.statusReconcilers[0]
	primaryKey := types.NamespacedName{Name: "my-cm", Namespace: "default"}

	// Seed deps.
	ref := reconcilerRef{ReconcilerID: sr.ID(), PrimaryKey: primaryKey}
	deployGVK := schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
	mgr.tracker.SetDeps(ref, []depKey{{GVK: deployGVK, NamespacedName: types.NamespacedName{Name: "dep", Namespace: "default"}}}, nil)

	err := sr.ReconcileStatus(context.Background(), mgr, primaryKey)
	if err != nil {
		t.Fatalf("status reconcile error: %v", err)
	}

	refs := mgr.tracker.Lookup(deployGVK, types.NamespacedName{Name: "dep", Namespace: "default"})
	if len(refs) != 0 {
		t.Errorf("expected deps to be cleared after predicate reject, got %d", len(refs))
	}
}

// --- WithGenerationChanged predicate tests ---

func TestWithGenerationChangedPassesOnCreate(t *testing.T) {
	var cfg watchConfig
	WithGenerationChanged()(&cfg)

	if len(cfg.predicates) != 1 {
		t.Fatal("expected 1 predicate")
	}
	p := cfg.predicates[0]

	if p.Create != nil {
		t.Error("Create func should be nil (defaults to pass)")
	}
	if p.Delete != nil {
		t.Error("Delete func should be nil (defaults to pass)")
	}
}

func TestWithGenerationChangedRejectsUnchanged(t *testing.T) {
	var cfg watchConfig
	WithGenerationChanged()(&cfg)
	p := cfg.predicates[0]

	old := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Generation: 5}}
	new := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Generation: 5}}

	if p.Update(old, new) {
		t.Error("Update should reject when generation unchanged")
	}
}

func TestWithGenerationChangedPassesOnChange(t *testing.T) {
	var cfg watchConfig
	WithGenerationChanged()(&cfg)
	p := cfg.predicates[0]

	old := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Generation: 5}}
	new := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Generation: 6}}

	if !p.Update(old, new) {
		t.Error("Update should pass when generation changed")
	}
}

// --- Predicate + runSubReconciler integration ---

func TestRunSubReconcilerWithPredicateDeletesPreviousOutputs(t *testing.T) {
	s := coreScheme()
	fc := &fakeCache{
		objects: map[types.NamespacedName]runtime.Object{
			{Name: "my-cm", Namespace: "default"}: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: "my-cm", Namespace: "default", UID: "uid-1",
					Labels: map[string]string{"role": "other"},
				},
			},
		},
	}
	fakeC := &fakeClient{}
	mgr := &Manager{
		scheme:          s,
		cache:           fc,
		client:          fakeC,
		log:             slog.Default(),
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		managerID:       "default",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)

	Reconcile(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-deploy", Namespace: cm.Namespace},
		}
	}, WithPredicate(func(cm *corev1.ConfigMap) bool {
		return cm.Labels["role"] == "primary"
	}))

	primaryKey := types.NamespacedName{Name: "my-cm", Namespace: "default"}
	outputKey := mgr.reconcilers[0].ID() + "/" + primaryKey.String()

	// Simulate a previous run that produced output.
	mgr.mu.Lock()
	mgr.lastOutputs[outputKey] = map[types.NamespacedName]bool{
		{Name: "my-cm-deploy", Namespace: "default"}: true,
	}
	mgr.mu.Unlock()

	mgr.runSubReconciler(context.Background(), mgr.reconcilers[0], primaryKey)

	// Predicate rejects -> nil desired -> previous output should be deleted.
	if len(fakeC.deleted) != 1 {
		t.Fatalf("expected 1 deletion of stale output, got %d", len(fakeC.deleted))
	}
	if fakeC.deleted[0].Name != "my-cm-deploy" {
		t.Errorf("expected my-cm-deploy to be deleted, got %q", fakeC.deleted[0].Name)
	}
}
