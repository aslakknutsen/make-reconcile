package main

import (
	"fmt"
	"slices"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mr "github.com/aslakknutsen/make-reconcile"
)

const gatewayNameLabel = "gateway.networking.k8s.io/gateway-name"

// EngineStatusReconciler computes Engine status by checking the WasmPlugin
// and finding matched Gateways via Pod labels.
//
// The original does this inline in provisionIstioEngineWithWasm after the
// SSA apply. It lists pods matching the workload selector, extracts gateway
// names, and patches status.
//
// Here, Fetch on WasmPlugins creates a dependency so status updates when the
// plugin changes. FetchAll on Pods with label filter finds matched gateways.
// When pods appear/disappear (gateway scaling), this re-runs automatically
// because FetchAll creates a broad dependency.
func EngineStatusReconciler(
	wasmPlugins *mr.Collection[*WasmPlugin],
	pods *mr.Collection[*corev1.Pod],
) func(*mr.HandlerContext, *Engine) *EngineStatus {
	return func(hc *mr.HandlerContext, engine *Engine) *EngineStatus {
		wp := mr.Fetch(hc, wasmPlugins,
			mr.FilterName(wasmPluginNamePrefix+engine.Name, engine.Namespace))

		if wp == nil {
			return &EngineStatus{
				Conditions: []metav1.Condition{{
					Type:               "Ready",
					Status:             metav1.ConditionFalse,
					ObservedGeneration: engine.Generation,
					LastTransitionTime: metav1.Now(),
					Reason:             "WasmPluginNotFound",
					Message:            "WasmPlugin has not been created yet",
				}},
			}
		}

		gateways := matchedGateways(hc, pods, engine)

		return &EngineStatus{
			Gateways: gateways,
			Conditions: []metav1.Condition{{
				Type:               "Ready",
				Status:             metav1.ConditionTrue,
				ObservedGeneration: engine.Generation,
				LastTransitionTime: metav1.Now(),
				Reason:             "Configured",
				Message:            fmt.Sprintf("WasmPlugin %s configured", wp.Name),
			}},
		}
	}
}

// matchedGateways finds Gateways whose pods match the Engine's workload selector.
// Uses FetchAll with label filter — any pod change in the namespace triggers re-evaluation.
func matchedGateways(hc *mr.HandlerContext, pods *mr.Collection[*corev1.Pod], engine *Engine) []GatewayReference {
	if engine.Spec.Selector == nil {
		return nil
	}

	allPods := mr.FetchAll(hc, pods,
		mr.FilterNamespace(engine.Namespace),
		mr.FilterLabel(engine.Spec.Selector.MatchLabels))

	var names []string
	for _, pod := range allPods {
		if gwName := pod.Labels[gatewayNameLabel]; gwName != "" {
			names = append(names, gwName)
		}
	}
	slices.Sort(names)
	names = slices.Compact(names)

	gateways := make([]GatewayReference, len(names))
	for i, name := range names {
		gateways[i] = GatewayReference{Name: name}
	}
	return gateways
}

// RuleSetStatusReconciler computes RuleSet status by checking whether the
// aggregated ConfigMap was produced successfully.
//
// The original interleaves status patching throughout the reconcile loop
// (ConfigMap not found → Degraded, invalid rules → Degraded, success → Ready).
// Here it's a separate concern that reads the output ConfigMap.
func RuleSetStatusReconciler(
	configMaps *mr.Collection[*corev1.ConfigMap],
	sourceCMs *mr.Collection[*corev1.ConfigMap],
) func(*mr.HandlerContext, *RuleSet) *RuleSetStatus {
	return func(hc *mr.HandlerContext, rs *RuleSet) *RuleSetStatus {
		// Check if all source ConfigMaps exist
		for _, rule := range rs.Spec.Rules {
			cm := mr.Fetch(hc, sourceCMs, mr.FilterName(rule.Name, rs.Namespace))
			if cm == nil {
				return &RuleSetStatus{
					Conditions: []metav1.Condition{{
						Type:               "Ready",
						Status:             metav1.ConditionFalse,
						ObservedGeneration: rs.Generation,
						LastTransitionTime: metav1.Now(),
						Reason:             "ConfigMapNotFound",
						Message:            fmt.Sprintf("Referenced ConfigMap %s does not exist", rule.Name),
					}},
				}
			}
			if _, ok := cm.Data["rules"]; !ok {
				return &RuleSetStatus{
					Conditions: []metav1.Condition{{
						Type:               "Ready",
						Status:             metav1.ConditionFalse,
						ObservedGeneration: rs.Generation,
						LastTransitionTime: metav1.Now(),
						Reason:             "InvalidConfigMap",
						Message:            fmt.Sprintf("ConfigMap %s missing 'rules' key", rule.Name),
					}},
				}
			}
		}

		aggregated := mr.Fetch(hc, configMaps,
			mr.FilterName(rs.Name+"-aggregated", rs.Namespace))
		if aggregated == nil {
			return &RuleSetStatus{
				Conditions: []metav1.Condition{{
					Type:               "Ready",
					Status:             metav1.ConditionFalse,
					ObservedGeneration: rs.Generation,
					LastTransitionTime: metav1.Now(),
					Reason:             "Pending",
					Message:            "Aggregated rules not yet available",
				}},
			}
		}

		return &RuleSetStatus{
			Conditions: []metav1.Condition{{
				Type:               "Ready",
				Status:             metav1.ConditionTrue,
				ObservedGeneration: rs.Generation,
				LastTransitionTime: metav1.Now(),
				Reason:             "RulesCached",
				Message:            fmt.Sprintf("Rules aggregated for %s/%s", rs.Namespace, rs.Name),
			}},
		}
	}
}
