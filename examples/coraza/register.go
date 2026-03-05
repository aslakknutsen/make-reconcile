package main

import (
	corev1 "k8s.io/api/core/v1"

	mr "github.com/aslakknutsen/make-reconcile"
)

func RegisterAll(mgr *mr.Manager) {
	engines := mr.Watch[*Engine](mgr)
	ruleSets := mr.Watch[*RuleSet](mgr)
	configMaps := mr.Watch[*corev1.ConfigMap](mgr)
	wasmPlugins := mr.Watch[*WasmPlugin](mgr)
	pods := mr.Watch[*corev1.Pod](mgr)

	// --- Engine controller ---
	//
	// Engine -> WasmPlugin
	//
	// The original controller's provisionIstioEngineWithWasm does:
	//   1. buildWasmPlugin (pure construction)
	//   2. SetControllerReference
	//   3. serverSideApply
	//   4. matchedGateways
	//   5. Status patch
	//   6. Event recording
	//
	// Here, step 1 is the reconciler function. Steps 2-3 are handled by
	// the framework (owner ref + SSA). Steps 4-5 are the status reconciler.
	// Step 6 (events) has no equivalent — see analysis.
	mr.Reconcile(mgr, engines, WasmPluginReconciler)

	// Engine -> status (conditions + matched gateways)
	mr.ReconcileStatus(mgr, engines, EngineStatusReconciler(wasmPlugins, pods))

	// --- RuleSet controller ---
	//
	// RuleSet -> aggregated ConfigMap
	//
	// The original controller does NOT produce a k8s object — it writes
	// to an in-memory cache. This is a design change that materializes
	// the aggregated rules as a ConfigMap. See rules.go for rationale.
	//
	// The Fetch calls on source ConfigMaps automatically track dependencies.
	// The original implements this manually via findRuleSetsForConfigMap
	// which lists all RuleSets and checks if any reference the changed
	// ConfigMap. That's ~30 lines of watch predicate code replaced by Fetch.
	mr.Reconcile(mgr, ruleSets, AggregatedRulesReconciler(configMaps))

	// RuleSet -> status (validates source ConfigMaps exist and have rules key)
	mr.ReconcileStatus(mgr, ruleSets, RuleSetStatusReconciler(configMaps, configMaps))

	// --- What the original has that we don't model ---
	//
	// 1. Event recording (r.Recorder.Eventf):
	//    The original fires k8s Events for ConfigMapNotFound, InvalidRuleSet,
	//    WasmPluginCreated, etc. make-reconcile has no event recording hook.
	//    Could be added as a callback/middleware on reconciler functions.
	//
	// 2. Driver dispatch (selectDriver):
	//    The original switches on engine.Spec.Driver.Istio vs future drivers.
	//    In make-reconcile, you'd register different reconcilers per driver
	//    and have each one return nil when its driver isn't selected:
	//      mr.Reconcile(mgr, engines, IstioWasmReconciler)    // returns nil if not istio
	//      mr.Reconcile(mgr, engines, EnvoyNativeReconciler)  // returns nil if not envoy
	//    This works but means all drivers run for every Engine. A predicate
	//    on Watch or Reconcile would be more efficient.
	//
	// 3. Coraza WAF validation (coraza.NewWAF):
	//    The original validates rules using the coraza library before caching.
	//    This fits fine inside the reconciler function — it's pure computation.
	//    Omitted here for dependency simplicity.
	//
	// 4. GenerationChangedPredicate:
	//    The original only reconciles on spec changes (generation increment),
	//    not on status-only updates. make-reconcile triggers on any change.
	//    This is wasteful but correct — the reconciler is idempotent.
}
