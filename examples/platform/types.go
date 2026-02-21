package main

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var SchemeGroupVersion = schema.GroupVersion{Group: "platform.example.io", Version: "v1"}

const (
	LabelName      = "platform.example.io/name"
	LabelComponent = "platform.example.io/component"
)

type Platform struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              PlatformSpec   `json:"spec,omitempty"`
	Status            PlatformStatus `json:"status,omitempty"`
}

type PlatformSpec struct {
	App         AppSpec         `json:"app"`
	Database    DatabaseSpec    `json:"database"`
	Cache       CacheSpec       `json:"cache,omitempty"`
	Ingress     IngressSpec     `json:"ingress,omitempty"`
	Autoscaling AutoscalingSpec `json:"autoscaling,omitempty"`
	Monitoring  MonitoringSpec  `json:"monitoring,omitempty"`
	Workers     []WorkerSpec    `json:"workers,omitempty"`
}

type AppSpec struct {
	Image    string `json:"image"`
	Replicas int32  `json:"replicas"`
	Port     int32  `json:"port"`
}

type DatabaseSpec struct {
	Image       string `json:"image"`
	StorageSize string `json:"storageSize"`
	Port        int32  `json:"port"`
}

type CacheSpec struct {
	Enabled bool   `json:"enabled"`
	Image   string `json:"image,omitempty"`
	Port    int32  `json:"port,omitempty"`
}

type IngressSpec struct {
	Enabled      bool   `json:"enabled"`
	Host         string `json:"host,omitempty"`
	TLSSecretRef string `json:"tlsSecretRef,omitempty"`
}

type AutoscalingSpec struct {
	Enabled     bool  `json:"enabled"`
	MinReplicas int32 `json:"minReplicas,omitempty"`
	MaxReplicas int32 `json:"maxReplicas,omitempty"`
	TargetCPU   int32 `json:"targetCPU,omitempty"`
}

type MonitoringSpec struct {
	Enabled bool   `json:"enabled"`
	Image   string `json:"image,omitempty"`
}

type WorkerSpec struct {
	Name    string   `json:"name"`
	Image   string   `json:"image"`
	Command []string `json:"command,omitempty"`
}

type PlatformStatus struct {
	Ready      bool   `json:"ready,omitempty"`
	Components int    `json:"components,omitempty"`
	Message    string `json:"message,omitempty"`
}

func (p *Platform) DeepCopyObject() runtime.Object {
	cp := *p
	cp.Spec.Workers = make([]WorkerSpec, len(p.Spec.Workers))
	copy(cp.Spec.Workers, p.Spec.Workers)
	return &cp
}

type PlatformList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Platform `json:"items"`
}

func (p *PlatformList) DeepCopyObject() runtime.Object {
	cp := *p
	cp.Items = make([]Platform, len(p.Items))
	copy(cp.Items, p.Items)
	return &cp
}

func AddToScheme(s *runtime.Scheme) {
	s.AddKnownTypeWithName(SchemeGroupVersion.WithKind("Platform"), &Platform{})
	s.AddKnownTypeWithName(SchemeGroupVersion.WithKind("PlatformList"), &PlatformList{})
	metav1.AddToGroupVersion(s, SchemeGroupVersion)
}

// componentLabels returns the standard labels for a sub-resource.
func componentLabels(platformName, component string) map[string]string {
	return map[string]string{
		LabelName:      platformName,
		LabelComponent: component,
	}
}

// selectorLabels returns labels used for pod selectors (subset of componentLabels).
func selectorLabels(platformName, component string) map[string]string {
	return map[string]string{
		LabelName:      platformName,
		LabelComponent: component,
	}
}
