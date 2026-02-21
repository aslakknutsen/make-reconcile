package main

import (
	corev1 "k8s.io/api/core/v1"

	mr "github.com/aslakknutsen/make-reconcile"
)

// RegisterAll creates all Watch collections and wires every component's
// sub-reconcilers to the manager. Adding a new component is one file
// plus one line here.
func RegisterAll(mgr *mr.Manager) {
	platforms := mr.Watch[*Platform](mgr)
	secrets := mr.Watch[*corev1.Secret](mgr)
	configMaps := mr.Watch[*corev1.ConfigMap](mgr)

	RegisterServiceAccount(mgr, platforms)
	RegisterConfig(mgr, platforms, secrets)
	RegisterApp(mgr, platforms, configMaps)
	RegisterDatabase(mgr, platforms)
	RegisterCache(mgr, platforms)
	RegisterIngress(mgr, platforms, secrets)
	RegisterAutoscaling(mgr, platforms)
	RegisterMonitoring(mgr, platforms)
	RegisterNetworkPolicy(mgr, platforms)
	RegisterWorkers(mgr, platforms)
}
