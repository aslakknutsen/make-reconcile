package mrtest_test

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	mr "github.com/aslakknutsen/make-reconcile"
	"github.com/aslakknutsen/make-reconcile/mrtest"
)

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	return s
}

var cmGVK = schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}

// --- Single reconciler tests ---

func TestSingleReconcilerProducesOutput(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)
	mr.Reconcile(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		replicas := int32(3)
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-deploy", Namespace: cm.Namespace},
			Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
		}
	})

	h.SetObject(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default"},
	})

	result := h.Reconcile(cmGVK, types.NamespacedName{Name: "app", Namespace: "default"})
	result.AssertAppliedCount(1)
	result.AssertApplied("app-deploy", "default")

	deploy := mrtest.GetObject[*appsv1.Deployment](h, "app-deploy", "default")
	if deploy == nil {
		t.Fatal("expected deployment in store after reconcile")
	}
	if *deploy.Spec.Replicas != 3 {
		t.Errorf("expected 3 replicas, got %d", *deploy.Spec.Replicas)
	}
}

func TestNilReturnDeletesOutput(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)
	mr.Reconcile(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		return nil
	})

	h.SetObject(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default"},
	})

	result := h.Reconcile(cmGVK, types.NamespacedName{Name: "app", Namespace: "default"})
	result.AssertAppliedCount(0)
	result.AssertDeletedCount(0)
}

func TestConditionalOutputNilMeansDelete(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)
	mr.Reconcile(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		if cm.Data["enabled"] == "true" {
			return &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-deploy", Namespace: cm.Namespace},
			}
		}
		return nil
	})

	key := types.NamespacedName{Name: "app", Namespace: "default"}

	h.SetObject(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default"},
		Data:       map[string]string{"enabled": "true"},
	})
	r1 := h.Reconcile(cmGVK, key)
	r1.AssertApplied("app-deploy", "default")

	h.SetObject(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default"},
		Data:       map[string]string{"enabled": "false"},
	})
	r2 := h.Reconcile(cmGVK, key)
	r2.AssertNotApplied("app-deploy", "default")
	r2.AssertDeleted("app-deploy", "default")
}

// --- Fetch + dependency tracking ---

func TestFetchCreatesDependencyAndOutputUsesIt(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)
	secrets := mr.Watch[*corev1.Secret](mgr)

	mr.Reconcile(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		secret := mr.Fetch(hc, secrets, mr.FilterName("db-creds", cm.Namespace))
		image := "default:latest"
		if secret != nil {
			if v, ok := secret.StringData["image"]; ok {
				image = v
			}
		}
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-deploy", Namespace: cm.Namespace},
			Spec: appsv1.DeploymentSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{Name: "app", Image: image}},
					},
				},
			},
		}
	})

	h.SetObject(
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default"}},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "db-creds", Namespace: "default"},
			StringData: map[string]string{"image": "myapp:v2"},
		},
	)

	result := h.Reconcile(cmGVK, types.NamespacedName{Name: "app", Namespace: "default"})
	result.AssertApplied("app-deploy", "default")

	deploy := mrtest.GetObject[*appsv1.Deployment](h, "app-deploy", "default")
	if deploy == nil {
		t.Fatal("expected deployment")
	}
	if deploy.Spec.Template.Spec.Containers[0].Image != "myapp:v2" {
		t.Errorf("expected image myapp:v2, got %s", deploy.Spec.Template.Spec.Containers[0].Image)
	}
}

func TestFetchMissingDependencyReturnsZero(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)
	secrets := mr.Watch[*corev1.Secret](mgr)

	var fetchResult *corev1.Secret
	mr.Reconcile(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		fetchResult = mr.Fetch(hc, secrets, mr.FilterName("nonexistent", cm.Namespace))
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-deploy", Namespace: cm.Namespace},
		}
	})

	h.SetObject(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default"}})
	h.Reconcile(cmGVK, types.NamespacedName{Name: "app", Namespace: "default"})

	if fetchResult != nil {
		t.Error("expected nil from Fetch for nonexistent secret")
	}
}

// --- ReconcileMany ---

func TestReconcileManyProducesMultipleOutputs(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)
	mr.ReconcileMany(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) []*corev1.Service {
		return []*corev1.Service{
			{ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-http", Namespace: cm.Namespace}},
			{ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-grpc", Namespace: cm.Namespace}},
		}
	})

	h.SetObject(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default"}})

	result := h.Reconcile(cmGVK, types.NamespacedName{Name: "app", Namespace: "default"})
	result.AssertAppliedCount(2)
	result.AssertApplied("app-http", "default")
	result.AssertApplied("app-grpc", "default")
}

// --- ReconcileStatus ---

func TestStatusReconcilerRunsAndPatches(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)
	deployments := mr.Watch[*appsv1.Deployment](mgr)

	mr.Reconcile(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		replicas := int32(1)
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-deploy", Namespace: cm.Namespace},
			Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
		}
	})

	type appStatus struct {
		Ready bool `json:"ready"`
	}
	mr.ReconcileStatus(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *appStatus {
		dep := mr.Fetch(hc, deployments, mr.FilterName(cm.Name+"-deploy", cm.Namespace))
		if dep != nil && dep.Status.ReadyReplicas > 0 {
			return &appStatus{Ready: true}
		}
		return &appStatus{Ready: false}
	})

	h.SetObject(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default"},
	})

	result := h.Reconcile(cmGVK, types.NamespacedName{Name: "app", Namespace: "default"})
	result.AssertApplied("app-deploy", "default")
	result.AssertStatusPatched("app", "default")
}

func TestStatusReconcilerNilSkipsPatch(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)

	type appStatus struct {
		Ready bool `json:"ready"`
	}
	mr.ReconcileStatus(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *appStatus {
		return nil
	})

	h.SetObject(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default"}})

	result := h.Reconcile(cmGVK, types.NamespacedName{Name: "app", Namespace: "default"})
	if len(result.StatusPatches()) != 0 {
		t.Errorf("expected no status patches for nil return, got %d", len(result.StatusPatches()))
	}
}

// --- WithPredicate ---

func TestPredicateSkipsNonMatchingPrimary(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)

	var invoked bool
	mr.Reconcile(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		invoked = true
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-deploy", Namespace: cm.Namespace},
		}
	}, mr.WithPredicate(func(cm *corev1.ConfigMap) bool {
		return cm.Labels["role"] == "primary"
	}))

	h.SetObject(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "app", Namespace: "default",
			Labels: map[string]string{"role": "secondary"},
		},
	})

	result := h.Reconcile(cmGVK, types.NamespacedName{Name: "app", Namespace: "default"})
	result.AssertAppliedCount(0)
	if invoked {
		t.Error("reconciler should not have been invoked for non-matching predicate")
	}
}

func TestPredicatePassesMatchingPrimary(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)
	mr.Reconcile(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-deploy", Namespace: cm.Namespace},
		}
	}, mr.WithPredicate(func(cm *corev1.ConfigMap) bool {
		return cm.Labels["role"] == "primary"
	}))

	h.SetObject(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "app", Namespace: "default",
			Labels: map[string]string{"role": "primary"},
		},
	})

	result := h.Reconcile(cmGVK, types.NamespacedName{Name: "app", Namespace: "default"})
	result.AssertApplied("app-deploy", "default")
}

// --- Events ---

func TestEventsRecordedDuringReconcile(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)
	mr.Reconcile(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-deploy", Namespace: cm.Namespace},
		}
	})

	h.SetObject(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default", UID: "uid-1"},
	})

	result := h.Reconcile(cmGVK, types.NamespacedName{Name: "app", Namespace: "default"})
	result.AssertEvent("Normal", "Applied")
}

func TestCustomRecordEvent(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)
	mr.Reconcile(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		hc.RecordEvent("Warning", "MissingConfig", "Config %s not found", "db-config")
		return nil
	})

	h.SetObject(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default"},
	})

	result := h.Reconcile(cmGVK, types.NamespacedName{Name: "app", Namespace: "default"})
	result.AssertEventContains("Warning", "MissingConfig", "db-config")
}

// --- Multi-reconciler registration (RegisterAll pattern) ---

func TestMultiReconcilerRegistration(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr, mr.WithGenerationChanged())
	secrets := mr.Watch[*corev1.Secret](mgr)
	deployments := mr.Watch[*appsv1.Deployment](mgr)

	// Reconciler 1: ConfigMap -> Deployment
	mr.Reconcile(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		secret := mr.Fetch(hc, secrets, mr.FilterName(cm.Name+"-secret", cm.Namespace))
		image := "default:latest"
		if secret != nil {
			if v, ok := secret.StringData["image"]; ok {
				image = v
			}
		}
		replicas := int32(1)
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: cm.Name, Namespace: cm.Namespace},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{Name: "app", Image: image}},
					},
				},
			},
		}
	})

	// Reconciler 2: ConfigMap -> Service
	mr.Reconcile(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *corev1.Service {
		return &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: cm.Name, Namespace: cm.Namespace},
		}
	})

	// Status reconciler
	type appStatus struct {
		Ready bool `json:"ready"`
	}
	mr.ReconcileStatus(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *appStatus {
		deploy := mr.Fetch(hc, deployments, mr.FilterName(cm.Name, cm.Namespace))
		if deploy != nil && deploy.Status.ReadyReplicas > 0 {
			return &appStatus{Ready: true}
		}
		return &appStatus{Ready: false}
	})

	// Seed the world
	h.SetObject(
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "myapp", Namespace: "default"}},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "myapp-secret", Namespace: "default"},
			StringData: map[string]string{"image": "myapp:v3"},
		},
	)

	result := h.Reconcile(cmGVK, types.NamespacedName{Name: "myapp", Namespace: "default"})

	result.AssertApplied("myapp", "default")
	if len(result.Applied()) < 2 {
		t.Fatalf("expected at least 2 applied objects (deploy + service), got %d", len(result.Applied()))
	}

	deploy := mrtest.GetObject[*appsv1.Deployment](h, "myapp", "default")
	if deploy == nil {
		t.Fatal("expected deployment in store")
	}
	if deploy.Spec.Template.Spec.Containers[0].Image != "myapp:v3" {
		t.Errorf("expected image from secret, got %s", deploy.Spec.Template.Spec.Containers[0].Image)
	}

	svc := mrtest.GetObject[*corev1.Service](h, "myapp", "default")
	if svc == nil {
		t.Fatal("expected service in store")
	}

	result.AssertStatusPatched("myapp", "default")
}

// --- Settle ---

func TestSettleConvergesMultiplePrimaries(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)
	mr.Reconcile(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-deploy", Namespace: cm.Namespace},
		}
	})

	h.SetObject(
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "app-a", Namespace: "default"}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "app-b", Namespace: "default"}},
	)

	result := h.Settle()

	result.AssertApplied("app-a-deploy", "default")
	result.AssertApplied("app-b-deploy", "default")
}

func TestSettleRunsStatusAfterOutputs(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)

	var order []string
	mr.Reconcile(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		order = append(order, "output:"+cm.Name)
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-deploy", Namespace: cm.Namespace},
		}
	})

	type status struct {
		Done bool `json:"done"`
	}
	mr.ReconcileStatus(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *status {
		order = append(order, "status:"+cm.Name)
		return &status{Done: true}
	})

	h.SetObject(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default"}})

	h.Settle()

	if len(order) < 2 {
		t.Fatalf("expected at least 2 invocations, got %d: %v", len(order), order)
	}

	outputIdx := -1
	statusIdx := -1
	for i, v := range order {
		if v == "output:app" && outputIdx == -1 {
			outputIdx = i
		}
		if v == "status:app" {
			statusIdx = i
		}
	}
	if outputIdx == -1 || statusIdx == -1 {
		t.Fatalf("missing expected invocations in order: %v", order)
	}
	if statusIdx <= outputIdx {
		t.Errorf("status should run after output, got order: %v", order)
	}
}

// --- Mutation after reconcile ---

func TestMutateAndRereconcile(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)
	mr.Reconcile(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		replicas := int32(1)
		if v, ok := cm.Data["replicas"]; ok && v == "5" {
			replicas = 5
		}
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-deploy", Namespace: cm.Namespace},
			Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
		}
	})

	key := types.NamespacedName{Name: "app", Namespace: "default"}

	h.SetObject(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default"},
		Data:       map[string]string{"replicas": "1"},
	})

	h.Reconcile(cmGVK, key)
	deploy := mrtest.GetObject[*appsv1.Deployment](h, "app-deploy", "default")
	if deploy == nil || *deploy.Spec.Replicas != 1 {
		t.Fatal("expected 1 replica initially")
	}

	h.SetObject(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default"},
		Data:       map[string]string{"replicas": "5"},
	})

	h.Reconcile(cmGVK, key)
	deploy = mrtest.GetObject[*appsv1.Deployment](h, "app-deploy", "default")
	if deploy == nil || *deploy.Spec.Replicas != 5 {
		t.Errorf("expected 5 replicas after mutation, got %d", *deploy.Spec.Replicas)
	}
}

// --- GetObject returns nil for missing ---

func TestGetObjectReturnsNilForMissing(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme())
	deploy := mrtest.GetObject[*appsv1.Deployment](h, "nonexistent", "default")
	if deploy != nil {
		t.Error("expected nil for nonexistent object")
	}
}

// --- Multiple reconcilers for same primary->output GVK ---

func TestMultipleReconcilersSameOutputGVK(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)

	mr.Reconcile(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-app", Namespace: cm.Namespace},
		}
	})

	mr.Reconcile(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-worker", Namespace: cm.Namespace},
		}
	})

	h.SetObject(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "default"}})

	result := h.Reconcile(cmGVK, types.NamespacedName{Name: "svc", Namespace: "default"})
	result.AssertAppliedCount(2)
	result.AssertApplied("svc-app", "default")
	result.AssertApplied("svc-worker", "default")
}

// --- OnDelete / finalizer tests ---

func TestOnDeleteFinalizerAddedOnReconcile(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)
	mr.OnDelete(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) error {
		return nil
	})

	mr.Reconcile(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-deploy", Namespace: cm.Namespace},
		}
	})

	h.SetObject(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default", UID: "uid-1"},
	})

	result := h.Reconcile(cmGVK, types.NamespacedName{Name: "app", Namespace: "default"})
	result.AssertApplied("app-deploy", "default")

	// Verify the finalizer was added to the primary in the store.
	cm := mrtest.GetObject[*corev1.ConfigMap](h, "app", "default")
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
		t.Errorf("expected finalizer on ConfigMap, got finalizers: %v", cm.GetFinalizers())
	}
}

func TestOnDeleteCleanupRunsOnDeletion(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)

	var cleanupCalled bool
	mr.OnDelete(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) error {
		cleanupCalled = true
		return nil
	})

	mr.Reconcile(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-deploy", Namespace: cm.Namespace},
		}
	})

	// First reconcile: adds finalizer.
	h.SetObject(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default", UID: "uid-1"},
	})
	h.Reconcile(cmGVK, types.NamespacedName{Name: "app", Namespace: "default"})

	// Simulate deletion: set DeletionTimestamp and keep the finalizer.
	now := metav1.Now()
	h.SetObject(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "app", Namespace: "default", UID: "uid-1",
			DeletionTimestamp: &now,
			Finalizers:        []string{"make-reconcile.io/finalizer-default"},
		},
	})
	result := h.Reconcile(cmGVK, types.NamespacedName{Name: "app", Namespace: "default"})

	if !cleanupCalled {
		t.Error("expected cleanup to be called on deletion")
	}

	// Output should not have been applied (cleanup path, not normal reconcile).
	result.AssertAppliedCount(0)

	// Finalizer should have been removed.
	cm := mrtest.GetObject[*corev1.ConfigMap](h, "app", "default")
	if cm != nil {
		for _, f := range cm.GetFinalizers() {
			if f == "make-reconcile.io/finalizer-default" {
				t.Error("finalizer should have been removed after cleanup")
			}
		}
	}
}

func TestOnDeleteCleanupCanFetchResources(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)
	secrets := mr.Watch[*corev1.Secret](mgr)

	var fetchedSecretName string
	mr.OnDelete(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) error {
		sec := mr.Fetch(hc, secrets, mr.FilterName(cm.Name+"-secret", cm.Namespace))
		if sec != nil {
			fetchedSecretName = sec.Name
		}
		return nil
	})

	mr.Reconcile(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		return nil
	})

	now := metav1.Now()
	h.SetObject(
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: "app", Namespace: "default", UID: "uid-1",
				DeletionTimestamp: &now,
				Finalizers:        []string{"make-reconcile.io/finalizer-default"},
			},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "app-secret", Namespace: "default"},
		},
	)

	h.Reconcile(cmGVK, types.NamespacedName{Name: "app", Namespace: "default"})

	if fetchedSecretName != "app-secret" {
		t.Errorf("expected cleanup to fetch 'app-secret', got %q", fetchedSecretName)
	}
}

func TestOnDeleteStatusSkippedDuringDeletion(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)

	mr.OnDelete(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) error {
		return nil
	})

	mr.Reconcile(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		return nil
	})

	type status struct {
		Done bool `json:"done"`
	}
	var statusInvoked bool
	mr.ReconcileStatus(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *status {
		statusInvoked = true
		return &status{Done: true}
	})

	now := metav1.Now()
	h.SetObject(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "app", Namespace: "default", UID: "uid-1",
			DeletionTimestamp: &now,
			Finalizers:        []string{"make-reconcile.io/finalizer-default"},
		},
	})

	h.Reconcile(cmGVK, types.NamespacedName{Name: "app", Namespace: "default"})

	if statusInvoked {
		t.Error("status reconciler should not run during deletion")
	}
}

func TestSettleHandlesDeletingPrimaries(t *testing.T) {
	h := mrtest.NewHarness(t, testScheme())
	mgr := h.Manager()

	configMaps := mr.Watch[*corev1.ConfigMap](mgr)

	var cleanupNames []string
	var reconcileNames []string

	mr.OnDelete(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) error {
		cleanupNames = append(cleanupNames, cm.Name)
		return nil
	})

	mr.Reconcile(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		reconcileNames = append(reconcileNames, cm.Name)
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-deploy", Namespace: cm.Namespace},
		}
	})

	now := metav1.Now()
	h.SetObject(
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: "alive", Namespace: "default", UID: "uid-1",
			},
		},
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: "dying", Namespace: "default", UID: "uid-2",
				DeletionTimestamp: &now,
				Finalizers:        []string{"make-reconcile.io/finalizer-default"},
			},
		},
	)

	h.Settle()

	if len(cleanupNames) != 1 || cleanupNames[0] != "dying" {
		t.Errorf("expected cleanup for 'dying' only, got %v", cleanupNames)
	}

	foundAlive := false
	for _, n := range reconcileNames {
		if n == "alive" {
			foundAlive = true
		}
		if n == "dying" {
			t.Error("'dying' should not have been reconciled normally")
		}
	}
	if !foundAlive {
		t.Error("expected 'alive' to be reconciled normally")
	}
}
