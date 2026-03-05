package main

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mr "github.com/aslakknutsen/make-reconcile"
)

// StatusReconciler computes GatewayClass status conditions by reading the
// Istio CR's status. Runs after the Istio output reconciler.
//
// The Fetch on the Istio collection creates a dependency: when the Istio
// CR's status changes (e.g., istiod becomes ready), this reconciler re-runs
// and the GatewayClass conditions are updated automatically.
//
// Corresponds to mapStatusToConditions in the original controller.
// The original embeds this inside the main Reconcile method. Here it's
// a separate concern that only runs when status-relevant data changes.
func StatusReconciler(istios *mr.Collection[*Istio]) func(*mr.HandlerContext, *GatewayClass) *GatewayClassStatus {
	return func(hc *mr.HandlerContext, gc *GatewayClass) *GatewayClassStatus {
		istio := mr.Fetch(hc, istios, mr.FilterName(defaultIstioName, defaultIstioNamespace))

		conditions := []metav1.Condition{
			controllerInstalledCondition(istio, gc.Generation),
		}

		return &GatewayClassStatus{
			Conditions: conditions,
		}
	}
}

func controllerInstalledCondition(istio *Istio, generation int64) metav1.Condition {
	c := metav1.Condition{
		Type:               "ControllerInstalled",
		ObservedGeneration: generation,
		LastTransitionTime: metav1.Now(),
	}

	if istio == nil {
		c.Status = metav1.ConditionUnknown
		c.Reason = "Pending"
		c.Message = "waiting for Istio CR to be created"
		return c
	}

	if istio.Status.Ready {
		c.Status = metav1.ConditionTrue
		c.Reason = "Installed"
		c.Message = fmt.Sprintf("istiod %s installed", istio.Spec.Version)
	} else {
		c.Status = metav1.ConditionFalse
		c.Reason = "NotReady"
		c.Message = "istiod not yet ready"
	}

	return c
}
