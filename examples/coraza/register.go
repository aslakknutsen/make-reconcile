package main

import (
	corev1 "k8s.io/api/core/v1"

	mr "github.com/aslakknutsen/make-reconcile"
)

func RegisterAll(mgr *mr.Manager) {
	// WithGenerationChanged filters out status-only updates (which don't
	// increment .metadata.generation). This matches the original controller's
	// use of predicate.GenerationChangedPredicate on both Engine and RuleSet.
	engines := mr.Watch[*Engine](mgr, mr.WithGenerationChanged())
	ruleSets := mr.Watch[*RuleSet](mgr, mr.WithGenerationChanged())
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
	//    With reconciler-level predicates, each driver gets its own reconciler
	//    that only runs for matching Engines:
	//      mr.Reconcile(mgr, engines, IstioWasmReconciler,
	//          mr.WithPredicate(func(e *Engine) bool { return e.Spec.Driver == "istio" }))
	//      mr.Reconcile(mgr, engines, EnvoyNativeReconciler,
	//          mr.WithPredicate(func(e *Engine) bool { return e.Spec.Driver == "envoy" }))
	//
	// 3. Coraza WAF validation (coraza.NewWAF):
	//    The original validates rules using the coraza library before caching.
	//    This fits fine inside the reconciler function — it's pure computation.
	//    Omitted here for dependency simplicity.
}
