package main

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mr "github.com/aslakknutsen/make-reconcile"
)

// SessionDestinationRuleReconciler creates a DestinationRule for each Session
// Ref, pointing to the cloned Deployment's pod version label.
//
// This maps reasonably well to make-reconcile. The original controller's
// DestinationRuleModificator creates a new DR (not mutating the existing one)
// with a single subset for the session version. Here we do the same via Fetch
// on the existing DR to extract the TrafficPolicy, then return a new DR.
//
// The original locates the DR by matching the service hostname against
// DR.spec.host — a query that Fetch can't express directly. We approximate
// by fetching all DRs in the namespace and filtering manually.
func SessionDestinationRuleReconciler(
	destinationRules *mr.Collection[*DestinationRule],
) func(*mr.HandlerContext, *Session) []DestinationRule {
	return func(hc *mr.HandlerContext, session *Session) []DestinationRule {
		allDRs := mr.FetchAll(hc, destinationRules,
			mr.FilterNamespace(session.Namespace))

		var results []DestinationRule

		for _, ref := range session.Spec.Refs {
			version := ref.Name + "-" + session.Name
			hostName := ref.Name + "." + session.Namespace + ".svc.cluster.local"

			var matchedDR *DestinationRule
			for i := range allDRs {
				if allDRs[i].Spec.Host == hostName || allDRs[i].Spec.Host == ref.Name {
					matchedDR = allDRs[i]
					break
				}
			}
			if matchedDR == nil {
				continue
			}

			results = append(results, DestinationRule{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "networking.istio.io/v1alpha3",
					Kind:       "DestinationRule",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dr-" + ref.Name + "-" + session.Name,
					Namespace: session.Namespace,
				},
				Spec: DestinationRuleSpec{
					Host: matchedDR.Spec.Host,
					Subsets: []Subset{{
						Name:   version,
						Labels: map[string]string{"version": version},
					}},
				},
			})
		}

		return results
	}
}
