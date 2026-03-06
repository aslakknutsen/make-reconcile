package makereconcile

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

// fakeEventRecorder captures events for test assertions.
type fakeEventRecorder struct {
	mu     sync.Mutex
	events []recordedEvent
}

type recordedEvent struct {
	ObjectName string
	EventType  string
	Reason     string
	Message    string
}

func (f *fakeEventRecorder) Event(object runtime.Object, eventtype string, reason string, message string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	name := ""
	if co, ok := object.(metav1.ObjectMetaAccessor); ok {
		name = co.GetObjectMeta().GetName()
	}
	f.events = append(f.events, recordedEvent{
		ObjectName: name,
		EventType:  eventtype,
		Reason:     reason,
		Message:    message,
	})
}

func (f *fakeEventRecorder) Eventf(object runtime.Object, eventtype string, reason string, messageFmt string, args ...interface{}) {
	f.Event(object, eventtype, reason, fmt.Sprintf(messageFmt, args...))
}

func (f *fakeEventRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventtype string, reason string, messageFmt string, args ...interface{}) {
	f.Event(object, eventtype, reason, fmt.Sprintf(messageFmt, args...))
}

// --- Option A: automatic framework events ---

func TestEventAppliedOnSuccessfulApply(t *testing.T) {
	s := coreScheme()
	rec := &fakeEventRecorder{}
	fc := &fakeCache{
		objects: map[types.NamespacedName]runtime.Object{
			{Name: "my-cm", Namespace: "default"}: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: "my-cm", Namespace: "default", UID: "uid-1",
				},
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
		eventRecorder:   rec,
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)
	Reconcile(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cm.Name + "-deploy",
				Namespace: cm.Namespace,
			},
		}
	})

	primaryKey := types.NamespacedName{Name: "my-cm", Namespace: "default"}
	mgr.runSubReconciler(context.Background(), mgr.reconcilers[0], primaryKey)

	if len(rec.events) != 1 {
		t.Fatalf("expected 1 event, got %d: %+v", len(rec.events), rec.events)
	}
	ev := rec.events[0]
	if ev.EventType != "Normal" {
		t.Errorf("expected Normal event, got %q", ev.EventType)
	}
	if ev.Reason != "Applied" {
		t.Errorf("expected reason Applied, got %q", ev.Reason)
	}
	if ev.ObjectName != "my-cm" {
		t.Errorf("expected event on primary 'my-cm', got %q", ev.ObjectName)
	}
	if !strings.Contains(ev.Message, "Deployment") {
		t.Errorf("expected message to contain output kind, got %q", ev.Message)
	}
}

func TestEventDeletedOnStaleOutputRemoval(t *testing.T) {
	s := coreScheme()
	rec := &fakeEventRecorder{}
	fc := &fakeCache{
		objects: map[types.NamespacedName]runtime.Object{
			{Name: "my-cm", Namespace: "default"}: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: "my-cm", Namespace: "default", UID: "uid-1",
				},
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
		eventRecorder:   rec,
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)
	Reconcile(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		return nil
	})

	primaryKey := types.NamespacedName{Name: "my-cm", Namespace: "default"}
	outputKey := mgr.reconcilers[0].ID() + "/" + primaryKey.String()

	mgr.mu.Lock()
	mgr.lastOutputs[outputKey] = map[types.NamespacedName]bool{
		{Name: "my-cm-deploy", Namespace: "default"}: true,
	}
	mgr.mu.Unlock()

	mgr.runSubReconciler(context.Background(), mgr.reconcilers[0], primaryKey)

	var found bool
	for _, ev := range rec.events {
		if ev.Reason == "Deleted" && ev.EventType == "Normal" {
			found = true
			if ev.ObjectName != "my-cm" {
				t.Errorf("expected event on primary 'my-cm', got %q", ev.ObjectName)
			}
			if !strings.Contains(ev.Message, "Deployment") {
				t.Errorf("expected message to contain output kind, got %q", ev.Message)
			}
		}
	}
	if !found {
		t.Errorf("expected Deleted event, got events: %+v", rec.events)
	}
}

func TestEventReconcileFailedOnStatusReconcilerError(t *testing.T) {
	s := coreScheme()
	rec := &fakeEventRecorder{}
	fc := &fakeCache{
		objects: map[types.NamespacedName]runtime.Object{
			{Name: "my-cm", Namespace: "default"}: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: "my-cm", Namespace: "default", UID: "uid-1",
				},
			},
		},
	}
	fakeC := &fakeClient{statusPatchErr: fmt.Errorf("status patch refused")}
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
		eventRecorder:   rec,
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)

	type cmStatus struct {
		Ready bool `json:"ready"`
	}
	ReconcileStatus(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *cmStatus {
		return &cmStatus{Ready: true}
	})

	primaryKey := types.NamespacedName{Name: "my-cm", Namespace: "default"}
	mgr.runStatusReconciler(context.Background(), mgr.statusReconcilers[0], primaryKey)

	if len(rec.events) != 1 {
		t.Fatalf("expected 1 event, got %d: %+v", len(rec.events), rec.events)
	}
	ev := rec.events[0]
	if ev.EventType != "Warning" {
		t.Errorf("expected Warning event, got %q", ev.EventType)
	}
	if ev.Reason != "ReconcileFailed" {
		t.Errorf("expected reason ReconcileFailed, got %q", ev.Reason)
	}
	if ev.ObjectName != "my-cm" {
		t.Errorf("expected event on 'my-cm', got %q", ev.ObjectName)
	}
}

func TestNoEventsWhenRecorderIsNil(t *testing.T) {
	s := coreScheme()
	fc := &fakeCache{
		objects: map[types.NamespacedName]runtime.Object{
			{Name: "my-cm", Namespace: "default"}: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: "my-cm", Namespace: "default", UID: "uid-1",
				},
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
		// eventRecorder intentionally nil
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)
	Reconcile(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name: cm.Name + "-deploy", Namespace: cm.Namespace,
			},
		}
	})

	primaryKey := types.NamespacedName{Name: "my-cm", Namespace: "default"}
	mgr.runSubReconciler(context.Background(), mgr.reconcilers[0], primaryKey)
	// No panic — that's the assertion.
}

// --- Option B: per-reconciler RecordEvent ---

func TestHandlerContextRecordEvent(t *testing.T) {
	s := coreScheme()
	rec := &fakeEventRecorder{}
	fc := &fakeCache{
		objects: map[types.NamespacedName]runtime.Object{
			{Name: "my-cm", Namespace: "default"}: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: "my-cm", Namespace: "default", UID: "uid-1",
				},
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
		eventRecorder:   rec,
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)
	Reconcile(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		hc.RecordEvent("Warning", "ConfigMapNotFound", "Referenced ConfigMap %s not found", "rules-cm")
		return nil
	})

	r := mgr.reconcilers[0]
	_, err := r.Reconcile(context.Background(), mgr, types.NamespacedName{Name: "my-cm", Namespace: "default"})
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	// Find the user-emitted event (there should be exactly one since the
	// reconciler returned nil so no apply/delete happens).
	if len(rec.events) != 1 {
		t.Fatalf("expected 1 event from RecordEvent, got %d: %+v", len(rec.events), rec.events)
	}
	ev := rec.events[0]
	if ev.EventType != "Warning" {
		t.Errorf("expected Warning, got %q", ev.EventType)
	}
	if ev.Reason != "ConfigMapNotFound" {
		t.Errorf("expected reason ConfigMapNotFound, got %q", ev.Reason)
	}
	if ev.ObjectName != "my-cm" {
		t.Errorf("expected event on primary 'my-cm', got %q", ev.ObjectName)
	}
	if !strings.Contains(ev.Message, "rules-cm") {
		t.Errorf("expected message to contain 'rules-cm', got %q", ev.Message)
	}
}

func TestHandlerContextRecordEventNilRecorder(t *testing.T) {
	s := coreScheme()
	fc := &fakeCache{
		objects: map[types.NamespacedName]runtime.Object{
			{Name: "my-cm", Namespace: "default"}: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: "my-cm", Namespace: "default", UID: "uid-1",
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
		// eventRecorder intentionally nil
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)
	Reconcile(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		hc.RecordEvent("Warning", "SomeReason", "some message")
		return nil
	})

	r := mgr.reconcilers[0]
	_, err := r.Reconcile(context.Background(), mgr, types.NamespacedName{Name: "my-cm", Namespace: "default"})
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}
	// No panic — that's the assertion.
}

func TestRecordEventInStatusReconciler(t *testing.T) {
	s := coreScheme()
	rec := &fakeEventRecorder{}
	fc := &fakeCache{
		objects: map[types.NamespacedName]runtime.Object{
			{Name: "my-cm", Namespace: "default"}: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: "my-cm", Namespace: "default", UID: "uid-1",
				},
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
		eventRecorder:   rec,
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)

	type cmStatus struct {
		Ready bool `json:"ready"`
	}
	ReconcileStatus(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *cmStatus {
		hc.RecordEvent("Normal", "StatusComputed", "Computed status for %s", cm.Name)
		return &cmStatus{Ready: true}
	})

	sr := mgr.statusReconcilers[0]
	err := sr.ReconcileStatus(context.Background(), mgr, types.NamespacedName{Name: "my-cm", Namespace: "default"})
	if err != nil {
		t.Fatalf("status reconcile error: %v", err)
	}

	if len(rec.events) != 1 {
		t.Fatalf("expected 1 event, got %d: %+v", len(rec.events), rec.events)
	}
	if rec.events[0].Reason != "StatusComputed" {
		t.Errorf("expected reason StatusComputed, got %q", rec.events[0].Reason)
	}
	if rec.events[0].ObjectName != "my-cm" {
		t.Errorf("expected event on 'my-cm', got %q", rec.events[0].ObjectName)
	}
}

func TestReconcileFailedEventNotEmittedWhenPrimaryGone(t *testing.T) {
	s := coreScheme()
	rec := &fakeEventRecorder{}
	fc := &fakeCache{
		objects: map[types.NamespacedName]runtime.Object{},
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
		eventRecorder:   rec,
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)
	Reconcile(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		return nil
	})

	// Primary doesn't exist → reconcile fails, getPrimary returns nil → no event.
	mgr.runSubReconciler(context.Background(), mgr.reconcilers[0],
		types.NamespacedName{Name: "gone", Namespace: "default"})

	if len(rec.events) != 0 {
		t.Errorf("expected 0 events when primary is gone (recordEvent is nil-safe), got %d: %+v",
			len(rec.events), rec.events)
	}
}
