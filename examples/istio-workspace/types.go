package main

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Simplified types modeling the real istio-workspace CRDs and Istio networking types.
// In production these come from:
//   github.com/maistra/istio-workspace/api/maistra/v1alpha1
//   istio.io/client-go/pkg/apis/networking/v1alpha3

var (
	WorkspaceGV  = schema.GroupVersion{Group: "workspace.maistra.io", Version: "v1alpha1"}
	IstioNetGV   = schema.GroupVersion{Group: "networking.istio.io", Version: "v1alpha3"}
)

// --- Session ---

type Session struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              SessionSpec   `json:"spec,omitempty"`
	Status            SessionStatus `json:"status,omitempty"`
}

type SessionSpec struct {
	Route Route `json:"route,omitempty"`
	Refs  []Ref `json:"ref,omitempty"`
}

type Ref struct {
	Name     string            `json:"name,omitempty"`
	Strategy string            `json:"strategy,omitempty"`
	Args     map[string]string `json:"args,omitempty"`
}

type Route struct {
	Type  string `json:"type,omitempty"`
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
}

type SessionStatus struct {
	State      string   `json:"state,omitempty"`
	Route      *Route   `json:"route,omitempty"`
	Hosts      []string `json:"hosts,omitempty"`
	RefNames   []string `json:"refNames,omitempty"`
	Strategies []string `json:"strategies,omitempty"`
}

func (s *Session) DeepCopyObject() runtime.Object {
	cp := *s
	cp.Spec.Refs = make([]Ref, len(s.Spec.Refs))
	copy(cp.Spec.Refs, s.Spec.Refs)
	cp.Status.Hosts = make([]string, len(s.Status.Hosts))
	copy(cp.Status.Hosts, s.Status.Hosts)
	cp.Status.RefNames = make([]string, len(s.Status.RefNames))
	copy(cp.Status.RefNames, s.Status.RefNames)
	cp.Status.Strategies = make([]string, len(s.Status.Strategies))
	copy(cp.Status.Strategies, s.Status.Strategies)
	return &cp
}

type SessionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Session `json:"items"`
}

func (l *SessionList) DeepCopyObject() runtime.Object {
	cp := *l
	cp.Items = make([]Session, len(l.Items))
	copy(cp.Items, l.Items)
	return &cp
}

// --- VirtualService (simplified) ---

type VirtualService struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              VirtualServiceSpec `json:"spec,omitempty"`
}

type VirtualServiceSpec struct {
	Hosts    []string     `json:"hosts,omitempty"`
	Gateways []string     `json:"gateways,omitempty"`
	HTTP     []HTTPRoute  `json:"http,omitempty"`
}

type HTTPRoute struct {
	Match   []HTTPMatchRequest `json:"match,omitempty"`
	Route   []HTTPRouteTarget  `json:"route,omitempty"`
	Headers *HeaderOperations  `json:"headers,omitempty"`
}

type HTTPMatchRequest struct {
	Headers map[string]StringMatch `json:"headers,omitempty"`
}

type StringMatch struct {
	Exact string `json:"exact,omitempty"`
}

type HTTPRouteTarget struct {
	Destination Destination `json:"destination"`
	Weight      int32       `json:"weight,omitempty"`
}

type Destination struct {
	Host   string `json:"host"`
	Subset string `json:"subset,omitempty"`
	Port   *Port  `json:"port,omitempty"`
}

type Port struct {
	Number uint32 `json:"number"`
}

type HeaderOperations struct {
	Request *HeaderOperationValues `json:"request,omitempty"`
}

type HeaderOperationValues struct {
	Add map[string]string `json:"add,omitempty"`
}

func (v *VirtualService) DeepCopyObject() runtime.Object {
	cp := *v
	cp.Spec.Hosts = make([]string, len(v.Spec.Hosts))
	copy(cp.Spec.Hosts, v.Spec.Hosts)
	cp.Spec.Gateways = make([]string, len(v.Spec.Gateways))
	copy(cp.Spec.Gateways, v.Spec.Gateways)
	cp.Spec.HTTP = make([]HTTPRoute, len(v.Spec.HTTP))
	copy(cp.Spec.HTTP, v.Spec.HTTP)
	return &cp
}

type VirtualServiceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VirtualService `json:"items"`
}

func (l *VirtualServiceList) DeepCopyObject() runtime.Object {
	cp := *l
	cp.Items = make([]VirtualService, len(l.Items))
	copy(cp.Items, l.Items)
	return &cp
}

// --- DestinationRule (simplified) ---

type DestinationRule struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              DestinationRuleSpec `json:"spec,omitempty"`
}

type DestinationRuleSpec struct {
	Host    string   `json:"host"`
	Subsets []Subset `json:"subsets,omitempty"`
}

type Subset struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels,omitempty"`
}

func (d *DestinationRule) DeepCopyObject() runtime.Object {
	cp := *d
	cp.Spec.Subsets = make([]Subset, len(d.Spec.Subsets))
	copy(cp.Spec.Subsets, d.Spec.Subsets)
	return &cp
}

type DestinationRuleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DestinationRule `json:"items"`
}

func (l *DestinationRuleList) DeepCopyObject() runtime.Object {
	cp := *l
	cp.Items = make([]DestinationRule, len(l.Items))
	copy(cp.Items, l.Items)
	return &cp
}

// --- Scheme ---

func AddToScheme(s *runtime.Scheme) {
	s.AddKnownTypeWithName(WorkspaceGV.WithKind("Session"), &Session{})
	s.AddKnownTypeWithName(WorkspaceGV.WithKind("SessionList"), &SessionList{})
	metav1.AddToGroupVersion(s, WorkspaceGV)

	s.AddKnownTypeWithName(IstioNetGV.WithKind("VirtualService"), &VirtualService{})
	s.AddKnownTypeWithName(IstioNetGV.WithKind("VirtualServiceList"), &VirtualServiceList{})
	s.AddKnownTypeWithName(IstioNetGV.WithKind("DestinationRule"), &DestinationRule{})
	s.AddKnownTypeWithName(IstioNetGV.WithKind("DestinationRuleList"), &DestinationRuleList{})
	metav1.AddToGroupVersion(s, IstioNetGV)
}
