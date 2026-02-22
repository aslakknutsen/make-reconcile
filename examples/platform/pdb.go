package main

import (
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	mr "github.com/aslakknutsen/make-reconcile"
)

// PodDisruptionBudgetReconciler produces a PDB for the app pods.
// Returns nil when autoscaling is disabled — no HPA means no need for a PDB.
func PodDisruptionBudgetReconciler(hc *mr.HandlerContext, p *Platform) *policyv1.PodDisruptionBudget {
	if !p.Spec.Autoscaling.Enabled {
		return nil
	}

	minAvail := intstr.FromInt32(p.Spec.Autoscaling.MinReplicas)
	return &policyv1.PodDisruptionBudget{
		TypeMeta:   metav1.TypeMeta{APIVersion: "policy/v1", Kind: "PodDisruptionBudget"},
		ObjectMeta: metav1.ObjectMeta{Name: p.Name + "-app", Namespace: p.Namespace, Labels: componentLabels(p.Name, "pdb")},
		Spec: policyv1.PodDisruptionBudgetSpec{
			MinAvailable: &minAvail,
			Selector:     &metav1.LabelSelector{MatchLabels: selectorLabels(p.Name, "app")},
		},
	}
}
