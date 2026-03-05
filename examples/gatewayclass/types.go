package main

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Simplified types modeling the real GatewayClass and Istio CRs.
// In production, these come from sigs.k8s.io/gateway-api and
// github.com/istio-ecosystem/sail-operator/api/v1.

var (
	GatewayClassGV = schema.GroupVersion{Group: "gateway.networking.k8s.io", Version: "v1"}
	IstioGV        = schema.GroupVersion{Group: "sailoperator.io", Version: "v1"}
)

// --- GatewayClass (cluster-scoped) ---

type GatewayClass struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              GatewayClassSpec   `json:"spec,omitempty"`
	Status            GatewayClassStatus `json:"status,omitempty"`
}

type GatewayClassSpec struct {
	ControllerName string `json:"controllerName"`
}

type GatewayClassStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

func (g *GatewayClass) DeepCopyObject() runtime.Object {
	cp := *g
	cp.Status.Conditions = make([]metav1.Condition, len(g.Status.Conditions))
	copy(cp.Status.Conditions, g.Status.Conditions)
	return &cp
}

type GatewayClassList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GatewayClass `json:"items"`
}

func (l *GatewayClassList) DeepCopyObject() runtime.Object {
	cp := *l
	cp.Items = make([]GatewayClass, len(l.Items))
	copy(cp.Items, l.Items)
	return &cp
}

// --- Istio CR (namespaced) ---

type Istio struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              IstioSpec   `json:"spec,omitempty"`
	Status            IstioStatus `json:"status,omitempty"`
}

type IstioSpec struct {
	Version   string            `json:"version"`
	Namespace string            `json:"namespace"`
	PilotEnv  map[string]string `json:"pilotEnv,omitempty"`
}

type IstioStatus struct {
	Ready      bool               `json:"ready,omitempty"`
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

func (i *Istio) DeepCopyObject() runtime.Object {
	cp := *i
	if i.Spec.PilotEnv != nil {
		cp.Spec.PilotEnv = make(map[string]string, len(i.Spec.PilotEnv))
		for k, v := range i.Spec.PilotEnv {
			cp.Spec.PilotEnv[k] = v
		}
	}
	cp.Status.Conditions = make([]metav1.Condition, len(i.Status.Conditions))
	copy(cp.Status.Conditions, i.Status.Conditions)
	return &cp
}

type IstioList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Istio `json:"items"`
}

func (l *IstioList) DeepCopyObject() runtime.Object {
	cp := *l
	cp.Items = make([]Istio, len(l.Items))
	copy(cp.Items, l.Items)
	return &cp
}

func AddToScheme(s *runtime.Scheme) {
	s.AddKnownTypeWithName(GatewayClassGV.WithKind("GatewayClass"), &GatewayClass{})
	s.AddKnownTypeWithName(GatewayClassGV.WithKind("GatewayClassList"), &GatewayClassList{})
	metav1.AddToGroupVersion(s, GatewayClassGV)

	s.AddKnownTypeWithName(IstioGV.WithKind("Istio"), &Istio{})
	s.AddKnownTypeWithName(IstioGV.WithKind("IstioList"), &IstioList{})
	metav1.AddToGroupVersion(s, IstioGV)
}
