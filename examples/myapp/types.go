package main

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var SchemeGroupVersion = schema.GroupVersion{Group: "example.io", Version: "v1"}

// MyApp is a simple custom resource that represents an application to deploy.
type MyApp struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              MyAppSpec   `json:"spec,omitempty"`
	Status            MyAppStatus `json:"status,omitempty"`
}

type MyAppSpec struct {
	Image    string `json:"image"`
	Replicas int32  `json:"replicas"`
	Port     int32  `json:"port"`
	// ConfigRef optionally references a ConfigMap to mount into the container.
	ConfigRef string `json:"configRef,omitempty"`
	// EnableMonitoring controls whether a monitoring sidecar is deployed.
	EnableMonitoring bool `json:"enableMonitoring,omitempty"`
}

type MyAppStatus struct {
	Ready bool `json:"ready,omitempty"`
}

func (m *MyApp) DeepCopyObject() runtime.Object {
	cp := *m
	return &cp
}

type MyAppList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MyApp `json:"items"`
}

func (m *MyAppList) DeepCopyObject() runtime.Object {
	cp := *m
	cp.Items = make([]MyApp, len(m.Items))
	copy(cp.Items, m.Items)
	return &cp
}

func AddToScheme(s *runtime.Scheme) {
	s.AddKnownTypeWithName(SchemeGroupVersion.WithKind("MyApp"), &MyApp{})
	s.AddKnownTypeWithName(SchemeGroupVersion.WithKind("MyAppList"), &MyAppList{})
	metav1.AddToGroupVersion(s, SchemeGroupVersion)
}
