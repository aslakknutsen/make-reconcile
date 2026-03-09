package makereconcile

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

func TestPatchReconcilerFailedRevertRetainsKey(t *testing.T) {
	s := coreScheme()

	store := newTestStore(s, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "session", Namespace: "default"},
	})

	strategy := &failingRevertStrategy{
		failKeys: map[types.NamespacedName]bool{
			{Name: "svc-b", Namespace: "default"}: true,
		},
	}

	targetCount := 2
	mgr := &Manager{
		scheme:          s,
		client:          &fakeClient{},
		cache:           store,
		log:             slog.Default(),
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		managerID:       "default",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
		lastPatches:     make(map[string]map[types.NamespacedName]bool),
		ownership:       &ownerRefStrategy{},
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)
	services := Watch[*corev1.Service](mgr)

	ReconcilePatchMany(mgr, configMaps, services,
		func(hc *HandlerContext, cm *corev1.ConfigMap) []*corev1.Service {
			var out []*corev1.Service
			for i := 0; i < targetCount; i++ {
				out = append(out, &corev1.Service{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "svc-" + string(rune('a'+i)),
						Namespace: cm.Namespace,
					},
				})
			}
			return out
		},
		func(cfg *patchConfig) { cfg.strategy = strategy },
	)

	r := mgr.patchReconcilers[0]
	primaryKey := types.NamespacedName{Name: "session", Namespace: "default"}
	patchKey := fmt.Sprintf("%s/%s", r.ID(), primaryKey.String())
	ctx := context.Background()

	// First reconcile: patches svc-a and svc-b.
	mgr.runPatchReconciler(ctx, r, primaryKey)

	mgr.mu.Lock()
	tracked := mgr.lastPatches[patchKey]
	mgr.mu.Unlock()
	if len(tracked) != 2 {
		t.Fatalf("expected 2 tracked keys after first reconcile, got %d", len(tracked))
	}

	// Shrink to 1 target. Revert of svc-b will fail.
	targetCount = 1
	strategy.resetRecorded()
	mgr.runPatchReconciler(ctx, r, primaryKey)

	mgr.mu.Lock()
	tracked = mgr.lastPatches[patchKey]
	mgr.mu.Unlock()

	svcB := types.NamespacedName{Name: "svc-b", Namespace: "default"}
	if !tracked[svcB] {
		t.Fatal("svc-b should remain in lastPatches after failed revert")
	}
	if len(tracked) != 2 {
		t.Fatalf("expected 2 tracked keys (svc-a wanted + svc-b failed revert), got %d", len(tracked))
	}

	// Verify svc-b was not recorded as successfully reverted.
	for _, k := range strategy.getReverted() {
		if k == svcB {
			t.Fatal("svc-b should not appear in successful reverts")
		}
	}

	// Allow svc-b revert to succeed on the next reconcile.
	strategy.clearFailKey(svcB)
	strategy.resetRecorded()
	mgr.runPatchReconciler(ctx, r, primaryKey)

	mgr.mu.Lock()
	tracked = mgr.lastPatches[patchKey]
	mgr.mu.Unlock()

	if tracked[svcB] {
		t.Fatal("svc-b should be removed from lastPatches after successful revert")
	}
	if len(tracked) != 1 {
		t.Fatalf("expected 1 tracked key after successful revert, got %d", len(tracked))
	}

	found := false
	for _, k := range strategy.getReverted() {
		if k == svcB {
			found = true
		}
	}
	if !found {
		t.Fatal("svc-b should appear in successful reverts after retry")
	}
}

// failingRevertStrategy is a patchStrategy that returns errors for
// configurable target keys on Revert, letting tests exercise the
// failed-revert retention logic in runPatchReconciler.
type failingRevertStrategy struct {
	mu       sync.Mutex
	failKeys map[types.NamespacedName]bool
	applied  []types.NamespacedName
	reverted []types.NamespacedName
}

func (s *failingRevertStrategy) Apply(_ context.Context, _ client.Client, _ *runtime.Scheme,
	_ schema.GroupVersionKind, targetKey types.NamespacedName,
	_ client.Object, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applied = append(s.applied, targetKey)
	return nil
}

func (s *failingRevertStrategy) Revert(_ context.Context, _ client.Client, _ *runtime.Scheme,
	_ schema.GroupVersionKind, targetKey types.NamespacedName, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failKeys[targetKey] {
		return fmt.Errorf("simulated revert failure for %v", targetKey)
	}
	s.reverted = append(s.reverted, targetKey)
	return nil
}

func (s *failingRevertStrategy) clearFailKey(key types.NamespacedName) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.failKeys, key)
}

func (s *failingRevertStrategy) resetRecorded() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applied = nil
	s.reverted = nil
}

func (s *failingRevertStrategy) getReverted() []types.NamespacedName {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]types.NamespacedName, len(s.reverted))
	copy(cp, s.reverted)
	return cp
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
