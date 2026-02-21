package makereconcile

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestIsNilObject(t *testing.T) {
	// Typed nil.
	var p *testObj
	if !isNilObject(p) {
		t.Error("typed nil pointer should be nil")
	}

	// Untyped nil.
	if !isNilObject(nil) {
		t.Error("untyped nil should be nil")
	}

	// Non-nil.
	obj := &testObj{}
	if isNilObject(obj) {
		t.Error("non-nil should not be nil")
	}
}

func TestGVKFor(t *testing.T) {
	s := testScheme()
	mgr := &Manager{scheme: s}
	gvk := gvkFor[*testObj](mgr)
	expected := schema.GroupVersionKind{Group: "test.io", Version: "v1", Kind: "TestObj"}
	if gvk != expected {
		t.Errorf("expected %v, got %v", expected, gvk)
	}
}
