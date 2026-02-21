package main

import (
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mr "github.com/aslakknutsen/make-reconcile"
)

// RegisterIngress registers a sub-reconciler for the Ingress resource.
// Returns nil when ingress is disabled. Fetches the TLS Secret so the
// Ingress is re-reconciled if the certificate rotates.
func RegisterIngress(mgr *mr.Manager, platforms *mr.Collection[*Platform], secrets *mr.Collection[*corev1.Secret]) {
	mr.Reconcile(mgr, platforms, func(hc *mr.HandlerContext, p *Platform) *networkingv1.Ingress {
		if !p.Spec.Ingress.Enabled {
			return nil
		}

		labels := componentLabels(p.Name, "ingress")

		// Fetch the TLS secret so we re-reconcile when the cert changes.
		if p.Spec.Ingress.TLSSecretRef != "" {
			_ = mr.Fetch(hc, secrets, mr.FilterName(p.Spec.Ingress.TLSSecretRef, p.Namespace))
		}

		pathType := networkingv1.PathTypePrefix
		ing := &networkingv1.Ingress{
			TypeMeta:   metav1.TypeMeta{APIVersion: "networking.k8s.io/v1", Kind: "Ingress"},
			ObjectMeta: metav1.ObjectMeta{Name: p.Name, Namespace: p.Namespace, Labels: labels},
			Spec: networkingv1.IngressSpec{
				Rules: []networkingv1.IngressRule{{
					Host: p.Spec.Ingress.Host,
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{{
								Path:     "/",
								PathType: &pathType,
								Backend: networkingv1.IngressBackend{
									Service: &networkingv1.IngressServiceBackend{
										Name: p.Name + "-app",
										Port: networkingv1.ServiceBackendPort{Name: "http"},
									},
								},
							}},
						},
					},
				}},
			},
		}

		if p.Spec.Ingress.TLSSecretRef != "" {
			ing.Spec.TLS = []networkingv1.IngressTLS{{
				Hosts:      []string{p.Spec.Ingress.Host},
				SecretName: p.Spec.Ingress.TLSSecretRef,
			}}
		}

		return ing
	})
}
