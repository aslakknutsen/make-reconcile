package main

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mr "github.com/aslakknutsen/make-reconcile"
)

// ClonedDeploymentReconciler produces a cloned Deployment for each Session Ref.
//
// This is the part that maps best to make-reconcile. The original controller's
// DeploymentModificator clones the target Deployment with a modified name,
// labels, and image. Here we Fetch the original and return the clone.
//
// The original uses a template engine to transform the Deployment JSON.
// That logic could live inside this function, but the template engine's
// imperative patching (arbitrary JSON transforms) doesn't have a
// make-reconcile equivalent — it's just Go code in the reconciler body.
func ClonedDeploymentReconciler(
	deployments *mr.Collection[*appsv1.Deployment],
) func(*mr.HandlerContext, *Session) []appsv1.Deployment {
	return func(hc *mr.HandlerContext, session *Session) []appsv1.Deployment {
		route := resolveRoute(session)
		var clones []appsv1.Deployment

		for _, ref := range session.Spec.Refs {
			original := mr.Fetch(hc, deployments,
				mr.FilterName(ref.Name, session.Namespace))
			if original == nil {
				continue
			}

			cloneName := original.Name + "-" + session.Name
			version := cloneName

			podLabels := copyLabels(original.Spec.Template.Labels)
			podLabels["version"] = version
			podLabels["ike.session"] = session.Name

			clone := appsv1.Deployment{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      cloneName,
					Namespace: session.Namespace,
					Labels: map[string]string{
						"ike.session": session.Name,
						"ike.ref":     ref.Name,
					},
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: original.Spec.Replicas,
					Selector: &metav1.LabelSelector{
						MatchLabels: podLabels,
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{Labels: podLabels},
						Spec:       *original.Spec.Template.Spec.DeepCopy(),
					},
				},
			}

			if image, ok := ref.Args["image"]; ok && len(clone.Spec.Template.Spec.Containers) > 0 {
				clone.Spec.Template.Spec.Containers[0].Image = image
			}

			_ = route // route is used by VirtualService reconcilers, not Deployment
			clones = append(clones, clone)
		}

		return clones
	}
}

func copyLabels(src map[string]string) map[string]string {
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}
