package makereconcile

import (
	"context"
	"log/slog"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
)

func coreScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	return s
}

func TestReconcileRegistration(t *testing.T) {
	s := coreScheme()
	mgr := &Manager{
		scheme:      s,
		watchedGVKs: make(map[schema.GroupVersionKind]bool),
		tracker:     newDependencyTracker(),
		managerID:   "default",
		lastOutputs: make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)
	pods := Watch[*corev1.Pod](mgr)

	_ = configMaps
	_ = pods

	// Verify the GVKs were registered.
	cmGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	podGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"}
	if !mgr.watchedGVKs[cmGVK] {
		t.Error("ConfigMap GVK not registered")
	}
	if !mgr.watchedGVKs[podGVK] {
		t.Error("Pod GVK not registered")
	}
}

func TestReconcileSubReconcilerRegistration(t *testing.T) {
	s := coreScheme()
	mgr := &Manager{
		scheme:      s,
		watchedGVKs: make(map[schema.GroupVersionKind]bool),
		tracker:     newDependencyTracker(),
		reconcilers: nil,
		lastOutputs: make(map[string]map[types.NamespacedName]bool),
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

	if len(mgr.reconcilers) != 1 {
		t.Fatalf("expected 1 reconciler, got %d", len(mgr.reconcilers))
	}

	r := mgr.reconcilers[0]
	if r.ID() != "ConfigMap->Deployment" {
		t.Errorf("unexpected reconciler ID: %s", r.ID())
	}

	deployGVK := schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
	if r.OutputGVK() != deployGVK {
		t.Errorf("unexpected output GVK: %v", r.OutputGVK())
	}
}

func TestSubReconcilerInvocation(t *testing.T) {
	s := coreScheme()
	// We need a fake cache that can serve Get calls.
	fc := &fakeCache{
		objects: map[types.NamespacedName]runtime.Object{
			{Name: "my-cm", Namespace: "default"}: &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-cm",
					Namespace: "default",
					UID:       "cm-uid-1",
				},
				Data: map[string]string{"key": "value"},
			},
		},
	}

	mgr := &Manager{
		scheme:      s,
		cache:       fc,
		watchedGVKs: make(map[schema.GroupVersionKind]bool),
		tracker:     newDependencyTracker(),
		reconcilers: nil,
		lastOutputs: make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)

	var invoked bool
	var gotName string

	Reconcile(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		invoked = true
		gotName = cm.Name
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cm.Name + "-deploy",
				Namespace: cm.Namespace,
			},
		}
	})

	r := mgr.reconcilers[0]
	ctx := context.Background()
	desired, err := r.Reconcile(ctx, mgr, types.NamespacedName{Name: "my-cm", Namespace: "default"})
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}
	if !invoked {
		t.Fatal("sub-reconciler was not invoked")
	}
	if gotName != "my-cm" {
		t.Errorf("expected primary name 'my-cm', got %q", gotName)
	}
	if len(desired) != 1 {
		t.Fatalf("expected 1 desired object, got %d", len(desired))
	}
	if desired[0].GetName() != "my-cm-deploy" {
		t.Errorf("expected output name 'my-cm-deploy', got %q", desired[0].GetName())
	}
}

func TestSubReconcilerNilMeansDelete(t *testing.T) {
	s := coreScheme()
	fc := &fakeCache{
		objects: map[types.NamespacedName]runtime.Object{
			{Name: "my-cm", Namespace: "default"}: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "default"},
			},
		},
	}
	mgr := &Manager{
		scheme:      s,
		cache:       fc,
		watchedGVKs: make(map[schema.GroupVersionKind]bool),
		tracker:     newDependencyTracker(),
		managerID:   "default",
		lastOutputs: make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)

	Reconcile(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		return nil // signal: deployment should not exist
	})

	r := mgr.reconcilers[0]
	desired, err := r.Reconcile(context.Background(), mgr, types.NamespacedName{Name: "my-cm", Namespace: "default"})
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}
	if desired != nil {
		t.Errorf("expected nil desired (delete signal), got %v", desired)
	}
}

func TestFetchTracksDependency(t *testing.T) {
	s := coreScheme()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "db-creds", Namespace: "default"},
		Data:       map[string][]byte{"password": []byte("hunter2")},
	}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "default"},
	}
	fc := &fakeCache{
		objects: map[types.NamespacedName]runtime.Object{
			{Name: "my-cm", Namespace: "default"}:   cm,
			{Name: "db-creds", Namespace: "default"}: secret,
		},
	}
	mgr := &Manager{
		scheme:      s,
		cache:       fc,
		watchedGVKs: make(map[schema.GroupVersionKind]bool),
		tracker:     newDependencyTracker(),
		managerID:   "default",
		lastOutputs: make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)
	secrets := Watch[*corev1.Secret](mgr)

	Reconcile(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		// Fetch a secret — this should create a tracked dependency.
		sec := Fetch(hc, secrets, FilterName("db-creds", "default"))
		_ = sec
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cm.Name + "-deploy",
				Namespace: cm.Namespace,
			},
		}
	})

	r := mgr.reconcilers[0]
	_, err := r.Reconcile(context.Background(), mgr, types.NamespacedName{Name: "my-cm", Namespace: "default"})
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	// The dependency tracker should now know that the "ConfigMap->Deployment" reconciler
	// for primary "my-cm" depends on Secret "db-creds".
	secretGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Secret"}
	refs := mgr.tracker.Lookup(secretGVK, types.NamespacedName{Name: "db-creds", Namespace: "default"})
	if len(refs) != 1 {
		t.Fatalf("expected 1 tracker ref for secret dependency, got %d", len(refs))
	}
	if refs[0].ReconcilerID != "ConfigMap->Deployment" {
		t.Errorf("unexpected reconciler ID in dep: %s", refs[0].ReconcilerID)
	}
	if refs[0].PrimaryKey.Name != "my-cm" {
		t.Errorf("unexpected primary key: %v", refs[0].PrimaryKey)
	}
}

func TestRunSubReconcilerTracksOutputDeps(t *testing.T) {
	s := coreScheme()
	fc := &fakeCache{
		objects: map[types.NamespacedName]runtime.Object{
			{Name: "my-cm", Namespace: "default"}: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-cm",
					Namespace: "default",
					UID:       "cm-uid-1",
				},
			},
		},
	}
	mgr := &Manager{
		scheme:      s,
		cache:       fc,
		client:      &fakeClient{},
		log:         slog.Default(),
		watchedGVKs: make(map[schema.GroupVersionKind]bool),
		tracker:     newDependencyTracker(),
		managerID:   "default",
		lastOutputs: make(map[string]map[types.NamespacedName]bool),
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

	// The output Deployment should now be tracked as a narrow dependency,
	// so an external change to it would trigger re-reconciliation.
	outputGVK := schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
	outputNN := types.NamespacedName{Name: "my-cm-deploy", Namespace: "default"}
	refs := mgr.tracker.Lookup(outputGVK, outputNN)
	if len(refs) != 1 {
		t.Fatalf("expected 1 tracker ref for output dep, got %d", len(refs))
	}
	if refs[0].ReconcilerID != "ConfigMap->Deployment" {
		t.Errorf("unexpected reconciler ID: %s", refs[0].ReconcilerID)
	}
	if refs[0].PrimaryKey != primaryKey {
		t.Errorf("unexpected primary key: %v", refs[0].PrimaryKey)
	}
}

func TestReconcileStatusRegistration(t *testing.T) {
	s := coreScheme()
	mgr := &Manager{
		scheme:      s,
		watchedGVKs: make(map[schema.GroupVersionKind]bool),
		tracker:     newDependencyTracker(),
		managerID:   "default",
		lastOutputs: make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)

	type cmStatus struct {
		Ready bool `json:"ready"`
	}
	ReconcileStatus(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *cmStatus {
		return &cmStatus{Ready: true}
	})

	if len(mgr.statusReconcilers) != 1 {
		t.Fatalf("expected 1 status reconciler, got %d", len(mgr.statusReconcilers))
	}
	sr := mgr.statusReconcilers[0]
	if sr.ID() != "ConfigMap.status" {
		t.Errorf("unexpected status reconciler ID: %s", sr.ID())
	}
	cmGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	if sr.PrimaryGVK() != cmGVK {
		t.Errorf("unexpected primary GVK: %v", sr.PrimaryGVK())
	}
}

func TestReconcileStatusInvocation(t *testing.T) {
	s := coreScheme()
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cm-deploy",
			Namespace: "default",
		},
		Status: appsv1.DeploymentStatus{ReadyReplicas: 3},
	}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "default"},
	}
	fc := &fakeCache{
		objects: map[types.NamespacedName]runtime.Object{
			{Name: "my-cm", Namespace: "default"}:        cm,
			{Name: "my-cm-deploy", Namespace: "default"}: deploy,
		},
	}
	fakeC := &fakeClient{}
	mgr := &Manager{
		scheme:      s,
		cache:       fc,
		client:      fakeC,
		log:         slog.Default(),
		watchedGVKs: make(map[schema.GroupVersionKind]bool),
		tracker:     newDependencyTracker(),
		managerID:   "default",
		lastOutputs: make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)
	deployments := Watch[*appsv1.Deployment](mgr)

	var gotReady int32
	type cmStatus struct {
		ReadyReplicas int32 `json:"readyReplicas"`
	}
	ReconcileStatus(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *cmStatus {
		dep := Fetch(hc, deployments, FilterName(cm.Name+"-deploy", cm.Namespace))
		if dep != nil {
			gotReady = dep.Status.ReadyReplicas
			return &cmStatus{ReadyReplicas: dep.Status.ReadyReplicas}
		}
		return &cmStatus{}
	})

	sr := mgr.statusReconcilers[0]
	err := sr.ReconcileStatus(context.Background(), mgr, types.NamespacedName{Name: "my-cm", Namespace: "default"})
	if err != nil {
		t.Fatalf("status reconcile error: %v", err)
	}
	if gotReady != 3 {
		t.Errorf("expected Fetch to return deploy with 3 ready replicas, got %d", gotReady)
	}
	if fakeC.statusPatches != 1 {
		t.Errorf("expected 1 status patch call, got %d", fakeC.statusPatches)
	}

	// Verify dependency was tracked: changing the deployment should re-trigger.
	deployGVK := schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
	refs := mgr.tracker.Lookup(deployGVK, types.NamespacedName{Name: "my-cm-deploy", Namespace: "default"})
	if len(refs) != 1 {
		t.Fatalf("expected 1 tracker ref for deployment dependency, got %d", len(refs))
	}
	if refs[0].ReconcilerID != "ConfigMap.status" {
		t.Errorf("unexpected reconciler ID: %s", refs[0].ReconcilerID)
	}
}

func TestReconcileStatusNilSkipsWrite(t *testing.T) {
	s := coreScheme()
	fc := &fakeCache{
		objects: map[types.NamespacedName]runtime.Object{
			{Name: "my-cm", Namespace: "default"}: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "default"},
			},
		},
	}
	fakeC := &fakeClient{}
	mgr := &Manager{
		scheme:      s,
		cache:       fc,
		client:      fakeC,
		log:         slog.Default(),
		watchedGVKs: make(map[schema.GroupVersionKind]bool),
		tracker:     newDependencyTracker(),
		managerID:   "default",
		lastOutputs: make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)

	type cmStatus struct {
		Ready bool `json:"ready"`
	}
	ReconcileStatus(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *cmStatus {
		return nil
	})

	sr := mgr.statusReconcilers[0]
	err := sr.ReconcileStatus(context.Background(), mgr, types.NamespacedName{Name: "my-cm", Namespace: "default"})
	if err != nil {
		t.Fatalf("status reconcile error: %v", err)
	}
	if fakeC.statusPatches != 0 {
		t.Errorf("expected 0 status patches for nil return, got %d", fakeC.statusPatches)
	}
}

func TestStatusRunsAfterOutputs(t *testing.T) {
	s := coreScheme()
	fc := &fakeCache{
		objects: map[types.NamespacedName]runtime.Object{
			{Name: "my-cm", Namespace: "default"}: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-cm",
					Namespace: "default",
					UID:       "cm-uid-1",
				},
			},
		},
	}
	fakeC := &fakeClient{}
	mgr := &Manager{
		scheme:      s,
		cache:       fc,
		client:      fakeC,
		log:         slog.Default(),
		watchedGVKs: make(map[schema.GroupVersionKind]bool),
		tracker:     newDependencyTracker(),
		managerID:   "default",
		lastOutputs: make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)

	var order []string

	Reconcile(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		order = append(order, "output")
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cm.Name + "-deploy",
				Namespace: cm.Namespace,
			},
		}
	})

	type cmStatus struct {
		Ready bool `json:"ready"`
	}
	ReconcileStatus(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *cmStatus {
		order = append(order, "status")
		return &cmStatus{Ready: true}
	})

	// Simulate what handle() does: trigger via a ConfigMap event.
	cmGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	handler := &eventHandler{mgr: mgr, gvk: cmGVK}
	handler.handle(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "default"},
	})

	if len(order) < 2 {
		t.Fatalf("expected at least 2 invocations, got %d", len(order))
	}
	if order[0] != "output" {
		t.Errorf("expected output reconciler to run first, got %q", order[0])
	}
	if order[len(order)-1] != "status" {
		t.Errorf("expected status reconciler to run last, got %q", order[len(order)-1])
	}
}

func TestReconcileManyRegistration(t *testing.T) {
	s := coreScheme()
	mgr := &Manager{
		scheme:      s,
		watchedGVKs: make(map[schema.GroupVersionKind]bool),
		tracker:     newDependencyTracker(),
		managerID:   "default",
		lastOutputs: make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)

	ReconcileMany(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) []*corev1.Service {
		return []*corev1.Service{
			{ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-svc-a", Namespace: cm.Namespace}},
			{ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-svc-b", Namespace: cm.Namespace}},
		}
	})

	if len(mgr.reconcilers) != 1 {
		t.Fatalf("expected 1 reconciler, got %d", len(mgr.reconcilers))
	}
	if mgr.reconcilers[0].ID() != "ConfigMap->[]Service" {
		t.Errorf("unexpected ID: %s", mgr.reconcilers[0].ID())
	}

	fc := &fakeCache{
		objects: map[types.NamespacedName]runtime.Object{
			{Name: "my-cm", Namespace: "ns"}: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "ns"},
			},
		},
	}
	mgr.cache = fc

	desired, err := mgr.reconcilers[0].Reconcile(context.Background(), mgr, types.NamespacedName{Name: "my-cm", Namespace: "ns"})
	if err != nil {
		t.Fatal(err)
	}
	if len(desired) != 2 {
		t.Fatalf("expected 2 desired objects, got %d", len(desired))
	}
}

func TestGCOrphansDeletesUnclaimedResources(t *testing.T) {
	s := coreScheme()
	deployGVK := schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}

	fakeC := &fakeClient{
		listObjects: map[schema.GroupVersionKind][]unstructured.Unstructured{
			deployGVK: {
				makeUnstructured(deployGVK, "wanted-deploy", "default", "test-mgr"),
				makeUnstructured(deployGVK, "orphan-deploy", "default", "test-mgr"),
			},
		},
	}

	mgr := &Manager{
		scheme:      s,
		client:      fakeC,
		log:         slog.Default(),
		watchedGVKs: make(map[schema.GroupVersionKind]bool),
		tracker:     newDependencyTracker(),
		managerID:   "test-mgr",
		lastOutputs: map[string]map[types.NamespacedName]bool{
			"ConfigMap->Deployment/default/my-cm": {
				{Name: "wanted-deploy", Namespace: "default"}: true,
			},
		},
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)
	Reconcile(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		return nil
	})

	mgr.gcOrphans(context.Background())

	if len(fakeC.deleted) != 1 {
		t.Fatalf("expected 1 deletion, got %d: %v", len(fakeC.deleted), fakeC.deleted)
	}
	if fakeC.deleted[0].Name != "orphan-deploy" {
		t.Errorf("expected orphan-deploy to be deleted, got %q", fakeC.deleted[0].Name)
	}
}

func TestGCOrphansPreservesClaimedResources(t *testing.T) {
	s := coreScheme()
	deployGVK := schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}

	fakeC := &fakeClient{
		listObjects: map[schema.GroupVersionKind][]unstructured.Unstructured{
			deployGVK: {
				makeUnstructured(deployGVK, "my-deploy", "default", "test-mgr"),
			},
		},
	}

	mgr := &Manager{
		scheme:      s,
		client:      fakeC,
		log:         slog.Default(),
		watchedGVKs: make(map[schema.GroupVersionKind]bool),
		tracker:     newDependencyTracker(),
		managerID:   "test-mgr",
		lastOutputs: map[string]map[types.NamespacedName]bool{
			"ConfigMap->Deployment/default/my-cm": {
				{Name: "my-deploy", Namespace: "default"}: true,
			},
		},
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)
	Reconcile(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		return nil
	})

	mgr.gcOrphans(context.Background())

	if len(fakeC.deleted) != 0 {
		t.Errorf("expected no deletions, got %d: %v", len(fakeC.deleted), fakeC.deleted)
	}
}

func TestGCOrphansManagerIDIsolation(t *testing.T) {
	s := coreScheme()
	deployGVK := schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}

	fakeC := &fakeClient{
		listObjects: map[schema.GroupVersionKind][]unstructured.Unstructured{
			deployGVK: {
				makeUnstructured(deployGVK, "other-deploy", "default", "other-mgr"),
			},
		},
	}

	mgr := &Manager{
		scheme:      s,
		client:      fakeC,
		log:         slog.Default(),
		watchedGVKs: make(map[schema.GroupVersionKind]bool),
		tracker:     newDependencyTracker(),
		managerID:   "my-mgr",
		lastOutputs: make(map[string]map[types.NamespacedName]bool),
	}

	configMaps := Watch[*corev1.ConfigMap](mgr)
	Reconcile(mgr, configMaps, func(hc *HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		return nil
	})

	mgr.gcOrphans(context.Background())

	if len(fakeC.deleted) != 0 {
		t.Errorf("expected no deletions (different manager ID), got %d: %v", len(fakeC.deleted), fakeC.deleted)
	}
}

func makeUnstructured(gvk schema.GroupVersionKind, name, namespace, managerID string) unstructured.Unstructured {
	apiVersion, kind := gvk.ToAPIVersionAndKind()
	return unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": apiVersion,
			"kind":       kind,
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
				"labels": map[string]interface{}{
					managedByLabel: managerID,
				},
			},
		},
	}
}
