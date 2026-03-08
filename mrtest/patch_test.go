package mrtest_test

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	mr "github.com/aslakknutsen/make-reconcile"
	"github.com/aslakknutsen/make-reconcile/mrtest"
)

func TestPatchReconcilerAppliesForeignPatch(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)
	services := mr.Watch[*corev1.Service](mgr)

	mr.ReconcilePatch(mgr, configMaps, services,
		func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *corev1.Service {
			return &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cm.Data["target-svc"],
					Namespace: cm.Namespace,
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{Name: "injected", Port: 8080},
					},
				},
			}
		},
		mr.WithSSAPatch(),
	)

	h.SetObject(
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "my-session", Namespace: "default"},
			Data:       map[string]string{"target-svc": "shared-svc"},
		},
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "shared-svc", Namespace: "default"},
		},
	)

	result := h.Reconcile(cmGVK, types.NamespacedName{Name: "my-session", Namespace: "default"})

	result.AssertForeignPatchedCount(1)
	result.AssertForeignPatched("shared-svc", "default")
	result.AssertAppliedCount(0)
}

func TestPatchReconcilerNilRevertsContribution(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)
	services := mr.Watch[*corev1.Service](mgr)

	enabled := true
	mr.ReconcilePatch(mgr, configMaps, services,
		func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *corev1.Service {
			if !enabled {
				return nil
			}
			return &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "shared-svc", Namespace: cm.Namespace},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{{Name: "injected", Port: 8080}},
				},
			}
		},
		mr.WithSSAPatch(),
	)

	h.SetObject(
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "my-session", Namespace: "default"},
		},
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "shared-svc", Namespace: "default"},
		},
	)

	// First reconcile: applies patch.
	r1 := h.Reconcile(cmGVK, types.NamespacedName{Name: "my-session", Namespace: "default"})
	r1.AssertForeignPatchedCount(1)

	// Second reconcile: nil return should revert the previous contribution.
	enabled = false
	r2 := h.Reconcile(cmGVK, types.NamespacedName{Name: "my-session", Namespace: "default"})
	r2.AssertForeignPatchedCount(0)
	r2.AssertForeignRevertedCount(1)
	r2.AssertForeignReverted("shared-svc", "default")
}

func TestPatchManyReconciler(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)
	services := mr.Watch[*corev1.Service](mgr)

	mr.ReconcilePatchMany(mgr, configMaps, services,
		func(hc *mr.HandlerContext, cm *corev1.ConfigMap) []*corev1.Service {
			return []*corev1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "svc-a", Namespace: cm.Namespace},
					Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 80}}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "svc-b", Namespace: cm.Namespace},
					Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 443}}},
				},
			}
		},
	)

	h.SetObject(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "session", Namespace: "default"},
	})

	result := h.Reconcile(cmGVK, types.NamespacedName{Name: "session", Namespace: "default"})
	result.AssertForeignPatchedCount(2)
	result.AssertForeignPatched("svc-a", "default")
	result.AssertForeignPatched("svc-b", "default")
}

func TestPatchReconcilerAutoFinalizer(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)
	services := mr.Watch[*corev1.Service](mgr)

	mr.ReconcilePatch(mgr, configMaps, services,
		func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *corev1.Service {
			return &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "shared-svc", Namespace: cm.Namespace},
				Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 8080}}},
			}
		},
	)

	h.SetObject(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "session", Namespace: "default", UID: "uid-1"},
	})

	h.Reconcile(cmGVK, types.NamespacedName{Name: "session", Namespace: "default"})

	cm := mrtest.GetObject[*corev1.ConfigMap](h, "session", "default")
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
		t.Errorf("expected finalizer on primary, got %v", cm.GetFinalizers())
	}
}

func TestPatchReconcilerRevertsOnPrimaryDeletion(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)
	services := mr.Watch[*corev1.Service](mgr)

	mr.ReconcilePatch(mgr, configMaps, services,
		func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *corev1.Service {
			return &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "shared-svc", Namespace: cm.Namespace},
				Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 8080}}},
			}
		},
	)

	// First reconcile: applies the patch and adds finalizer.
	h.SetObject(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "session", Namespace: "default", UID: "uid-1"},
	})
	h.Reconcile(cmGVK, types.NamespacedName{Name: "session", Namespace: "default"})

	// Simulate deletion: set DeletionTimestamp with finalizer still present.
	now := metav1.Now()
	h.SetObject(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "session", Namespace: "default", UID: "uid-1",
			DeletionTimestamp: &now,
			Finalizers:        []string{"make-reconcile.io/finalizer-default"},
		},
	})

	result := h.Reconcile(cmGVK, types.NamespacedName{Name: "session", Namespace: "default"})

	// The patch should have been reverted.
	result.AssertForeignRevertedCount(1)
	result.AssertForeignReverted("shared-svc", "default")

	// No new patches applied.
	result.AssertForeignPatchedCount(0)
	result.AssertAppliedCount(0)

	// Finalizer should be removed.
	cm := mrtest.GetObject[*corev1.ConfigMap](h, "session", "default")
	if cm != nil {
		for _, f := range cm.GetFinalizers() {
			if f == "make-reconcile.io/finalizer-default" {
				t.Error("finalizer should have been removed after cleanup")
			}
		}
	}
}

func TestPatchReconcilerTargetShrinks(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)
	services := mr.Watch[*corev1.Service](mgr)

	targetCount := 2
	mr.ReconcilePatchMany(mgr, configMaps, services,
		func(hc *mr.HandlerContext, cm *corev1.ConfigMap) []*corev1.Service {
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
	)

	h.SetObject(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "session", Namespace: "default"},
	})
	key := types.NamespacedName{Name: "session", Namespace: "default"}

	// First reconcile: patches svc-a and svc-b.
	r1 := h.Reconcile(cmGVK, key)
	r1.AssertForeignPatchedCount(2)

	// Shrink to 1 target.
	targetCount = 1
	r2 := h.Reconcile(cmGVK, key)
	r2.AssertForeignPatchedCount(1)
	r2.AssertForeignPatched("svc-a", "default")
	r2.AssertForeignRevertedCount(1)
	r2.AssertForeignReverted("svc-b", "default")
}

func TestPatchReconcilerRunsDuringSettle(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)
	services := mr.Watch[*corev1.Service](mgr)

	mr.ReconcilePatch(mgr, configMaps, services,
		func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *corev1.Service {
			return &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "shared-svc", Namespace: cm.Namespace},
			}
		},
	)

	h.SetObject(
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "s1", Namespace: "default"}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "s2", Namespace: "default"}},
	)

	result := h.Settle()
	result.AssertForeignPatchedCount(2)
}

func TestPatchReconcilerWithFetchDependency(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)
	secrets := mr.Watch[*corev1.Secret](mgr)
	services := mr.Watch[*corev1.Service](mgr)

	mr.ReconcilePatch(mgr, configMaps, services,
		func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *corev1.Service {
			sec := mr.Fetch(hc, secrets, mr.FilterName(cm.Name+"-config", cm.Namespace))
			if sec == nil {
				return nil
			}
			return &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      sec.StringData["target"],
					Namespace: cm.Namespace,
				},
			}
		},
	)

	h.SetObject(
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "session", Namespace: "default"},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "session-config", Namespace: "default"},
			StringData: map[string]string{"target": "my-vs"},
		},
	)

	result := h.Reconcile(cmGVK, types.NamespacedName{Name: "session", Namespace: "default"})
	result.AssertForeignPatchedCount(1)
	result.AssertForeignPatched("my-vs", "default")
}

func TestPatchReconcilerCoexistsWithOutputReconciler(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)
	services := mr.Watch[*corev1.Service](mgr)

	// Regular output reconciler: creates a Service we own.
	mr.Reconcile(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *corev1.Service {
		return &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-owned", Namespace: cm.Namespace},
		}
	})

	// Foreign patch reconciler: contributes to a Service we don't own.
	mr.ReconcilePatch(mgr, configMaps, services,
		func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *corev1.Service {
			return &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "shared-svc", Namespace: cm.Namespace},
				Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 9090}}},
			}
		},
	)

	h.SetObject(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default", UID: "uid-1"},
	})

	result := h.Reconcile(cmGVK, types.NamespacedName{Name: "app", Namespace: "default"})

	// Regular output should be applied normally.
	result.AssertAppliedCount(1)
	result.AssertApplied("app-owned", "default")

	// Foreign patch should be tracked separately.
	result.AssertForeignPatchedCount(1)
	result.AssertForeignPatched("shared-svc", "default")
}
