package makereconcile

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

func TestContributorKeyFormat(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Session"}
	nn := types.NamespacedName{Name: "session-a", Namespace: "default"}
	got := contributorKey(gvk, nn)
	want := "Session/default/session-a"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestGetSetContributors(t *testing.T) {
	obj := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "shared", Namespace: "default"},
	}

	// Empty initially.
	if c := getContributors(obj); len(c) != 0 {
		t.Fatalf("expected empty contributors, got %v", c)
	}

	// Set some.
	setContributors(obj, []string{"Session/default/a", "Session/default/b"})
	got := getContributors(obj)
	if len(got) != 2 || got[0] != "Session/default/a" || got[1] != "Session/default/b" {
		t.Errorf("unexpected contributors: %v", got)
	}

	// Clear.
	setContributors(obj, nil)
	if c := getContributors(obj); len(c) != 0 {
		t.Errorf("expected empty after clear, got %v", c)
	}

	ann := obj.GetAnnotations()
	if _, ok := ann[contributorAnnotation]; ok {
		t.Error("expected annotation to be removed when contributors are empty")
	}
}

func TestOwnerRefStrategyDefaults(t *testing.T) {
	s := &ownerRefStrategy{}
	if s.needsFinalizer() {
		t.Error("ownerRefStrategy should not need finalizer")
	}

	del, err := s.releaseOwnership(nil, nil, nil, schema.GroupVersionKind{}, types.NamespacedName{}, schema.GroupVersionKind{}, types.NamespacedName{}, "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !del {
		t.Error("ownerRefStrategy should always return shouldDelete=true")
	}
}

func TestAnnotationStrategyNeedsFinalizer(t *testing.T) {
	s := &annotationStrategy{}
	if !s.needsFinalizer() {
		t.Error("annotationStrategy should need finalizer")
	}
}

func TestGetOwnershipDefaultsToOwnerRef(t *testing.T) {
	m := &Manager{}
	o := m.getOwnership()
	if _, ok := o.(*ownerRefStrategy); !ok {
		t.Errorf("expected ownerRefStrategy default, got %T", o)
	}
}

func TestNeedsFinalizer(t *testing.T) {
	cmGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}

	// Default strategy, no cleanup → no finalizer.
	m := &Manager{managerID: "test"}
	if m.needsFinalizer(cmGVK) {
		t.Error("should not need finalizer without cleanup or annotation strategy")
	}

	// Default strategy with cleanup → needs finalizer.
	m.cleanupFns = map[schema.GroupVersionKind]cleanupEntry{
		cmGVK: {},
	}
	if !m.needsFinalizer(cmGVK) {
		t.Error("should need finalizer with cleanup registered")
	}

	// Annotation strategy, no cleanup → needs finalizer.
	m2 := &Manager{
		managerID: "test",
		ownership: &annotationStrategy{},
	}
	if !m2.needsFinalizer(cmGVK) {
		t.Error("annotation strategy should always need finalizer")
	}
}
