package makereconcile

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

var (
	deployGVK   = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
	configMapGVK = schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	serviceGVK  = schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Service"}
)

func TestTracker_NarrowLookup(t *testing.T) {
	dt := newDependencyTracker()

	ref := reconcilerRef{ReconcilerID: "app->Deployment", PrimaryKey: types.NamespacedName{Name: "myapp", Namespace: "default"}}
	cmKey := depKey{GVK: configMapGVK, NamespacedName: types.NamespacedName{Name: "app-config", Namespace: "default"}}

	dt.SetDeps(ref, []depKey{cmKey}, nil)

	got := dt.Lookup(configMapGVK, types.NamespacedName{Name: "app-config", Namespace: "default"})
	if len(got) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(got))
	}
	if got[0] != ref {
		t.Errorf("expected %v, got %v", ref, got[0])
	}

	// Unrelated configmap should return nothing.
	got = dt.Lookup(configMapGVK, types.NamespacedName{Name: "other", Namespace: "default"})
	if len(got) != 0 {
		t.Fatalf("expected 0 refs for unrelated resource, got %d", len(got))
	}
}

func TestTracker_BroadLookup(t *testing.T) {
	dt := newDependencyTracker()

	ref := reconcilerRef{ReconcilerID: "app->Service", PrimaryKey: types.NamespacedName{Name: "myapp", Namespace: "default"}}
	broadKey := depKeyBroad{GVK: serviceGVK, Namespace: "default"}

	dt.SetDeps(ref, nil, []depKeyBroad{broadKey})

	// Any service in "default" should match.
	got := dt.Lookup(serviceGVK, types.NamespacedName{Name: "whatever", Namespace: "default"})
	if len(got) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(got))
	}

	// Service in different namespace should not match.
	got = dt.Lookup(serviceGVK, types.NamespacedName{Name: "whatever", Namespace: "kube-system"})
	if len(got) != 0 {
		t.Fatalf("expected 0 refs for different namespace, got %d", len(got))
	}
}

func TestTracker_SetDepsReplacesOld(t *testing.T) {
	dt := newDependencyTracker()

	ref := reconcilerRef{ReconcilerID: "app->Deployment", PrimaryKey: types.NamespacedName{Name: "myapp", Namespace: "default"}}
	cmKey1 := depKey{GVK: configMapGVK, NamespacedName: types.NamespacedName{Name: "config-v1", Namespace: "default"}}
	cmKey2 := depKey{GVK: configMapGVK, NamespacedName: types.NamespacedName{Name: "config-v2", Namespace: "default"}}

	dt.SetDeps(ref, []depKey{cmKey1}, nil)

	// config-v1 should trigger.
	got := dt.Lookup(configMapGVK, cmKey1.NamespacedName)
	if len(got) != 1 {
		t.Fatalf("expected 1 ref for config-v1, got %d", len(got))
	}

	// Update deps: now depends on config-v2 instead of config-v1.
	dt.SetDeps(ref, []depKey{cmKey2}, nil)

	// config-v1 should no longer trigger.
	got = dt.Lookup(configMapGVK, cmKey1.NamespacedName)
	if len(got) != 0 {
		t.Fatalf("expected 0 refs for old dep, got %d", len(got))
	}

	// config-v2 should trigger.
	got = dt.Lookup(configMapGVK, cmKey2.NamespacedName)
	if len(got) != 1 {
		t.Fatalf("expected 1 ref for new dep, got %d", len(got))
	}
}

func TestTracker_RemoveDeps(t *testing.T) {
	dt := newDependencyTracker()

	ref := reconcilerRef{ReconcilerID: "app->Deployment", PrimaryKey: types.NamespacedName{Name: "myapp", Namespace: "default"}}
	cmKey := depKey{GVK: configMapGVK, NamespacedName: types.NamespacedName{Name: "app-config", Namespace: "default"}}

	dt.SetDeps(ref, []depKey{cmKey}, nil)
	dt.RemoveDeps(ref)

	got := dt.Lookup(configMapGVK, cmKey.NamespacedName)
	if len(got) != 0 {
		t.Fatalf("expected 0 refs after remove, got %d", len(got))
	}
}

func TestTracker_AppendNarrow(t *testing.T) {
	dt := newDependencyTracker()

	ref := reconcilerRef{ReconcilerID: "app->Deployment", PrimaryKey: types.NamespacedName{Name: "myapp", Namespace: "default"}}
	cmKey := depKey{GVK: configMapGVK, NamespacedName: types.NamespacedName{Name: "app-config", Namespace: "default"}}

	// Initial deps via SetDeps.
	dt.SetDeps(ref, []depKey{cmKey}, nil)

	// Append an output dependency.
	outputKey := depKey{GVK: deployGVK, NamespacedName: types.NamespacedName{Name: "myapp-deploy", Namespace: "default"}}
	dt.AppendNarrow(ref, []depKey{outputKey})

	// Original fetch dep should still resolve.
	got := dt.Lookup(configMapGVK, cmKey.NamespacedName)
	if len(got) != 1 {
		t.Fatalf("expected 1 ref for original dep, got %d", len(got))
	}

	// Appended output dep should also resolve.
	got = dt.Lookup(deployGVK, outputKey.NamespacedName)
	if len(got) != 1 {
		t.Fatalf("expected 1 ref for appended output dep, got %d", len(got))
	}
	if got[0] != ref {
		t.Errorf("expected %v, got %v", ref, got[0])
	}
}

func TestTracker_MultipleReconcilersOnSameDep(t *testing.T) {
	dt := newDependencyTracker()

	ref1 := reconcilerRef{ReconcilerID: "app->Deployment", PrimaryKey: types.NamespacedName{Name: "myapp", Namespace: "default"}}
	ref2 := reconcilerRef{ReconcilerID: "app->Service", PrimaryKey: types.NamespacedName{Name: "myapp", Namespace: "default"}}
	cmKey := depKey{GVK: configMapGVK, NamespacedName: types.NamespacedName{Name: "shared-config", Namespace: "default"}}

	dt.SetDeps(ref1, []depKey{cmKey}, nil)
	dt.SetDeps(ref2, []depKey{cmKey}, nil)

	got := dt.Lookup(configMapGVK, cmKey.NamespacedName)
	if len(got) != 2 {
		t.Fatalf("expected 2 refs, got %d", len(got))
	}
}
