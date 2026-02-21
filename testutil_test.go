package makereconcile

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// testObj is a minimal client.Object for unit testing.
type testObj struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              testObjSpec `json:"spec,omitempty"`
}

type testObjSpec struct {
	Image    string `json:"image,omitempty"`
	Replicas int32  `json:"replicas,omitempty"`
}

func (t *testObj) DeepCopyObject() runtime.Object {
	cp := *t
	return &cp
}

// testObjList is the list type for testObj.
type testObjList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []testObj `json:"items"`
}

func (t *testObjList) DeepCopyObject() runtime.Object {
	cp := *t
	cp.Items = make([]testObj, len(t.Items))
	copy(cp.Items, t.Items)
	return &cp
}

// outputObj is a minimal output type for testing.
type outputObj struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Data              string `json:"data,omitempty"`
}

func (o *outputObj) DeepCopyObject() runtime.Object {
	cp := *o
	return &cp
}

type outputObjList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []outputObj `json:"items"`
}

func (o *outputObjList) DeepCopyObject() runtime.Object {
	cp := *o
	cp.Items = make([]outputObj, len(o.Items))
	copy(cp.Items, o.Items)
	return &cp
}

var testSchemeGV = schema.GroupVersion{Group: "test.io", Version: "v1"}

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	s.AddKnownTypeWithName(
		testSchemeGV.WithKind("TestObj"),
		&testObj{},
	)
	s.AddKnownTypeWithName(
		testSchemeGV.WithKind("TestObjList"),
		&testObjList{},
	)
	s.AddKnownTypeWithName(
		testSchemeGV.WithKind("OutputObj"),
		&outputObj{},
	)
	s.AddKnownTypeWithName(
		testSchemeGV.WithKind("OutputObjList"),
		&outputObjList{},
	)
	return s
}
