package makereconcile_test

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	mr "github.com/aslakknutsen/make-reconcile"
)

func ExampleWatch() {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	mgr := newTestManager(scheme)

	pods := mr.Watch[*corev1.Pod](mgr)
	fmt.Println("Watching:", pods.GVK().Kind)
	// Output: Watching: Pod
}

func ExampleReconcile() {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	mgr := newTestManager(scheme)
	configMaps := mr.Watch[*corev1.ConfigMap](mgr)

	mr.Reconcile(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) *appsv1.Deployment {
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cm.Name + "-deploy",
				Namespace: cm.Namespace,
			},
		}
	})

	fmt.Println("Registered sub-reconciler: ConfigMap -> Deployment")
	// Output: Registered sub-reconciler: ConfigMap -> Deployment
}

func ExampleReconcileMany() {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	mgr := newTestManager(scheme)
	configMaps := mr.Watch[*corev1.ConfigMap](mgr)

	mr.ReconcileMany(mgr, configMaps, func(hc *mr.HandlerContext, cm *corev1.ConfigMap) []*corev1.Service {
		return []*corev1.Service{
			{ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-http"}},
			{ObjectMeta: metav1.ObjectMeta{Name: cm.Name + "-grpc"}},
		}
	})

	fmt.Println("Registered many sub-reconciler: ConfigMap -> []Service")
	// Output: Registered many sub-reconciler: ConfigMap -> []Service
}

// newTestManager creates a Manager suitable for examples (no real cluster).
// This uses unexported fields via the exported constructor's option pattern;
// for examples we just need a manager with a scheme for registration tests.
func newTestManager(scheme *runtime.Scheme) *mr.Manager {
	return mr.NewManagerForTest(scheme)
}
