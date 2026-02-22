package main

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mr "github.com/aslakknutsen/make-reconcile"
)

// WorkersReconciler produces one Deployment per entry in spec.workers[].
// When a worker is removed from the list, the framework deletes its Deployment.
func WorkersReconciler(hc *mr.HandlerContext, p *Platform) []*appsv1.Deployment {
	if len(p.Spec.Workers) == 0 {
		return nil
	}

	deployments := make([]*appsv1.Deployment, 0, len(p.Spec.Workers))
	for _, w := range p.Spec.Workers {
		labels := componentLabels(p.Name, "worker-"+w.Name)
		sel := selectorLabels(p.Name, "worker-"+w.Name)

		var one int32 = 1
		dep := &appsv1.Deployment{
			TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
			ObjectMeta: metav1.ObjectMeta{Name: p.Name + "-worker-" + w.Name, Namespace: p.Namespace, Labels: labels},
			Spec: appsv1.DeploymentSpec{
				Replicas: &one,
				Selector: &metav1.LabelSelector{MatchLabels: sel},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: sel},
					Spec: corev1.PodSpec{
						ServiceAccountName: p.Name,
						RestartPolicy:      corev1.RestartPolicyAlways,
						Containers: []corev1.Container{{
							Name:    w.Name,
							Image:   w.Image,
							Command: w.Command,
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
		deployments = append(deployments, dep)
	}
	return deployments
}
