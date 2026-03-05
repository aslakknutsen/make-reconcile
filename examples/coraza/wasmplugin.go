package main

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mr "github.com/aslakknutsen/make-reconcile"
)

const wasmPluginNamePrefix = "coraza-engine-"

// WasmPluginReconciler produces an Istio WasmPlugin for each Engine.
//
// Corresponds to provisionIstioEngineWithWasm + buildWasmPlugin in the
// original controller (~100 lines including error handling, logging, status
// patching, and event recording). Here it's a pure function returning
// desired state.
//
// The original also does inline status patching on both success and failure.
// In make-reconcile, that's a separate StatusReconciler — this function only
// produces the WasmPlugin.
func WasmPluginReconciler(hc *mr.HandlerContext, engine *Engine) *WasmPlugin {
	if engine.Spec.WasmImage == "" {
		return nil
	}

	matchLabels := map[string]string{}
	if engine.Spec.Selector != nil {
		matchLabels = engine.Spec.Selector.MatchLabels
	}

	rulesetKey := fmt.Sprintf("%s/%s", engine.Namespace, engine.Spec.RuleSet.Name)

	pluginConfig := map[string]any{
		"cache_server_instance": rulesetKey,
		"cache_server_cluster":  engine.Spec.CacheCluster,
	}
	if engine.Spec.PollInterval > 0 {
		pluginConfig["rule_reload_interval_seconds"] = engine.Spec.PollInterval
	}

	return &WasmPlugin{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "extensions.istio.io/v1alpha1",
			Kind:       "WasmPlugin",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      wasmPluginNamePrefix + engine.Name,
			Namespace: engine.Namespace,
		},
		Spec: WasmPluginSpec{
			URL:          engine.Spec.WasmImage,
			PluginConfig: pluginConfig,
			Selector:     matchLabels,
		},
	}
}
