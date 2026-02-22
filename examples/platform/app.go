package main

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	mr "github.com/aslakknutsen/make-reconcile"
)

// AppDeploymentReconciler returns a handler that produces the app Deployment.
// It Fetches the ConfigMap to include a content hash annotation — when the
// ConfigMap changes, this re-runs and pods roll.
func AppDeploymentReconciler(configMaps *mr.Collection[*corev1.ConfigMap]) func(*mr.HandlerContext, *Platform) *appsv1.Deployment {
	return func(hc *mr.HandlerContext, p *Platform) *appsv1.Deployment {
		labels := componentLabels(p.Name, "app")
		sel := selectorLabels(p.Name, "app")

		cm := mr.Fetch(hc, configMaps, mr.FilterName(p.Name+"-config", p.Namespace))
		hash := configDataHash(cm)

		replicas := p.Spec.App.Replicas
		return &appsv1.Deployment{
			TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
			ObjectMeta: metav1.ObjectMeta{Name: p.Name + "-app", Namespace: p.Namespace, Labels: labels},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Selector: &metav1.LabelSelector{MatchLabels: sel},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels:      sel,
						Annotations: map[string]string{"platform.example.io/config-hash": hash},
					},
					Spec: corev1.PodSpec{
						ServiceAccountName: p.Name,
						Containers: []corev1.Container{{
							Name:  "app",
							Image: p.Spec.App.Image,
							Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: p.Spec.App.Port}},
							EnvFrom: []corev1.EnvFromSource{{
								ConfigMapRef: &corev1.ConfigMapEnvSource{
									LocalObjectReference: corev1.LocalObjectReference{Name: p.Name + "-config"},
								},
							}},
						}},
					},
				},
			},
		}
	}
}

func AppServiceReconciler(hc *mr.HandlerContext, p *Platform) *corev1.Service {
	labels := componentLabels(p.Name, "app")
	sel := selectorLabels(p.Name, "app")

	return &corev1.Service{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
		ObjectMeta: metav1.ObjectMeta{Name: p.Name + "-app", Namespace: p.Namespace, Labels: labels},
		Spec: corev1.ServiceSpec{
			Selector: sel,
			Ports: []corev1.ServicePort{{
				Name:       "http",
				Port:       p.Spec.App.Port,
				TargetPort: intstr.FromString("http"),
			}},
		},
	}
}
