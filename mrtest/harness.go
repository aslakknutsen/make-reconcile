package mrtest

import (
	"context"
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	mr "github.com/aslakknutsen/make-reconcile"
)

// TestHarness wraps a Manager with an in-memory ObjectStore for testing
// reconciler logic without a real cluster. It supports both single-reconciler
// isolation tests and full multi-reconciler integration tests.
type TestHarness struct {
	t        *testing.T
	mgr      *mr.Manager
	store    *ObjectStore
	recorder *EventRecorder
}

// NewHarness creates a TestHarness backed by an in-memory ObjectStore.
// Pass the returned Manager() to Watch/Reconcile/RegisterAll calls.
// Additional ManagerOptions (e.g. WithAnnotationOwnership) are forwarded
// to the underlying Manager.
func NewHarness(t *testing.T, scheme *runtime.Scheme, opts ...mr.ManagerOption) *TestHarness {
	t.Helper()
	store := NewObjectStore(scheme)
	rec := &EventRecorder{}
	mgrOpts := []mr.ManagerOption{
		mr.WithCache(store),
		mr.WithClient(store),
		mr.WithEventRecorder(rec),
	}
	mgrOpts = append(mgrOpts, opts...)
	mgr := mr.NewManagerForTest(scheme, mgrOpts...)
	return &TestHarness{
		t:        t,
		mgr:      mgr,
		store:    store,
		recorder: rec,
	}
}

// Manager returns the underlying Manager for Watch/Reconcile/RegisterAll.
func (h *TestHarness) Manager() *mr.Manager { return h.mgr }

// Store returns the ObjectStore for direct inspection if needed.
func (h *TestHarness) Store() *ObjectStore { return h.store }

// SetObject adds or updates objects in the in-memory store.
func (h *TestHarness) SetObject(objs ...client.Object) {
	h.t.Helper()
	for _, obj := range objs {
		h.store.SetObject(obj)
	}
}

// DeleteObject removes an object from the in-memory store.
func (h *TestHarness) DeleteObject(obj client.Object) {
	h.t.Helper()
	h.store.DeleteObject(obj)
}

// Reconcile triggers reconciliation for a single primary resource identified
// by GVK and key. This runs the same two-phase logic as the real event
// handler: output reconcilers first, then status reconcilers.
func (h *TestHarness) Reconcile(gvk schema.GroupVersionKind, key types.NamespacedName) *Result {
	h.t.Helper()
	h.store.Reset()
	h.recorder.Reset()
	h.mgr.HandleForTest(context.Background(), gvk, key)
	return h.snapshot()
}

// Settle runs a full reconcile over all primaries (output reconcilers first,
// then status reconcilers). Within a single pass, outputs are stored back into
// the ObjectStore so later reconcilers in the same pass can see them via Fetch.
//
// If you need multi-level cascading (reconciler B depends on A's output which
// depends on C), call Settle multiple times.
func (h *TestHarness) Settle() *Result {
	h.t.Helper()
	h.store.Reset()
	h.recorder.Reset()
	h.mgr.FullReconcileForTest(context.Background())
	return h.snapshot()
}

// Events returns all recorded events since the last Reconcile/Settle call.
func (h *TestHarness) Events() []Event {
	return h.recorder.Events()
}

func (h *TestHarness) snapshot() *Result {
	return &Result{
		t:               h.t,
		applied:         copyObjects(h.store.Applied),
		deleted:         copyNNs(h.store.Deleted),
		statusPatches:   copyObjects(h.store.StatusPatches),
		events:          h.recorder.Events(),
		foreignPatched:  copyObjects(h.store.ForeignPatched),
		foreignReverted: copyNNs(h.store.ForeignReverted),
	}
}

func copyObjects(objs []client.Object) []client.Object {
	cp := make([]client.Object, len(objs))
	copy(cp, objs)
	return cp
}

func copyNNs(nns []types.NamespacedName) []types.NamespacedName {
	cp := make([]types.NamespacedName, len(nns))
	copy(cp, nns)
	return cp
}

// GetObject is a typed helper that retrieves an object from the harness store.
func GetObject[T client.Object](h *TestHarness, name, namespace string) T {
	h.t.Helper()
	var zero T
	// Create a non-nil exemplar so the scheme can resolve the GVK.
	typ := reflect.TypeOf(zero)
	if typ.Kind() == reflect.Ptr {
		typ = typ.Elem()
	}
	exemplar := reflect.New(typ).Interface().(client.Object)
	gvks, _, err := h.store.Scheme().ObjectKinds(exemplar)
	if err != nil || len(gvks) == 0 {
		h.t.Fatalf("GetObject: type %T not registered in scheme", zero)
	}
	obj := h.store.GetObject(gvks[0], types.NamespacedName{Name: name, Namespace: namespace})
	if obj == nil {
		return zero
	}
	typed, ok := obj.(T)
	if !ok {
		h.t.Fatalf("GetObject: stored object is %T, wanted %T", obj, zero)
	}
	return typed
}
