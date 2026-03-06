package makereconcile

import (
	"context"
	"fmt"
	"reflect"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// internalStore is a minimal in-memory cache.Cache for internal tests.
// Unlike mrtest.ObjectStore it does not implement client.Client — internal
// tests that need a client still use fakeClient.
type internalStore struct {
	scheme  *runtime.Scheme
	objects map[internalKey]client.Object
}

type internalKey struct {
	GVK schema.GroupVersionKind
	Key types.NamespacedName
}

func newTestStore(s *runtime.Scheme, objs ...client.Object) *internalStore {
	store := &internalStore{scheme: s, objects: make(map[internalKey]client.Object)}
	for _, obj := range objs {
		gvks, _, _ := s.ObjectKinds(obj)
		if len(gvks) == 0 {
			continue
		}
		nn := types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}
		store.objects[internalKey{GVK: gvks[0], Key: nn}] = obj
	}
	return store
}

func (s *internalStore) Get(_ context.Context, key client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
	gvks, _, err := s.scheme.ObjectKinds(obj)
	if err != nil {
		return fmt.Errorf("unknown type %T: %w", obj, err)
	}
	stored, ok := s.objects[internalKey{GVK: gvks[0], Key: key}]
	if !ok {
		return fmt.Errorf("not found: %v %v", gvks[0], key)
	}
	src := reflect.ValueOf(stored)
	dst := reflect.ValueOf(obj)
	if src.Type() != dst.Type() {
		return fmt.Errorf("type mismatch: stored %T, target %T", stored, obj)
	}
	dst.Elem().Set(src.Elem())
	return nil
}

func (s *internalStore) List(_ context.Context, list client.ObjectList, opts ...client.ListOption) error {
	listOpts := &client.ListOptions{}
	for _, o := range opts {
		o.ApplyToList(listOpts)
	}
	listGVK := list.GetObjectKind().GroupVersionKind()
	if listGVK.Kind == "" {
		gvks, _, err := s.scheme.ObjectKinds(list)
		if err == nil && len(gvks) > 0 {
			listGVK = gvks[0]
		}
	}
	itemKind := listGVK.Kind
	if len(itemKind) > 4 && itemKind[len(itemKind)-4:] == "List" {
		itemKind = itemKind[:len(itemKind)-4]
	}
	itemGVK := schema.GroupVersionKind{Group: listGVK.Group, Version: listGVK.Version, Kind: itemKind}

	var items []runtime.Object
	for k, obj := range s.objects {
		if k.GVK != itemGVK {
			continue
		}
		if listOpts.Namespace != "" && k.Key.Namespace != listOpts.Namespace {
			continue
		}
		if listOpts.LabelSelector != nil && !listOpts.LabelSelector.Matches(labels.Set(obj.GetLabels())) {
			continue
		}
		items = append(items, obj.DeepCopyObject())
	}
	return meta.SetList(list, items)
}

func (s *internalStore) GetInformer(_ context.Context, _ client.Object, _ ...cache.InformerGetOption) (cache.Informer, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *internalStore) GetInformerForKind(_ context.Context, _ schema.GroupVersionKind, _ ...cache.InformerGetOption) (cache.Informer, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *internalStore) RemoveInformer(_ context.Context, _ client.Object) error {
	return fmt.Errorf("not implemented")
}
func (s *internalStore) Start(_ context.Context) error           { return nil }
func (s *internalStore) WaitForCacheSync(_ context.Context) bool { return true }
func (s *internalStore) IndexField(_ context.Context, _ client.Object, _ string, _ client.IndexerFunc) error {
	return fmt.Errorf("not implemented")
}

// internalEventRecorder captures events for internal test assertions.
type internalEventRecorder struct {
	events []internalEvent
}

type internalEvent struct {
	ObjectName string
	EventType  string
	Reason     string
	Message    string
}

func (r *internalEventRecorder) Event(object runtime.Object, eventType string, reason string, message string) {
	name := ""
	if co, ok := object.(metav1.ObjectMetaAccessor); ok {
		name = co.GetObjectMeta().GetName()
	}
	r.events = append(r.events, internalEvent{ObjectName: name, EventType: eventType, Reason: reason, Message: message})
}
func (r *internalEventRecorder) Eventf(object runtime.Object, eventType string, reason string, messageFmt string, args ...interface{}) {
	r.Event(object, eventType, reason, fmt.Sprintf(messageFmt, args...))
}
func (r *internalEventRecorder) AnnotatedEventf(object runtime.Object, _ map[string]string, eventType string, reason string, messageFmt string, args ...interface{}) {
	r.Event(object, eventType, reason, fmt.Sprintf(messageFmt, args...))
}

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
