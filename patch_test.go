package makereconcile

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

func TestReconcilePatchRegistration(t *testing.T) {
	s := coreScheme()
	mgr := &Manager{
		scheme:          s,
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		managerID:       "default",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
		lastPatches:     make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)
	services := Watch[*corev1.Service](mgr)

	ReconcilePatch(mgr, configMaps, services, func(hc *HandlerContext, cm *corev1.ConfigMap) *corev1.Service {
		return &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-svc", Namespace: cm.Namespace},
		}
	})

	if len(mgr.patchReconcilers) != 1 {
		t.Fatalf("expected 1 patch reconciler, got %d", len(mgr.patchReconcilers))
	}

	r := mgr.patchReconcilers[0]
	if r.ID() != "ConfigMap~>Service" {
		t.Errorf("unexpected ID: %s", r.ID())
	}

	svcGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Service"}
	if r.TargetGVK() != svcGVK {
		t.Errorf("unexpected target GVK: %v", r.TargetGVK())
	}
}

func TestReconcilePatchManyRegistration(t *testing.T) {
	s := coreScheme()
	mgr := &Manager{
		scheme:          s,
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		managerID:       "default",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
		lastPatches:     make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)
	services := Watch[*corev1.Service](mgr)

	ReconcilePatchMany(mgr, configMaps, services, func(hc *HandlerContext, cm *corev1.ConfigMap) []*corev1.Service {
		return []*corev1.Service{
			{ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-svc-a", Namespace: cm.Namespace}},
			{ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-svc-b", Namespace: cm.Namespace}},
		}
	})

	if len(mgr.patchReconcilers) != 1 {
		t.Fatalf("expected 1 patch reconciler, got %d", len(mgr.patchReconcilers))
	}
	if mgr.patchReconcilers[0].ID() != "ConfigMap~>[]Service" {
		t.Errorf("unexpected ID: %s", mgr.patchReconcilers[0].ID())
	}
}

func TestReconcilePatchDuplicateID(t *testing.T) {
	s := coreScheme()
	mgr := &Manager{
		scheme:          s,
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		managerID:       "default",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
		lastPatches:     make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)
	services := Watch[*corev1.Service](mgr)

	ReconcilePatch(mgr, configMaps, services, func(hc *HandlerContext, cm *corev1.ConfigMap) *corev1.Service {
		return nil
	})
	ReconcilePatch(mgr, configMaps, services, func(hc *HandlerContext, cm *corev1.ConfigMap) *corev1.Service {
		return nil
	})

	if len(mgr.patchReconcilers) != 2 {
		t.Fatalf("expected 2 patch reconcilers, got %d", len(mgr.patchReconcilers))
	}
	if mgr.patchReconcilers[0].ID() != "ConfigMap~>Service" {
		t.Errorf("first ID: %s", mgr.patchReconcilers[0].ID())
	}
	if mgr.patchReconcilers[1].ID() != "ConfigMap~>Service#1" {
		t.Errorf("second ID: %s", mgr.patchReconcilers[1].ID())
	}
}

func TestPatchSubReconcilerInvocation(t *testing.T) {
	s := coreScheme()
	store := newTestStore(s, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "default"},
	})
	mgr := &Manager{
		scheme:          s,
		cache:           store,
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		managerID:       "default",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
		lastPatches:     make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)
	services := Watch[*corev1.Service](mgr)

	var invoked bool
	ReconcilePatch(mgr, configMaps, services, func(hc *HandlerContext, cm *corev1.ConfigMap) *corev1.Service {
		invoked = true
		return &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-svc", Namespace: cm.Namespace},
		}
	})

	r := mgr.patchReconcilers[0]
	outputs, err := r.Reconcile(context.Background(), mgr, types.NamespacedName{Name: "my-cm", Namespace: "default"})
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}
	if !invoked {
		t.Fatal("patch reconciler was not invoked")
	}
	if len(outputs) != 1 {
		t.Fatalf("expected 1 output, got %d", len(outputs))
	}
	if outputs[0].TargetKey.Name != "my-cm-svc" {
		t.Errorf("expected target name 'my-cm-svc', got %q", outputs[0].TargetKey.Name)
	}
}

func TestPatchSubReconcilerNilMeansRemove(t *testing.T) {
	s := coreScheme()
	store := newTestStore(s, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "default"},
	})
	mgr := &Manager{
		scheme:          s,
		cache:           store,
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		managerID:       "default",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
		lastPatches:     make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)
	services := Watch[*corev1.Service](mgr)

	ReconcilePatch(mgr, configMaps, services, func(hc *HandlerContext, cm *corev1.ConfigMap) *corev1.Service {
		return nil
	})

	r := mgr.patchReconcilers[0]
	outputs, err := r.Reconcile(context.Background(), mgr, types.NamespacedName{Name: "my-cm", Namespace: "default"})
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}
	if outputs != nil {
		t.Errorf("expected nil outputs for nil return, got %v", outputs)
	}
}

func TestPatchManySubReconcilerInvocation(t *testing.T) {
	s := coreScheme()
	store := newTestStore(s, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "ns"},
	})
	mgr := &Manager{
		scheme:          s,
		cache:           store,
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		managerID:       "default",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
		lastPatches:     make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)
	services := Watch[*corev1.Service](mgr)

	ReconcilePatchMany(mgr, configMaps, services, func(hc *HandlerContext, cm *corev1.ConfigMap) []*corev1.Service {
		return []*corev1.Service{
			{ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-a", Namespace: cm.Namespace}},
			{ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-b", Namespace: cm.Namespace}},
		}
	})

	r := mgr.patchReconcilers[0]
	outputs, err := r.Reconcile(context.Background(), mgr, types.NamespacedName{Name: "my-cm", Namespace: "ns"})
	if err != nil {
		t.Fatal(err)
	}
	if len(outputs) != 2 {
		t.Fatalf("expected 2 outputs, got %d", len(outputs))
	}
}

func TestPatchManyDuplicateTargetKeyReturnsError(t *testing.T) {
	s := coreScheme()
	store := newTestStore(s, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "ns"},
	})
	mgr := &Manager{
		scheme:          s,
		cache:           store,
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		managerID:       "default",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
		lastPatches:     make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)
	services := Watch[*corev1.Service](mgr)

	ReconcilePatchMany(mgr, configMaps, services, func(hc *HandlerContext, cm *corev1.ConfigMap) []*corev1.Service {
		return []*corev1.Service{
			{ObjectMeta: metav1.ObjectMeta{Name: "dup-svc", Namespace: cm.Namespace}},
			{ObjectMeta: metav1.ObjectMeta{Name: "dup-svc", Namespace: cm.Namespace}},
		}
	})

	r := mgr.patchReconcilers[0]
	_, err := r.Reconcile(context.Background(), mgr, types.NamespacedName{Name: "my-cm", Namespace: "ns"})
	if err == nil {
		t.Fatal("expected error for duplicate target keys, got nil")
	}
	if got := err.Error(); got != "duplicate target key ns/dup-svc returned from patch reconciler ConfigMap~>[]Service" {
		t.Errorf("unexpected error message: %s", got)
	}
}

func TestPatchFieldManagerName(t *testing.T) {
	name := patchFieldManagerName("default/ConfigMap~>Service/default/my-cm")
	if name != "mr-patch/default/ConfigMap~>Service/default/my-cm" {
		t.Errorf("unexpected field manager: %s", name)
	}

	// Verify truncation for long names.
	long := ""
	for i := 0; i < 200; i++ {
		long += "x"
	}
	name = patchFieldManagerName(long)
	if len(name) > 128 {
		t.Errorf("field manager exceeds 128 chars: %d", len(name))
	}
}

func TestHasPatchReconcilersAffectsNeedsFinalizer(t *testing.T) {
	s := coreScheme()
	mgr := &Manager{
		scheme:          s,
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		managerID:       "default",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
		lastPatches:     make(map[string]map[types.NamespacedName]bool),
		ownership:       &ownerRefStrategy{},
	}

	cmGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	if mgr.needsFinalizer(cmGVK) {
		t.Error("needsFinalizer should be false before registering patch reconcilers")
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)
	services := Watch[*corev1.Service](mgr)

	ReconcilePatch(mgr, configMaps, services, func(hc *HandlerContext, cm *corev1.ConfigMap) *corev1.Service {
		return nil
	})

	if !mgr.needsFinalizer(cmGVK) {
		t.Error("needsFinalizer should be true after registering patch reconcilers")
	}
}
