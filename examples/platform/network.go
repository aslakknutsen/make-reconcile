package main

import (
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	mr "github.com/aslakknutsen/make-reconcile"
)

// RegisterServiceAccount registers a sub-reconciler for the shared ServiceAccount.
func RegisterServiceAccount(mgr *mr.Manager, platforms *mr.Collection[*Platform]) {
	mr.Reconcile(mgr, platforms, func(hc *mr.HandlerContext, p *Platform) *corev1.ServiceAccount {
		return &corev1.ServiceAccount{
			TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ServiceAccount"},
			ObjectMeta: metav1.ObjectMeta{Name: p.Name, Namespace: p.Namespace, Labels: componentLabels(p.Name, "serviceaccount")},
		}
	})
}

// RegisterNetworkPolicy registers a sub-reconciler for the NetworkPolicy.
// It restricts ingress to only the app pods on the app port.
func RegisterNetworkPolicy(mgr *mr.Manager, platforms *mr.Collection[*Platform]) {
	mr.Reconcile(mgr, platforms, func(hc *mr.HandlerContext, p *Platform) *networkingv1.NetworkPolicy {
		labels := componentLabels(p.Name, "network")
		appSel := selectorLabels(p.Name, "app")

		return &networkingv1.NetworkPolicy{
			TypeMeta:   metav1.TypeMeta{APIVersion: "networking.k8s.io/v1", Kind: "NetworkPolicy"},
			ObjectMeta: metav1.ObjectMeta{Name: p.Name + "-allow-app", Namespace: p.Namespace, Labels: labels},
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: metav1.LabelSelector{MatchLabels: appSel},
				PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
				Ingress: []networkingv1.NetworkPolicyIngressRule{{
					Ports: []networkingv1.NetworkPolicyPort{{
						Port: intOrStringPtr(p.Spec.App.Port),
					}},
				}},
			},
		}
	})
}

func intOrStringPtr(port int32) *intstr.IntOrString {
	v := intstr.FromInt32(port)
	return &v
}
