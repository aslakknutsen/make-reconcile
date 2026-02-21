package main

import (
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	mr "github.com/aslakknutsen/make-reconcile"
)

// RegisterMonitoring registers sub-reconcilers for the monitoring stack.
// Uses FetchAll to discover all Services in the namespace and build a
// Prometheus scrape config. Returns nil when monitoring is disabled.
func RegisterMonitoring(mgr *mr.Manager, platforms *mr.Collection[*Platform]) {
	services := mr.Watch[*corev1.Service](mgr)

	// Monitoring ConfigMap with auto-discovered scrape targets
	mr.Reconcile(mgr, platforms, func(hc *mr.HandlerContext, p *Platform) *corev1.ConfigMap {
		if !p.Spec.Monitoring.Enabled {
			return nil
		}

		// FetchAll services in our namespace — broad dependency.
		// Any Service change in this namespace re-triggers this reconciler.
		svcs := mr.FetchAll(hc, services, mr.FilterNamespace(p.Namespace))

		var targets []string
		for _, svc := range svcs {
			// Only scrape services belonging to this platform.
			if svc.Labels[LabelName] == p.Name {
				for _, port := range svc.Spec.Ports {
					targets = append(targets, fmt.Sprintf("%s.%s:%d", svc.Name, p.Namespace, port.Port))
				}
			}
		}

		labels := componentLabels(p.Name, "monitoring")
		return &corev1.ConfigMap{
			TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
			ObjectMeta: metav1.ObjectMeta{Name: p.Name + "-prometheus", Namespace: p.Namespace, Labels: labels},
			Data: map[string]string{
				"prometheus.yml": buildPrometheusConfig(targets),
			},
		}
	})

	// Monitoring Deployment
	mr.Reconcile(mgr, platforms, func(hc *mr.HandlerContext, p *Platform) *appsv1.Deployment {
		if !p.Spec.Monitoring.Enabled {
			return nil
		}

		labels := componentLabels(p.Name, "monitoring")
		sel := selectorLabels(p.Name, "monitoring")

		var one int32 = 1
		return &appsv1.Deployment{
			TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
			ObjectMeta: metav1.ObjectMeta{Name: p.Name + "-monitoring", Namespace: p.Namespace, Labels: labels},
			Spec: appsv1.DeploymentSpec{
				Replicas: &one,
				Selector: &metav1.LabelSelector{MatchLabels: sel},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: sel},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{
							Name:  "prometheus",
							Image: p.Spec.Monitoring.Image,
							Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: 9090}},
							VolumeMounts: []corev1.VolumeMount{{
								Name:      "config",
								MountPath: "/etc/prometheus",
							}},
						}},
						Volumes: []corev1.Volume{{
							Name: "config",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{Name: p.Name + "-prometheus"},
								},
							},
						}},
					},
				},
			},
		}
	})

	// Monitoring Service
	mr.Reconcile(mgr, platforms, func(hc *mr.HandlerContext, p *Platform) *corev1.Service {
		if !p.Spec.Monitoring.Enabled {
			return nil
		}

		labels := componentLabels(p.Name, "monitoring")
		sel := selectorLabels(p.Name, "monitoring")

		return &corev1.Service{
			TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
			ObjectMeta: metav1.ObjectMeta{Name: p.Name + "-monitoring", Namespace: p.Namespace, Labels: labels},
			Spec: corev1.ServiceSpec{
				Selector: sel,
				Ports: []corev1.ServicePort{{
					Name:       "http",
					Port:       9090,
					TargetPort: intstr.FromString("http"),
				}},
			},
		}
	})
}

func buildPrometheusConfig(targets []string) string {
	if len(targets) == 0 {
		return "scrape_configs: []\n"
	}
	quoted := make([]string, len(targets))
	for i, t := range targets {
		quoted[i] = fmt.Sprintf("'%s'", t)
	}
	return fmt.Sprintf(`global:
  scrape_interval: 15s
scrape_configs:
  - job_name: platform
    static_configs:
      - targets: [%s]
`, strings.Join(quoted, ", "))
}
