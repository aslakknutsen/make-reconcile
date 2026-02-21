package main

import (
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mr "github.com/aslakknutsen/make-reconcile"
)

// RegisterAutoscaling registers a sub-reconciler for the HorizontalPodAutoscaler.
// Returns nil when autoscaling is disabled.
func RegisterAutoscaling(mgr *mr.Manager, platforms *mr.Collection[*Platform]) {
	mr.Reconcile(mgr, platforms, func(hc *mr.HandlerContext, p *Platform) *autoscalingv2.HorizontalPodAutoscaler {
		if !p.Spec.Autoscaling.Enabled {
			return nil
		}

		labels := componentLabels(p.Name, "autoscaling")
		targetCPU := p.Spec.Autoscaling.TargetCPU

		return &autoscalingv2.HorizontalPodAutoscaler{
			TypeMeta:   metav1.TypeMeta{APIVersion: "autoscaling/v2", Kind: "HorizontalPodAutoscaler"},
			ObjectMeta: metav1.ObjectMeta{Name: p.Name + "-app", Namespace: p.Namespace, Labels: labels},
			Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
				ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       p.Name + "-app",
				},
				MinReplicas: &p.Spec.Autoscaling.MinReplicas,
				MaxReplicas: p.Spec.Autoscaling.MaxReplicas,
				Metrics: []autoscalingv2.MetricSpec{{
					Type: autoscalingv2.ResourceMetricSourceType,
					Resource: &autoscalingv2.ResourceMetricSource{
						Name: "cpu",
						Target: autoscalingv2.MetricTarget{
							Type:               autoscalingv2.UtilizationMetricType,
							AverageUtilization: &targetCPU,
						},
					},
				}},
			},
		}
	})
}
