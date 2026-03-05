package main

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Simplified types modeling the real coraza-kubernetes-operator CRDs.
// In production, these come from github.com/networking-incubator/coraza-kubernetes-operator/api/v1alpha1.

var (
	WafGV        = schema.GroupVersion{Group: "waf.k8s.coraza.io", Version: "v1alpha1"}
	IstioExtGV   = schema.GroupVersion{Group: "extensions.istio.io", Version: "v1alpha1"}
)

// --- Engine ---

type Engine struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              EngineSpec   `json:"spec,omitempty"`
	Status            EngineStatus `json:"status,omitempty"`
}

type EngineSpec struct {
	RuleSet       RuleSetReference       `json:"ruleSet"`
	WasmImage     string                 `json:"wasmImage"`
	FailurePolicy string                 `json:"failurePolicy"`
	Selector      *metav1.LabelSelector  `json:"workloadSelector,omitempty"`
	CacheCluster  string                 `json:"cacheCluster,omitempty"`
	PollInterval  int32                  `json:"pollIntervalSeconds,omitempty"`
}

type RuleSetReference struct {
	Name string `json:"name"`
}

type GatewayReference struct {
	Name string `json:"name"`
}

type EngineStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	Gateways   []GatewayReference `json:"gateways,omitempty"`
}

func (e *Engine) DeepCopyObject() runtime.Object {
	cp := *e
	cp.Status.Conditions = make([]metav1.Condition, len(e.Status.Conditions))
	copy(cp.Status.Conditions, e.Status.Conditions)
	cp.Status.Gateways = make([]GatewayReference, len(e.Status.Gateways))
	copy(cp.Status.Gateways, e.Status.Gateways)
	return &cp
}

type EngineList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Engine `json:"items"`
}

func (l *EngineList) DeepCopyObject() runtime.Object {
	cp := *l
	cp.Items = make([]Engine, len(l.Items))
	copy(cp.Items, l.Items)
	return &cp
}

// --- RuleSet ---

type RuleSet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              RuleSetSpec   `json:"spec,omitempty"`
	Status            RuleSetStatus `json:"status,omitempty"`
}

type RuleSetSpec struct {
	Rules []RuleSourceReference `json:"rules"`
}

type RuleSourceReference struct {
	Name string `json:"name"`
}

type RuleSetStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

func (r *RuleSet) DeepCopyObject() runtime.Object {
	cp := *r
	cp.Spec.Rules = make([]RuleSourceReference, len(r.Spec.Rules))
	copy(cp.Spec.Rules, r.Spec.Rules)
	cp.Status.Conditions = make([]metav1.Condition, len(r.Status.Conditions))
	copy(cp.Status.Conditions, r.Status.Conditions)
	return &cp
}

type RuleSetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RuleSet `json:"items"`
}

func (l *RuleSetList) DeepCopyObject() runtime.Object {
	cp := *l
	cp.Items = make([]RuleSet, len(l.Items))
	copy(cp.Items, l.Items)
	return &cp
}

// --- WasmPlugin (Istio extension) ---
//
// The original controller uses unstructured.Unstructured for this type
// because the Istio WasmPlugin CRD isn't available as a Go type in their
// dependencies. make-reconcile requires typed objects registered in the
// scheme, so we define a simplified typed version here.

type WasmPlugin struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              WasmPluginSpec `json:"spec,omitempty"`
}

type WasmPluginSpec struct {
	URL          string            `json:"url"`
	PluginConfig map[string]any    `json:"pluginConfig,omitempty"`
	Selector     map[string]string `json:"matchLabels,omitempty"`
}

func (w *WasmPlugin) DeepCopyObject() runtime.Object {
	cp := *w
	if w.Spec.PluginConfig != nil {
		cp.Spec.PluginConfig = make(map[string]any, len(w.Spec.PluginConfig))
		for k, v := range w.Spec.PluginConfig {
			cp.Spec.PluginConfig[k] = v
		}
	}
	if w.Spec.Selector != nil {
		cp.Spec.Selector = make(map[string]string, len(w.Spec.Selector))
		for k, v := range w.Spec.Selector {
			cp.Spec.Selector[k] = v
		}
	}
	return &cp
}

type WasmPluginList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WasmPlugin `json:"items"`
}

func (l *WasmPluginList) DeepCopyObject() runtime.Object {
	cp := *l
	cp.Items = make([]WasmPlugin, len(l.Items))
	copy(cp.Items, l.Items)
	return &cp
}

// --- Scheme ---

func AddToScheme(s *runtime.Scheme) {
	s.AddKnownTypeWithName(WafGV.WithKind("Engine"), &Engine{})
	s.AddKnownTypeWithName(WafGV.WithKind("EngineList"), &EngineList{})
	s.AddKnownTypeWithName(WafGV.WithKind("RuleSet"), &RuleSet{})
	s.AddKnownTypeWithName(WafGV.WithKind("RuleSetList"), &RuleSetList{})
	metav1.AddToGroupVersion(s, WafGV)

	s.AddKnownTypeWithName(IstioExtGV.WithKind("WasmPlugin"), &WasmPlugin{})
	s.AddKnownTypeWithName(IstioExtGV.WithKind("WasmPluginList"), &WasmPluginList{})
	metav1.AddToGroupVersion(s, IstioExtGV)
}
