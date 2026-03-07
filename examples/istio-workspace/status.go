package main

import (
	appsv1 "k8s.io/api/apps/v1"

	mr "github.com/aslakknutsen/make-reconcile"
)

// SessionStatusReconciler computes Session status from the state of cloned
// Deployments and session DestinationRules.
//
// The original controller updates status progressively during the
// locate→validate→modify pipeline, emitting multiple Status().Update calls.
// make-reconcile runs status reconcilers once after all output reconcilers,
// so we compute the aggregate here.
func SessionStatusReconciler(
	deployments *mr.Collection[*appsv1.Deployment],
	destinationRules *mr.Collection[*DestinationRule],
) func(*mr.HandlerContext, *Session) *SessionStatus {
	return func(hc *mr.HandlerContext, session *Session) *SessionStatus {
		route := resolveRoute(session)

		var refNames []string
		var strategies []string
		allReady := true

		for _, ref := range session.Spec.Refs {
			refNames = append(refNames, ref.Name)
			if ref.Strategy != "" {
				strategies = append(strategies, ref.Strategy)
			}

			cloneName := ref.Name + "-" + session.Name
			deploy := mr.Fetch(hc, deployments,
				mr.FilterName(cloneName, session.Namespace))
			if deploy == nil {
				allReady = false
				continue
			}
			if deploy.Status.ReadyReplicas == 0 {
				allReady = false
			}

			drName := "dr-" + ref.Name + "-" + session.Name
			dr := mr.Fetch(hc, destinationRules,
				mr.FilterName(drName, session.Namespace))
			if dr == nil {
				allReady = false
			}
		}

		state := "Processing"
		if allReady && len(session.Spec.Refs) > 0 {
			state = "Success"
		}

		return &SessionStatus{
			State:      state,
			Route:      &route,
			RefNames:   refNames,
			Strategies: strategies,
		}
	}
}

func resolveRoute(session *Session) Route {
	if session.Spec.Route.Type != "" {
		return session.Spec.Route
	}
	return Route{
		Type:  "header",
		Name:  "x-workspace-route",
		Value: session.Name,
	}
}
