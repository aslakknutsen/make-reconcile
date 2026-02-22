package main

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	mr "github.com/aslakknutsen/make-reconcile"
)

func RegisterAll(mgr *mr.Manager) {
	platforms := mr.Watch[*Platform](mgr)
	secrets := mr.Watch[*corev1.Secret](mgr)
	configMaps := mr.Watch[*corev1.ConfigMap](mgr)
	services := mr.Watch[*corev1.Service](mgr)
	deployments := mr.Watch[*appsv1.Deployment](mgr)

	// Infrastructure
	mr.Reconcile(mgr, platforms, ServiceAccountReconciler)
	mr.Reconcile(mgr, platforms, NetworkPolicyReconciler)

	// Config chain: Secret -> ConfigMap -> Deployment
	mr.Reconcile(mgr, platforms, DatabaseSecretReconciler)
	mr.Reconcile(mgr, platforms, ConfigReconciler(secrets))
	mr.Reconcile(mgr, platforms, AppDeploymentReconciler(configMaps))
	mr.Reconcile(mgr, platforms, AppServiceReconciler)

	// Database
	mr.Reconcile(mgr, platforms, DatabaseStatefulSetReconciler)
	mr.Reconcile(mgr, platforms, DatabaseServiceReconciler)

	// Conditional (nil-means-delete)
	mr.Reconcile(mgr, platforms, IngressReconciler(secrets))
	mr.Reconcile(mgr, platforms, AutoscalingReconciler)
	mr.Reconcile(mgr, platforms, PodDisruptionBudgetReconciler)

	// Monitoring (FetchAll: services)
	mr.Reconcile(mgr, platforms, MonitoringConfigMapReconciler(services))
	mr.Reconcile(mgr, platforms, MonitoringDeploymentReconciler)
	mr.Reconcile(mgr, platforms, MonitoringServiceReconciler)

	// Dynamic set
	mr.ReconcileMany(mgr, platforms, WorkersReconciler)

	// Status (runs after all outputs)
	mr.ReconcileStatus(mgr, platforms, StatusReconciler(deployments))
}
