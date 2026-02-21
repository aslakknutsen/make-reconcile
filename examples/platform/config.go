package main

import (
	"crypto/sha256"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mr "github.com/aslakknutsen/make-reconcile"
)

// RegisterConfig registers a sub-reconciler that produces a ConfigMap
// containing application configuration. It Fetches the DB credentials
// Secret to compose a database connection string. When the Secret changes,
// this sub-reconciler re-runs automatically.
func RegisterConfig(mgr *mr.Manager, platforms *mr.Collection[*Platform], secrets *mr.Collection[*corev1.Secret]) {
	mr.Reconcile(mgr, platforms, func(hc *mr.HandlerContext, p *Platform) *corev1.ConfigMap {
		data := map[string]string{
			"APP_PORT": fmt.Sprintf("%d", p.Spec.App.Port),
		}

		// Fetch the DB credentials secret — creates a tracked dependency.
		dbSecretName := p.Name + "-db"
		dbSecret := mr.Fetch(hc, secrets, mr.FilterName(dbSecretName, p.Namespace))
		if dbSecret != nil {
			user := string(dbSecret.Data["username"])
			pass := string(dbSecret.Data["password"])
			data["DATABASE_URL"] = fmt.Sprintf("postgres://%s:%s@%s-db:%d/app", user, pass, p.Name, p.Spec.Database.Port)
		}

		if p.Spec.Cache.Enabled {
			data["CACHE_URL"] = fmt.Sprintf("redis://%s-cache:%d", p.Name, p.Spec.Cache.Port)
		}

		return &corev1.ConfigMap{
			TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
			ObjectMeta: metav1.ObjectMeta{Name: p.Name + "-config", Namespace: p.Namespace, Labels: componentLabels(p.Name, "config")},
			Data:       data,
		}
	})
}

// configDataHash returns a short hash of ConfigMap data, used as a pod
// annotation to trigger rolling updates when config changes.
func configDataHash(cm *corev1.ConfigMap) string {
	if cm == nil {
		return "none"
	}
	h := sha256.New()
	for k, v := range cm.Data {
		fmt.Fprintf(h, "%s=%s;", k, v)
	}
	return fmt.Sprintf("%x", h.Sum(nil))[:12]
}
