package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"

	mr "github.com/aslakknutsen/make-reconcile"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	AddToScheme(scheme)

	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		kubeconfig = os.Getenv("HOME") + "/.kube/config"
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		log.Error("failed to build kubeconfig", "error", err)
		os.Exit(1)
	}

	mgr, err := mr.NewManager(cfg, scheme, mr.WithLogger(log))
	if err != nil {
		log.Error("failed to create manager", "error", err)
		os.Exit(1)
	}

	// Watch our primary resource and dependencies.
	// WithGenerationChanged filters out status-only updates, preventing
	// the status reconciler from triggering a redundant output reconcile cycle.
	apps := mr.Watch[*MyApp](mgr, mr.WithGenerationChanged())
	configMaps := mr.Watch[*corev1.ConfigMap](mgr)
	deployments := mr.Watch[*appsv1.Deployment](mgr)

	// Sub-reconciler: MyApp -> Deployment
	mr.Reconcile(mgr, apps, func(hc *mr.HandlerContext, app *MyApp) *appsv1.Deployment {
		labels := map[string]string{"app": app.Name}
		containers := []corev1.Container{{
			Name:  "app",
			Image: app.Spec.Image,
			Ports: []corev1.ContainerPort{{ContainerPort: app.Spec.Port}},
		}}

		// If a configRef is set, fetch the ConfigMap and mount it.
		if app.Spec.ConfigRef != "" {
			cm := mr.Fetch(hc, configMaps, mr.FilterName(app.Spec.ConfigRef, app.Namespace))
			if cm != nil {
				containers[0].VolumeMounts = []corev1.VolumeMount{{
					Name:      "config",
					MountPath: "/etc/config",
				}}
				_ = cm // the Fetch call is what matters for dependency tracking
			}
		}

		replicas := app.Spec.Replicas
		return &appsv1.Deployment{
			TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
			ObjectMeta: metav1.ObjectMeta{
				Name:      app.Name,
				Namespace: app.Namespace,
				Labels:    labels,
			},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Selector: &metav1.LabelSelector{MatchLabels: labels},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: labels},
					Spec: corev1.PodSpec{
						Containers: containers,
					},
				},
			},
		}
	})

	// Sub-reconciler: MyApp -> Service
	mr.Reconcile(mgr, apps, func(hc *mr.HandlerContext, app *MyApp) *corev1.Service {
		return &corev1.Service{
			TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
			ObjectMeta: metav1.ObjectMeta{
				Name:      app.Name,
				Namespace: app.Namespace,
			},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": app.Name},
				Ports: []corev1.ServicePort{{
					Port:       app.Spec.Port,
					TargetPort: intstr.FromInt32(app.Spec.Port),
				}},
			},
		}
	})

	// Sub-reconciler: MyApp -> monitoring Deployment (conditional).
	// Returns nil when monitoring is disabled — framework deletes the Deployment.
	mr.Reconcile(mgr, apps, func(hc *mr.HandlerContext, app *MyApp) *appsv1.Deployment {
		if !app.Spec.EnableMonitoring {
			return nil
		}
		labels := map[string]string{"app": app.Name + "-monitor"}
		var one int32 = 1
		return &appsv1.Deployment{
			TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
			ObjectMeta: metav1.ObjectMeta{
				Name:      app.Name + "-monitor",
				Namespace: app.Namespace,
				Labels:    labels,
			},
			Spec: appsv1.DeploymentSpec{
				Replicas: &one,
				Selector: &metav1.LabelSelector{MatchLabels: labels},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: labels},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{
							Name:  "monitor",
							Image: "prom/prometheus:latest",
						}},
					},
				},
			},
		}
	})

	// Status reconciler: computes MyApp.status from the app Deployment's readiness.
	// Runs after output reconcilers. Fetching the Deployment creates a dependency,
	// so when the Deployment's status changes, the status reconciler re-runs.
	mr.ReconcileStatus(mgr, apps, func(hc *mr.HandlerContext, app *MyApp) *MyAppStatus {
		deploy := mr.Fetch(hc, deployments, mr.FilterName(app.Name, app.Namespace))
		if deploy != nil && deploy.Status.ReadyReplicas >= app.Spec.Replicas {
			return &MyAppStatus{Ready: true}
		}
		return &MyAppStatus{Ready: false}
	})

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	log.Info("starting make-reconcile manager")
	if err := mgr.Start(ctx); err != nil {
		log.Error("manager exited with error", "error", err)
		os.Exit(1)
	}
}
