package makereconcile

import (
	"context"
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	fieldManager    = "make-reconcile"
	managedByLabel  = "make-reconcile.io/managed-by"
)

// applyDesired uses server-side apply to create or update the desired resource.
// It automatically sets ownerReferences pointing to the primary resource and
// a managed-by label used for orphan garbage collection.
func applyDesired(ctx context.Context, c client.Client, desired client.Object, ownerGVK schema.GroupVersionKind, ownerRef types.NamespacedName, ownerUID types.UID, managerID string) error {
	setOwnerRef(desired, ownerGVK, ownerRef, ownerUID)
	setManagedByLabel(desired, managerID)

	opts := []client.PatchOption{
		client.ForceOwnership,
		client.FieldOwner(fieldManager),
	}
	return c.Patch(ctx, desired, client.Apply, opts...)
}

func setManagedByLabel(obj client.Object, managerID string) {
	labels := obj.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels[managedByLabel] = managerID
	obj.SetLabels(labels)
}

// deleteIfExists deletes the resource identified by gvk+name if it exists.
func deleteIfExists(ctx context.Context, c client.Client, scheme *runtime.Scheme, gvk schema.GroupVersionKind, nn types.NamespacedName) error {
	obj, err := scheme.New(gvk)
	if err != nil {
		return fmt.Errorf("unknown GVK %v: %w", gvk, err)
	}
	cObj, ok := obj.(client.Object)
	if !ok {
		return fmt.Errorf("type for %v does not implement client.Object", gvk)
	}
	cObj.SetName(nn.Name)
	cObj.SetNamespace(nn.Namespace)

	err = c.Delete(ctx, cObj)
	if err != nil {
		if isNotFound(err) {
			return nil
		}
		return err
	}
	return nil
}

func setOwnerRef(obj client.Object, ownerGVK schema.GroupVersionKind, ownerRef types.NamespacedName, ownerUID types.UID) {
	if ownerUID == "" {
		return
	}
	apiVersion, kind := ownerGVK.ToAPIVersionAndKind()
	isController := true
	refs := obj.GetOwnerReferences()
	for i, ref := range refs {
		if ref.UID == ownerUID {
			refs[i].Controller = &isController
			obj.SetOwnerReferences(refs)
			return
		}
	}
	refs = append(refs, metav1.OwnerReference{
		APIVersion:         apiVersion,
		Kind:               kind,
		Name:               ownerRef.Name,
		UID:                ownerUID,
		Controller:         &isController,
		BlockOwnerDeletion: boolPtr(true),
	})
	obj.SetOwnerReferences(refs)
}

func boolPtr(b bool) *bool { return &b }

// applyStatus patches the primary resource's status subresource via SSA.
// The status value is JSON-marshaled into the .status field of an unstructured
// object, then applied with the framework's field manager.
func applyStatus(ctx context.Context, c client.Client, gvk schema.GroupVersionKind, nn types.NamespacedName, status any) error {
	statusBytes, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("marshal status: %w", err)
	}
	var statusMap map[string]interface{}
	if err := json.Unmarshal(statusBytes, &statusMap); err != nil {
		return fmt.Errorf("unmarshal status to map: %w", err)
	}

	apiVersion, kind := gvk.ToAPIVersionAndKind()
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": apiVersion,
			"kind":       kind,
			"metadata": map[string]interface{}{
				"name":      nn.Name,
				"namespace": nn.Namespace,
			},
			"status": statusMap,
		},
	}

	return c.Status().Patch(ctx, obj, client.Apply,
		client.ForceOwnership,
		client.FieldOwner(fieldManager),
	)
}

// addFinalizer adds the named finalizer to obj via merge patch. No-op if
// the finalizer is already present.
func addFinalizer(ctx context.Context, c client.Client, obj client.Object, finalizer string) error {
	if hasFinalizer(obj, finalizer) {
		return nil
	}
	base := obj.DeepCopyObject().(client.Object)
	obj.SetFinalizers(append(obj.GetFinalizers(), finalizer))
	return c.Patch(ctx, obj, client.MergeFrom(base))
}

// removeFinalizer removes the named finalizer from obj via merge patch.
// No-op if the finalizer is not present.
func removeFinalizer(ctx context.Context, c client.Client, obj client.Object, finalizer string) error {
	if !hasFinalizer(obj, finalizer) {
		return nil
	}
	base := obj.DeepCopyObject().(client.Object)
	var remaining []string
	for _, f := range obj.GetFinalizers() {
		if f != finalizer {
			remaining = append(remaining, f)
		}
	}
	obj.SetFinalizers(remaining)
	return c.Patch(ctx, obj, client.MergeFrom(base))
}

func hasFinalizer(obj client.Object, finalizer string) bool {
	for _, f := range obj.GetFinalizers() {
		if f == finalizer {
			return true
		}
	}
	return false
}

func isNotFound(err error) bool {
	// apimachinery errors implement StatusError with a reason.
	type statusErr interface {
		Status() metav1.Status
	}
	if se, ok := err.(statusErr); ok {
		return se.Status().Reason == metav1.StatusReasonNotFound
	}
	return false
}
