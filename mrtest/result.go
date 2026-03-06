package mrtest

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Result captures the outputs, deletes, and events from a Reconcile or Settle call.
type Result struct {
	t             *testing.T
	applied       []client.Object
	deleted       []types.NamespacedName
	statusPatches []client.Object
	events        []Event
}

// Applied returns all objects that were applied (SSA-patched) during the reconcile.
func (r *Result) Applied() []client.Object { return r.applied }

// Deleted returns the keys of all objects that were deleted during the reconcile.
func (r *Result) Deleted() []types.NamespacedName { return r.deleted }

// StatusPatches returns all objects whose status was patched.
func (r *Result) StatusPatches() []client.Object { return r.statusPatches }

// Events returns all events recorded during the reconcile.
func (r *Result) Events() []Event { return r.events }

// AssertApplied asserts that an object with the given name and namespace was applied.
func (r *Result) AssertApplied(name, namespace string) {
	r.t.Helper()
	for _, obj := range r.applied {
		if obj.GetName() == name && obj.GetNamespace() == namespace {
			return
		}
	}
	r.t.Errorf("expected object %s/%s to be applied, but it was not", namespace, name)
}

// AssertNotApplied asserts that no object with the given name and namespace was applied.
func (r *Result) AssertNotApplied(name, namespace string) {
	r.t.Helper()
	for _, obj := range r.applied {
		if obj.GetName() == name && obj.GetNamespace() == namespace {
			r.t.Errorf("expected object %s/%s to NOT be applied, but it was", namespace, name)
			return
		}
	}
}

// AssertAppliedCount asserts the total number of applied objects.
func (r *Result) AssertAppliedCount(n int) {
	r.t.Helper()
	if len(r.applied) != n {
		r.t.Errorf("expected %d applied objects, got %d", n, len(r.applied))
	}
}

// AssertDeleted asserts that an object with the given name and namespace was deleted.
func (r *Result) AssertDeleted(name, namespace string) {
	r.t.Helper()
	nn := types.NamespacedName{Name: name, Namespace: namespace}
	for _, d := range r.deleted {
		if d == nn {
			return
		}
	}
	r.t.Errorf("expected object %s/%s to be deleted, but it was not", namespace, name)
}

// AssertDeletedCount asserts the total number of deleted objects.
func (r *Result) AssertDeletedCount(n int) {
	r.t.Helper()
	if len(r.deleted) != n {
		r.t.Errorf("expected %d deleted objects, got %d", n, len(r.deleted))
	}
}

// AssertEvent asserts that an event with the given type and reason was recorded.
func (r *Result) AssertEvent(eventType, reason string) {
	r.t.Helper()
	for _, e := range r.events {
		if e.EventType == eventType && e.Reason == reason {
			return
		}
	}
	r.t.Errorf("expected event %s/%s, got %v", eventType, reason, r.events)
}

// AssertEventContains asserts an event whose message contains the given substring.
func (r *Result) AssertEventContains(eventType, reason, substring string) {
	r.t.Helper()
	for _, e := range r.events {
		if e.EventType == eventType && e.Reason == reason && strings.Contains(e.Message, substring) {
			return
		}
	}
	r.t.Errorf("expected event %s/%s containing %q, got %v", eventType, reason, substring, r.events)
}

// AssertStatusPatched asserts that a status patch was applied for the given name.
func (r *Result) AssertStatusPatched(name, namespace string) {
	r.t.Helper()
	for _, obj := range r.statusPatches {
		if obj.GetName() == name && obj.GetNamespace() == namespace {
			return
		}
	}
	r.t.Errorf("expected status patch for %s/%s, but none found", namespace, name)
}
