package makereconcile

import (
	"context"
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	contributorAnnotation = "make-reconcile.io/contributors"
	maxConflictRetries    = 5
)

// ownershipStrategy controls how output resources are linked to their primary
// resources. Selected at Manager construction time.
type ownershipStrategy interface {
	// prepareDesired mutates the desired object before SSA apply
	// (e.g., set OwnerReference for ownerRef strategy, no-op for annotation).
	prepareDesired(desired client.Object, ownerGVK schema.GroupVersionKind,
		ownerRef types.NamespacedName, ownerUID types.UID, managerID string)

	// applyOwnership performs post-apply bookkeeping. For annotation strategy,
	// this adds the primary to the contributor list on the output resource.
	applyOwnership(ctx context.Context, c client.Client, scheme *runtime.Scheme,
		outputGVK schema.GroupVersionKind, outputNN types.NamespacedName,
		ownerGVK schema.GroupVersionKind, ownerRef types.NamespacedName,
		managerID string) error

	// releaseOwnership removes this primary's claim from the output. Returns
	// shouldDelete=true if no owners remain and the resource should be deleted.
	releaseOwnership(ctx context.Context, c client.Client, scheme *runtime.Scheme,
		outputGVK schema.GroupVersionKind, outputNN types.NamespacedName,
		ownerGVK schema.GroupVersionKind, ownerRef types.NamespacedName,
		managerID string) (shouldDelete bool, err error)

	// needsFinalizer reports whether this strategy requires a finalizer on
	// primaries for cleanup, even without an explicit OnDelete callback.
	needsFinalizer() bool
}

// ownerRefStrategy is the default: sets OwnerReferences on output resources and
// relies on Kubernetes GC for cleanup on primary deletion.
type ownerRefStrategy struct{}

func (s *ownerRefStrategy) prepareDesired(desired client.Object, ownerGVK schema.GroupVersionKind,
	ownerRef types.NamespacedName, ownerUID types.UID, _ string) {
	setOwnerRef(desired, ownerGVK, ownerRef, ownerUID)
}

func (s *ownerRefStrategy) applyOwnership(_ context.Context, _ client.Client, _ *runtime.Scheme,
	_ schema.GroupVersionKind, _ types.NamespacedName,
	_ schema.GroupVersionKind, _ types.NamespacedName, _ string) error {
	return nil
}

func (s *ownerRefStrategy) releaseOwnership(_ context.Context, _ client.Client, _ *runtime.Scheme,
	_ schema.GroupVersionKind, _ types.NamespacedName,
	_ schema.GroupVersionKind, _ types.NamespacedName, _ string) (bool, error) {
	return true, nil
}

func (s *ownerRefStrategy) needsFinalizer() bool { return false }

// annotationStrategy tracks contributing primaries via an annotation on the
// output resource. No OwnerReference is set. The output is only deleted when
// the last contributor is removed.
type annotationStrategy struct{}

func (s *annotationStrategy) prepareDesired(_ client.Object, _ schema.GroupVersionKind,
	_ types.NamespacedName, _ types.UID, _ string) {
	// No OwnerReference — lifecycle is managed via contributor annotation.
}

func (s *annotationStrategy) applyOwnership(ctx context.Context, c client.Client, scheme *runtime.Scheme,
	outputGVK schema.GroupVersionKind, outputNN types.NamespacedName,
	ownerGVK schema.GroupVersionKind, ownerRef types.NamespacedName,
	_ string) error {
	obj, err := newObjectForGVK(scheme, outputGVK, outputNN)
	if err != nil {
		return err
	}
	contributor := contributorKey(ownerGVK, ownerRef)
	return addContributor(ctx, c, obj, contributor)
}

func (s *annotationStrategy) releaseOwnership(ctx context.Context, c client.Client, scheme *runtime.Scheme,
	outputGVK schema.GroupVersionKind, outputNN types.NamespacedName,
	ownerGVK schema.GroupVersionKind, ownerRef types.NamespacedName,
	_ string) (bool, error) {
	obj, err := newObjectForGVK(scheme, outputGVK, outputNN)
	if err != nil {
		return false, err
	}
	contributor := contributorKey(ownerGVK, ownerRef)
	return removeContributor(ctx, c, obj, contributor)
}

func (s *annotationStrategy) needsFinalizer() bool { return true }

// contributorKey returns a stable identifier for a contributing primary:
// "Kind/Namespace/Name". Unique within a single manager.
func contributorKey(gvk schema.GroupVersionKind, nn types.NamespacedName) string {
	return fmt.Sprintf("%s/%s/%s", gvk.Kind, nn.Namespace, nn.Name)
}

func newObjectForGVK(scheme *runtime.Scheme, gvk schema.GroupVersionKind, nn types.NamespacedName) (client.Object, error) {
	obj, err := scheme.New(gvk)
	if err != nil {
		return nil, fmt.Errorf("unknown GVK %v: %w", gvk, err)
	}
	cObj, ok := obj.(client.Object)
	if !ok {
		return nil, fmt.Errorf("type for %v does not implement client.Object", gvk)
	}
	cObj.SetName(nn.Name)
	cObj.SetNamespace(nn.Namespace)
	return cObj, nil
}

func getContributors(obj client.Object) []string {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return nil
	}
	raw, ok := annotations[contributorAnnotation]
	if !ok || raw == "" {
		return nil
	}
	var contributors []string
	if err := json.Unmarshal([]byte(raw), &contributors); err != nil {
		return nil
	}
	return contributors
}

func setContributors(obj client.Object, contributors []string) {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	if len(contributors) == 0 {
		delete(annotations, contributorAnnotation)
	} else {
		data, _ := json.Marshal(contributors)
		annotations[contributorAnnotation] = string(data)
	}
	obj.SetAnnotations(annotations)
}

// addContributor adds a contributor to the annotation on the object via
// read-modify-write with optimistic retry on conflict.
func addContributor(ctx context.Context, c client.Client, obj client.Object, contributor string) error {
	for attempt := 0; attempt < maxConflictRetries; attempt++ {
		if err := c.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
			return fmt.Errorf("get for contributor update: %w", err)
		}

		contributors := getContributors(obj)
		for _, existing := range contributors {
			if existing == contributor {
				return nil
			}
		}

		base := obj.DeepCopyObject().(client.Object)
		contributors = append(contributors, contributor)
		setContributors(obj, contributors)

		err := c.Patch(ctx, obj, client.MergeFrom(base))
		if err == nil {
			return nil
		}
		if isConflict(err) {
			continue
		}
		return fmt.Errorf("patch contributor annotation: %w", err)
	}
	return fmt.Errorf("add contributor: exceeded %d conflict retries", maxConflictRetries)
}

// removeContributor removes a contributor from the annotation. Returns true if
// the contributor list is now empty (caller should delete the resource).
func removeContributor(ctx context.Context, c client.Client, obj client.Object, contributor string) (empty bool, err error) {
	if err := c.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("get for contributor removal: %w", err)
	}

	contributors := getContributors(obj)
	var remaining []string
	for _, existing := range contributors {
		if existing != contributor {
			remaining = append(remaining, existing)
		}
	}

	if len(remaining) == len(contributors) {
		return len(contributors) == 0, nil
	}

	base := obj.DeepCopyObject().(client.Object)
	setContributors(obj, remaining)

	for attempt := 0; attempt < maxConflictRetries; attempt++ {
		patchErr := c.Patch(ctx, obj, client.MergeFrom(base))
		if patchErr == nil {
			return len(remaining) == 0, nil
		}
		if isConflict(patchErr) {
			// Re-read and retry the whole operation.
			if err := c.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
				return false, fmt.Errorf("get for contributor removal retry: %w", err)
			}
			contributors = getContributors(obj)
			remaining = nil
			for _, existing := range contributors {
				if existing != contributor {
					remaining = append(remaining, existing)
				}
			}
			base = obj.DeepCopyObject().(client.Object)
			setContributors(obj, remaining)
			continue
		}
		return false, fmt.Errorf("patch contributor annotation: %w", patchErr)
	}
	return false, fmt.Errorf("remove contributor: exceeded %d conflict retries", maxConflictRetries)
}

func isConflict(err error) bool {
	type statusErr interface {
		Status() metav1.Status
	}
	if se, ok := err.(statusErr); ok {
		return se.Status().Reason == metav1.StatusReasonConflict
	}
	return false
}
