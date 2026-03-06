package mrtest

import (
	"fmt"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// Event is a recorded Kubernetes event for test assertions.
type Event struct {
	ObjectName string
	EventType  string
	Reason     string
	Message    string
}

// EventRecorder captures events for test assertions. It implements
// k8s.io/client-go/tools/record.EventRecorder.
type EventRecorder struct {
	mu     sync.Mutex
	events []Event
}

func (r *EventRecorder) Event(object runtime.Object, eventType string, reason string, message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := ""
	if co, ok := object.(metav1.ObjectMetaAccessor); ok {
		name = co.GetObjectMeta().GetName()
	}
	r.events = append(r.events, Event{
		ObjectName: name,
		EventType:  eventType,
		Reason:     reason,
		Message:    message,
	})
}

func (r *EventRecorder) Eventf(object runtime.Object, eventType string, reason string, messageFmt string, args ...interface{}) {
	r.Event(object, eventType, reason, fmt.Sprintf(messageFmt, args...))
}

func (r *EventRecorder) AnnotatedEventf(object runtime.Object, _ map[string]string, eventType string, reason string, messageFmt string, args ...interface{}) {
	r.Event(object, eventType, reason, fmt.Sprintf(messageFmt, args...))
}

// Events returns a copy of all recorded events.
func (r *EventRecorder) Events() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]Event, len(r.events))
	copy(cp, r.events)
	return cp
}

// Reset clears all recorded events.
func (r *EventRecorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = nil
}
