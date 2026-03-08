package mrtest_test

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	mr "github.com/aslakknutsen/make-reconcile"
	"github.com/aslakknutsen/make-reconcile/mrtest"
)

// --- Annotation-based ownership tests ---

func TestAnnotationOwnershipAddsContributorOnApply(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme(), mr.WithAnnotationOwnership())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)
	mr.Reconcile(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *corev1.Service {
		return &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "shared-svc", Namespace: cm.Namespace},
		}
	})

	h.SetObject(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "primary-a", Namespace: "default", UID: "uid-a"},
	})

	result := h.Reconcile(cmGVK, types.NamespacedName{Name: "primary-a", Namespace: "default"})
	result.AssertApplied("shared-svc", "default")

	svc := mrtest.GetObject[*corev1.Service](h, "shared-svc", "default")
	if svc == nil {
		t.Fatal("expected service in store")
	}

	ann := svc.GetAnnotations()
	if ann == nil {
		t.Fatal("expected annotations on service")
	}
	raw, ok := ann["make-reconcile.io/contributors"]
	if !ok {
		t.Fatal("expected contributor annotation")
	}
	if raw != `["ConfigMap/default/primary-a"]` {
		t.Errorf("unexpected contributor annotation: %s", raw)
	}
}

func TestAnnotationOwnershipNoOwnerRef(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme(), mr.WithAnnotationOwnership())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)
	mr.Reconcile(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *corev1.Service {
		return &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-svc", Namespace: cm.Namespace},
		}
	})

	h.SetObject(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default", UID: "uid-1"},
	})

	h.Reconcile(cmGVK, types.NamespacedName{Name: "app", Namespace: "default"})

	svc := mrtest.GetObject[*corev1.Service](h, "app-svc", "default")
	if svc == nil {
		t.Fatal("expected service in store")
	}
	if len(svc.GetOwnerReferences()) != 0 {
		t.Errorf("annotation strategy should not set OwnerReferences, got %v", svc.GetOwnerReferences())
	}
}

func TestAnnotationOwnershipTwoPrimariesSameOutput(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme(), mr.WithAnnotationOwnership())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)
	mr.Reconcile(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *corev1.Service {
		return &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "shared-svc", Namespace: cm.Namespace},
		}
	})

	h.SetObject(
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "primary-a", Namespace: "default", UID: "uid-a"}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "primary-b", Namespace: "default", UID: "uid-b"}},
	)

	h.Reconcile(cmGVK, types.NamespacedName{Name: "primary-a", Namespace: "default"})
	h.Reconcile(cmGVK, types.NamespacedName{Name: "primary-b", Namespace: "default"})

	svc := mrtest.GetObject[*corev1.Service](h, "shared-svc", "default")
	if svc == nil {
		t.Fatal("expected service in store")
	}

	ann := svc.GetAnnotations()
	raw := ann["make-reconcile.io/contributors"]
	// Both contributors should be listed.
	if raw != `["ConfigMap/default/primary-a","ConfigMap/default/primary-b"]` {
		t.Errorf("unexpected contributors: %s", raw)
	}
}

func TestAnnotationOwnershipDeleteOnePrimaryLeavesOutput(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme(), mr.WithAnnotationOwnership())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)
	mr.Reconcile(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *corev1.Service {
		return &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "shared-svc", Namespace: cm.Namespace},
		}
	})

	h.SetObject(
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "primary-a", Namespace: "default", UID: "uid-a"}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "primary-b", Namespace: "default", UID: "uid-b"}},
	)

	// Both primaries reconcile, adding contributors.
	h.Reconcile(cmGVK, types.NamespacedName{Name: "primary-a", Namespace: "default"})
	h.Reconcile(cmGVK, types.NamespacedName{Name: "primary-b", Namespace: "default"})

	// Delete primary-a: set DeletionTimestamp.
	now := metav1.Now()
	h.SetObject(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "primary-a", Namespace: "default", UID: "uid-a",
			DeletionTimestamp: &now,
			Finalizers:        []string{"make-reconcile.io/finalizer-default"},
		},
	})

	result := h.Reconcile(cmGVK, types.NamespacedName{Name: "primary-a", Namespace: "default"})

	// The shared service should NOT be deleted — primary-b still contributes.
	result.AssertDeletedCount(0)

	svc := mrtest.GetObject[*corev1.Service](h, "shared-svc", "default")
	if svc == nil {
		t.Fatal("expected shared service to still exist")
	}

	ann := svc.GetAnnotations()
	raw := ann["make-reconcile.io/contributors"]
	if raw != `["ConfigMap/default/primary-b"]` {
		t.Errorf("expected only primary-b contributor, got %s", raw)
	}
}

func TestAnnotationOwnershipDeleteLastPrimaryDeletesOutput(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme(), mr.WithAnnotationOwnership())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)
	mr.Reconcile(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *corev1.Service {
		return &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "shared-svc", Namespace: cm.Namespace},
		}
	})

	h.SetObject(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "only-primary", Namespace: "default", UID: "uid-1"},
	})

	h.Reconcile(cmGVK, types.NamespacedName{Name: "only-primary", Namespace: "default"})

	// Delete the only primary.
	now := metav1.Now()
	h.SetObject(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "only-primary", Namespace: "default", UID: "uid-1",
			DeletionTimestamp: &now,
			Finalizers:        []string{"make-reconcile.io/finalizer-default"},
		},
	})

	result := h.Reconcile(cmGVK, types.NamespacedName{Name: "only-primary", Namespace: "default"})

	// The shared service should be deleted — no contributors remain.
	result.AssertDeleted("shared-svc", "default")
}

func TestAnnotationOwnershipFinalizerAddedAutomatically(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme(), mr.WithAnnotationOwnership())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)
	// No OnDelete registered — annotation strategy should still add finalizer.
	mr.Reconcile(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *corev1.Service {
		return &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-svc", Namespace: cm.Namespace},
		}
	})

	h.SetObject(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default", UID: "uid-1"},
	})

	h.Reconcile(cmGVK, types.NamespacedName{Name: "app", Namespace: "default"})

	cm := mrtest.GetObject[*corev1.ConfigMap](h, "app", "default")
	if cm == nil {
		t.Fatal("expected ConfigMap in store")
	}
	found := false
	for _, f := range cm.GetFinalizers() {
		if f == "make-reconcile.io/finalizer-default" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected finalizer on ConfigMap (annotation strategy), got: %v", cm.GetFinalizers())
	}
}

func TestAnnotationOwnershipWithUserCleanup(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme(), mr.WithAnnotationOwnership())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)

	var cleanupCalled bool
	mr.OnDelete(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) error {
		cleanupCalled = true
		return nil
	})

	mr.Reconcile(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *corev1.Service {
		return &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "shared-svc", Namespace: cm.Namespace},
		}
	})

	h.SetObject(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default", UID: "uid-1"},
	})
	h.Reconcile(cmGVK, types.NamespacedName{Name: "app", Namespace: "default"})

	// Delete.
	now := metav1.Now()
	h.SetObject(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "app", Namespace: "default", UID: "uid-1",
			DeletionTimestamp: &now,
			Finalizers:        []string{"make-reconcile.io/finalizer-default"},
		},
	})
	h.Reconcile(cmGVK, types.NamespacedName{Name: "app", Namespace: "default"})

	if !cleanupCalled {
		t.Error("expected user cleanup to be called alongside annotation release")
	}
}

func TestAnnotationOwnershipStaleOutputRelease(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme(), mr.WithAnnotationOwnership())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)

	// Reconciler that conditionally produces output.
	mr.Reconcile(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *corev1.Service {
		if cm.Data["produce"] == "true" {
			return &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "shared-svc", Namespace: cm.Namespace},
			}
		}
		return nil
	})

	key := types.NamespacedName{Name: "primary-a", Namespace: "default"}

	// Two primaries produce the same output.
	h.SetObject(
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "primary-a", Namespace: "default", UID: "uid-a"},
			Data:       map[string]string{"produce": "true"},
		},
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "primary-b", Namespace: "default", UID: "uid-b"},
			Data:       map[string]string{"produce": "true"},
		},
	)

	h.Reconcile(cmGVK, key)
	h.Reconcile(cmGVK, types.NamespacedName{Name: "primary-b", Namespace: "default"})

	// Now primary-a stops producing the output (returns nil).
	h.SetObject(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "primary-a", Namespace: "default", UID: "uid-a"},
		Data:       map[string]string{"produce": "false"},
	})
	result := h.Reconcile(cmGVK, key)

	// Service should NOT be deleted — primary-b still contributes.
	result.AssertDeletedCount(0)

	svc := mrtest.GetObject[*corev1.Service](h, "shared-svc", "default")
	if svc == nil {
		t.Fatal("expected service to still exist")
	}
	ann := svc.GetAnnotations()
	raw := ann["make-reconcile.io/contributors"]
	if raw != `["ConfigMap/default/primary-b"]` {
		t.Errorf("expected only primary-b contributor, got %s", raw)
	}
}

func TestAnnotationOwnershipSettleWithMultiplePrimaries(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme(), mr.WithAnnotationOwnership())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)
	mr.Reconcile(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *corev1.Service {
		return &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "shared-svc", Namespace: cm.Namespace},
		}
	})

	h.SetObject(
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "default", UID: "uid-a"}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: "default", UID: "uid-b"}},
	)

	h.Settle()

	svc := mrtest.GetObject[*corev1.Service](h, "shared-svc", "default")
	if svc == nil {
		t.Fatal("expected shared service after settle")
	}

	ann := svc.GetAnnotations()
	raw := ann["make-reconcile.io/contributors"]
	// Both should be listed (order depends on map iteration, but both must be present).
	if raw == "" {
		t.Fatal("expected contributor annotation")
	}
	// Check both are present.
	containsA := false
	containsB := false
	if len(raw) > 0 {
		containsA = contains(raw, "ConfigMap/default/a")
		containsB = contains(raw, "ConfigMap/default/b")
	}
	if !containsA || !containsB {
		t.Errorf("expected both contributors, got %s", raw)
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Verify that ownerRef strategy (default) is unchanged.
func TestDefaultOwnerRefStrategyStillWorks(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)
	mr.Reconcile(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *corev1.Service {
		return &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-svc", Namespace: cm.Namespace},
		}
	})

	h.SetObject(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default", UID: "uid-1"},
	})

	result := h.Reconcile(cmGVK, types.NamespacedName{Name: "app", Namespace: "default"})
	result.AssertApplied("app-svc", "default")

	svc := mrtest.GetObject[*corev1.Service](h, "app-svc", "default")
	if svc == nil {
		t.Fatal("expected service")
	}

	// Should have OwnerReference (default strategy).
	if len(svc.GetOwnerReferences()) == 0 {
		t.Error("expected OwnerReference on output with default strategy")
	}

	// Should NOT have contributor annotation.
	ann := svc.GetAnnotations()
	if ann != nil {
		if _, ok := ann["make-reconcile.io/contributors"]; ok {
			t.Error("default strategy should not set contributor annotation")
		}
	}
}

// Verify conditional delete with ownerRef strategy works.
func TestDefaultStrategyConditionalDelete(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)
	mr.Reconcile(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *corev1.Service {
		if cm.Data["create"] == "true" {
			return &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-svc", Namespace: cm.Namespace},
			}
		}
		return nil
	})

	key := types.NamespacedName{Name: "app", Namespace: "default"}

	h.SetObject(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default", UID: "uid-1"},
		Data:       map[string]string{"create": "true"},
	})
	h.Reconcile(cmGVK, key)

	h.SetObject(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default", UID: "uid-1"},
		Data:       map[string]string{"create": "false"},
	})
	result := h.Reconcile(cmGVK, key)
	result.AssertDeleted("app-svc", "default")
}

func TestAnnotationOwnershipStatusSkippedDuringDeletion(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme(), mr.WithAnnotationOwnership())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)

	mr.Reconcile(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *corev1.Service {
		return &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-svc", Namespace: cm.Namespace},
		}
	})

	type status struct {
		Done bool `json:"done"`
	}
	var statusInvoked bool
	mr.ReconcileStatus(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *status {
		statusInvoked = true
		return &status{Done: true}
	})

	// First reconcile.
	h.SetObject(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default", UID: "uid-1"},
	})
	h.Reconcile(cmGVK, types.NamespacedName{Name: "app", Namespace: "default"})
	statusInvoked = false

	// Delete.
	now := metav1.Now()
	h.SetObject(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "app", Namespace: "default", UID: "uid-1",
			DeletionTimestamp: &now,
			Finalizers:        []string{"make-reconcile.io/finalizer-default"},
		},
	})
	h.Reconcile(cmGVK, types.NamespacedName{Name: "app", Namespace: "default"})

	if statusInvoked {
		t.Error("status reconciler should not run during deletion with annotation strategy")
	}
}
