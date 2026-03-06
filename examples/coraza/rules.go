package main

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mr "github.com/aslakknutsen/make-reconcile"
)

// AggregatedRulesReconciler produces a ConfigMap containing the validated,
// aggregated rules from all referenced source ConfigMaps.
//
// This is a DESIGN CHANGE from the original controller. The original writes
// aggregated rules into an in-memory cache (cache.Put). That's a pure side
// effect — no k8s object is produced. make-reconcile can't express that.
//
// By materializing the aggregated rules as a ConfigMap, we get:
// - Persistence across restarts (the original's in-memory cache is lost)
// - Standard k8s ownership and GC via owner references
// - The cache server can read from this ConfigMap instead
// - The aggregation logic becomes a testable pure function
//
// Corresponds to the bulk of RuleSetReconciler.Reconcile (~80 lines of
// ConfigMap fetching, validation, aggregation, cache write, and status
// patching). The Fetch calls on each source ConfigMap automatically create
// dependencies — when a source ConfigMap changes, this re-runs.
func AggregatedRulesReconciler(configMaps *mr.Collection[*corev1.ConfigMap]) func(*mr.HandlerContext, *RuleSet) *corev1.ConfigMap {
	return func(hc *mr.HandlerContext, rs *RuleSet) *corev1.ConfigMap {
		if len(rs.Spec.Rules) == 0 {
			return nil
		}

		var aggregated strings.Builder
		for i, rule := range rs.Spec.Rules {
			cm := mr.Fetch(hc, configMaps, mr.FilterName(rule.Name, rs.Namespace))
			if cm == nil {
				hc.RecordEvent("Warning", "ConfigMapNotFound",
					"Referenced ConfigMap %s not found in namespace %s", rule.Name, rs.Namespace)
				return nil
			}

			data, ok := cm.Data["rules"]
			if !ok {
				hc.RecordEvent("Warning", "InvalidRuleSet",
					"ConfigMap %s missing 'rules' key", rule.Name)
				return nil
			}

			aggregated.WriteString(data)
			if i < len(rs.Spec.Rules)-1 {
				aggregated.WriteString("\n")
			}
		}

		// In the original, coraza.NewWAF validates the rules here.
		// Validation is a pure computation — fits fine inside the
		// reconciler function. Omitted for simplicity in this example.

		return &corev1.ConfigMap{
			TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
			ObjectMeta: metav1.ObjectMeta{
				Name:      rs.Name + "-aggregated",
				Namespace: rs.Namespace,
				Labels: map[string]string{
					"waf.k8s.coraza.io/ruleset": rs.Name,
				},
			},
			Data: map[string]string{
				"rules": aggregated.String(),
			},
		}
	}
}
