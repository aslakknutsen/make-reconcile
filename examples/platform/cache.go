package main

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	mr "github.com/aslakknutsen/make-reconcile"
)

// RegisterCache registers sub-reconcilers for the Redis cache. Both return
// nil when caching is disabled — the framework deletes the resources.
func RegisterCache(mgr *mr.Manager, platforms *mr.Collection[*Platform]) {
	// Cache Deployment
	mr.Reconcile(mgr, platforms, func(hc *mr.HandlerContext, p *Platform) *appsv1.Deployment {
		if !p.Spec.Cache.Enabled {
			return nil
		}

		labels := componentLabels(p.Name, "cache")
		sel := selectorLabels(p.Name, "cache")

		var one int32 = 1
		return &appsv1.Deployment{
			TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
			ObjectMeta: metav1.ObjectMeta{Name: p.Name + "-cache", Namespace: p.Namespace, Labels: labels},
			Spec: appsv1.DeploymentSpec{
				Replicas: &one,
				Selector: &metav1.LabelSelector{MatchLabels: sel},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: sel},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{
							Name:  "redis",
							Image: p.Spec.Cache.Image,
							Ports: []corev1.ContainerPort{{Name: "redis", ContainerPort: p.Spec.Cache.Port}},
						}},
					},
				},
			},
		}
	})

	// Cache Service
	mr.Reconcile(mgr, platforms, func(hc *mr.HandlerContext, p *Platform) *corev1.Service {
		if !p.Spec.Cache.Enabled {
			return nil
		}

		labels := componentLabels(p.Name, "cache")
		sel := selectorLabels(p.Name, "cache")

		return &corev1.Service{
			TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
			ObjectMeta: metav1.ObjectMeta{Name: p.Name + "-cache", Namespace: p.Namespace, Labels: labels},
			Spec: corev1.ServiceSpec{
				Selector: sel,
				Ports: []corev1.ServicePort{{
					Name:       "redis",
					Port:       p.Spec.Cache.Port,
					TargetPort: intstr.FromString("redis"),
				}},
			},
		}
	})
}
