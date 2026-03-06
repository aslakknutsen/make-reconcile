package makereconcile

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

// --- Option A: automatic framework events ---

func TestEventAppliedOnSuccessfulApply(t *testing.T) {
	s := coreScheme()
	rec := &internalEventRecorder{}
	store := newTestStore(s, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "default", UID: "uid-1"},
	})
	mgr := &Manager{
		scheme:          s,
		cache:           store,
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

	events := rec.events
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d: %+v", len(events), events)
	}
	ev := events[0]
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
	rec := &internalEventRecorder{}
	store := newTestStore(s, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "default", UID: "uid-1"},
	})
	mgr := &Manager{
		scheme:          s,
		cache:           store,
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
	rec := &internalEventRecorder{}
	store := newTestStore(s, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "default", UID: "uid-1"},
	})
	fakeC := &fakeClient{statusPatchErr: fmt.Errorf("status patch refused")}
	mgr := &Manager{
		scheme:          s,
		cache:           store,
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

	events := rec.events
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d: %+v", len(events), events)
	}
	ev := events[0]
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
	store := newTestStore(s, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "default", UID: "uid-1"},
	})
	mgr := &Manager{
		scheme:          s,
		cache:           store,
		client:          &fakeClient{},
		log:             slog.Default(),
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		managerID:       "default",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
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
}

// --- Option B: per-reconciler RecordEvent ---

func TestHandlerContextRecordEvent(t *testing.T) {
	s := coreScheme()
	rec := &internalEventRecorder{}
	store := newTestStore(s, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "default", UID: "uid-1"},
	})
	mgr := &Manager{
		scheme:          s,
		cache:           store,
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

	events := rec.events
	if len(events) != 1 {
		t.Fatalf("expected 1 event from RecordEvent, got %d: %+v", len(events), events)
	}
	ev := events[0]
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
	store := newTestStore(s, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "default", UID: "uid-1"},
	})
	mgr := &Manager{
		scheme:          s,
		cache:           store,
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		managerID:       "default",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
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
}

func TestRecordEventInStatusReconciler(t *testing.T) {
	s := coreScheme()
	rec := &internalEventRecorder{}
	store := newTestStore(s, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "default", UID: "uid-1"},
	})
	mgr := &Manager{
		scheme:          s,
		cache:           store,
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

	events := rec.events
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d: %+v", len(events), events)
	}
	if events[0].Reason != "StatusComputed" {
		t.Errorf("expected reason StatusComputed, got %q", events[0].Reason)
	}
	if events[0].ObjectName != "my-cm" {
		t.Errorf("expected event on 'my-cm', got %q", events[0].ObjectName)
	}
}

func TestReconcileFailedEventNotEmittedWhenPrimaryGone(t *testing.T) {
	s := coreScheme()
	rec := &internalEventRecorder{}
	store := newTestStore(s)
	mgr := &Manager{
		scheme:          s,
		cache:           store,
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

	mgr.runSubReconciler(context.Background(), mgr.reconcilers[0],
		types.NamespacedName{Name: "gone", Namespace: "default"})

	events := rec.events
	if len(events) != 0 {
		t.Errorf("expected 0 events when primary is gone, got %d: %+v", len(events), events)
	}
}
