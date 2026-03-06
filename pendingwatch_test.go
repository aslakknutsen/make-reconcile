package makereconcile

import (
	"context"
	"log/slog"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

func TestRegisterHandlerSucceeds(t *testing.T) {
	s := coreScheme()
	cmGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	fc := newCRDAwareCache(s, nil)

	mgr := &Manager{
		scheme:          s,
		cache:           fc,
		log:             slog.Default(),
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		managerID:       "default",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
	}

	registered, err := mgr.registerHandler(context.Background(), cmGVK)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !registered {
		t.Error("expected handler to be registered")
	}
	if fc.HandlerCount(cmGVK) != 1 {
		t.Errorf("expected 1 handler registered, got %d", fc.HandlerCount(cmGVK))
	}
}

func TestRegisterHandlerNoMatchReturnsFalse(t *testing.T) {
	s := coreScheme()
	cmGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	fc := newCRDAwareCache(s, nil, cmGVK)

	mgr := &Manager{
		scheme:          s,
		cache:           fc,
		log:             slog.Default(),
		watchedGVKs:     make(map[schema.GroupVersionKind]bool),
		watchPredicates: make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:         newDependencyTracker(),
		managerID:       "default",
		lastOutputs:     make(map[string]map[types.NamespacedName]bool),
	}

	registered, err := mgr.registerHandler(context.Background(), cmGVK)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if registered {
		t.Error("expected handler NOT to be registered when CRD is missing")
	}
	if fc.HandlerCount(cmGVK) != 0 {
		t.Errorf("expected 0 handlers, got %d", fc.HandlerCount(cmGVK))
	}
}

func TestStartContinuesWithMissingCRD(t *testing.T) {
	s := coreScheme()
	deployGVK := schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
	fc := newCRDAwareCache(s, nil, deployGVK)

	mgr := &Manager{
		scheme:                    s,
		cache:                     fc,
		client:                    &fakeClient{},
		log:                       slog.Default(),
		watchedGVKs:               make(map[schema.GroupVersionKind]bool),
		watchPredicates:           make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:                   newDependencyTracker(),
		managerID:                 "default",
		lastOutputs:               make(map[string]map[types.NamespacedName]bool),
		resyncPeriod:              time.Hour,
		pendingWatchRetryInterval: time.Hour,
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)
	Reconcile(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- mgr.Start(ctx)
	}()

	// Give Start time to proceed past handler registration and cache sync.
	time.Sleep(100 * time.Millisecond)

	if mgr.PendingWatchCount() != 1 {
		t.Errorf("expected 1 pending watch, got %d", mgr.PendingWatchCount())
	}

	// ConfigMap handler should be registered since its CRD is available.
	cmGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	if fc.HandlerCount(cmGVK) != 1 {
		t.Errorf("expected ConfigMap handler registered, got %d", fc.HandlerCount(cmGVK))
	}

	// Deployment handler should NOT be registered.
	if fc.HandlerCount(deployGVK) != 0 {
		t.Errorf("expected no Deployment handler, got %d", fc.HandlerCount(deployGVK))
	}

	cancel()
	<-errCh
}

func TestRetryPendingWatchesResolves(t *testing.T) {
	s := coreScheme()
	deployGVK := schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
	cmGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}

	fc := newCRDAwareCache(s, map[types.NamespacedName]runtime.Object{
		{Name: "my-cm", Namespace: "default"}: &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "default", UID: "uid-1"},
		},
	}, deployGVK)

	mgr := &Manager{
		scheme:                    s,
		cache:                     fc,
		client:                    &fakeClient{},
		log:                       slog.Default(),
		watchedGVKs:               make(map[schema.GroupVersionKind]bool),
		watchPredicates:           make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:                   newDependencyTracker(),
		managerID:                 "default",
		lastOutputs:               make(map[string]map[types.NamespacedName]bool),
		resyncPeriod:              time.Hour,
		pendingWatchRetryInterval: 50 * time.Millisecond,
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)
	_ = Watch[*appsv1.Deployment](mgr)

	Reconcile(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- mgr.Start(ctx)
	}()

	// Wait for Start to complete initial setup.
	time.Sleep(100 * time.Millisecond)

	if mgr.PendingWatchCount() != 1 {
		t.Fatalf("expected 1 pending watch, got %d", mgr.PendingWatchCount())
	}
	if fc.HandlerCount(deployGVK) != 0 {
		t.Fatalf("Deployment handler should not be registered yet")
	}

	// ConfigMap handler should already be registered.
	if fc.HandlerCount(cmGVK) != 1 {
		t.Fatalf("expected ConfigMap handler, got %d", fc.HandlerCount(cmGVK))
	}

	// "Install" the CRD.
	fc.SetCRDAvailable(deployGVK)

	// Wait for retry to pick it up.
	time.Sleep(200 * time.Millisecond)

	if mgr.PendingWatchCount() != 0 {
		t.Errorf("expected 0 pending watches after CRD available, got %d", mgr.PendingWatchCount())
	}
	if fc.HandlerCount(deployGVK) != 1 {
		t.Errorf("expected Deployment handler registered after CRD available, got %d", fc.HandlerCount(deployGVK))
	}

	cancel()
	<-errCh
}

func TestRetryPendingWatchesStopsWhenAllResolved(t *testing.T) {
	s := coreScheme()
	deployGVK := schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}

	fc := newCRDAwareCache(s, nil, deployGVK)

	mgr := &Manager{
		scheme:                    s,
		cache:                     fc,
		client:                    &fakeClient{},
		log:                       slog.Default(),
		watchedGVKs:               map[schema.GroupVersionKind]bool{deployGVK: true},
		watchPredicates:           make(map[schema.GroupVersionKind][]EventPredicate),
		tracker:                   newDependencyTracker(),
		managerID:                 "default",
		lastOutputs:               make(map[string]map[types.NamespacedName]bool),
		pendingWatchRetryInterval: 20 * time.Millisecond,
		pendingWatches:            []schema.GroupVersionKind{deployGVK},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Make the CRD available immediately.
	fc.SetCRDAvailable(deployGVK)

	done := make(chan struct{})
	go func() {
		mgr.retryPendingWatches(ctx)
		close(done)
	}()

	select {
	case <-done:
		// retryPendingWatches exited because all watches resolved.
	case <-ctx.Done():
		t.Fatal("retryPendingWatches did not exit after all watches resolved")
	}

	if mgr.PendingWatchCount() != 0 {
		t.Errorf("expected 0 pending watches, got %d", mgr.PendingWatchCount())
	}
}

func TestPendingWatchCountZeroByDefault(t *testing.T) {
	mgr := &Manager{}
	if mgr.PendingWatchCount() != 0 {
		t.Errorf("expected 0 pending watches on new manager, got %d", mgr.PendingWatchCount())
	}
}

func TestWithPendingWatchRetryIntervalOption(t *testing.T) {
	mgr := &Manager{}
	opt := WithPendingWatchRetryInterval(42 * time.Second)
	opt(mgr)
	if mgr.pendingWatchRetryInterval != 42*time.Second {
		t.Errorf("expected 42s, got %v", mgr.pendingWatchRetryInterval)
	}
}
