package makereconcile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const patchFieldManagerPrefix = "mr-patch/"

// patchStrategy defines how a foreign-resource contribution is applied and
// reverted. Different CRDs need different strategies depending on their
// x-kubernetes-list-type declarations.
type patchStrategy interface {
	// Apply merges the contribution into the target resource.
	Apply(ctx context.Context, c client.Client, scheme *runtime.Scheme,
		targetGVK schema.GroupVersionKind, targetKey types.NamespacedName,
		contribution client.Object, patchID string) error

	// Revert removes the contribution from the target resource.
	Revert(ctx context.Context, c client.Client, scheme *runtime.Scheme,
		targetGVK schema.GroupVersionKind, targetKey types.NamespacedName,
		patchID string) error
}

// ssaPatchStrategy uses server-side apply with a unique field manager per
// contribution. Works when the target CRD has x-kubernetes-list-type: map on
// contributed list fields. ForceOwnership is used so the controller asserts
// ownership of its contributed fields while other field managers retain theirs.
type ssaPatchStrategy struct{}

func (s *ssaPatchStrategy) Apply(ctx context.Context, c client.Client, _ *runtime.Scheme,
	_ schema.GroupVersionKind, _ types.NamespacedName,
	contribution client.Object, patchID string) error {
	fieldMgr := patchFieldManagerName(patchID)
	return c.Patch(ctx, contribution, client.Apply,
		client.FieldOwner(fieldMgr),
		client.ForceOwnership,
	)
}

func (s *ssaPatchStrategy) Revert(ctx context.Context, c client.Client, scheme *runtime.Scheme,
	targetGVK schema.GroupVersionKind, targetKey types.NamespacedName,
	patchID string) error {
	fieldMgr := patchFieldManagerName(patchID)

	// Apply a minimal object (apiVersion + kind + metadata only) with the same
	// field manager. This releases ownership of all previously managed fields.
	empty := emptyUnstructuredForGVK(targetGVK, targetKey)
	return c.Patch(ctx, empty, client.Apply,
		client.FieldOwner(fieldMgr),
		client.ForceOwnership,
	)
}

// patchFieldManagerName builds a field manager name from the patchID.
// Format: "mr-patch/<patchID>". Truncated with a hash if over 128 chars.
func patchFieldManagerName(patchID string) string {
	name := patchFieldManagerPrefix + patchID
	if len(name) <= 128 {
		return name
	}
	h := sha256.Sum256([]byte(patchID))
	hash := hex.EncodeToString(h[:8])
	// 3 ("...") + 16 (hex) = 19 chars suffix
	return name[:128-19] + "..." + hash
}

// patchID builds a stable identifier for a specific contribution:
// "<managerID>/<reconcilerID>/<primaryKey>".
func patchID(managerID, reconcilerID string, primaryKey types.NamespacedName) string {
	return fmt.Sprintf("%s/%s/%s", managerID, reconcilerID, primaryKey.String())
}

func emptyUnstructuredForGVK(gvk schema.GroupVersionKind, nn types.NamespacedName) *unstructured.Unstructured {
	apiVersion, kind := gvk.ToAPIVersionAndKind()
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": apiVersion,
			"kind":       kind,
			"metadata": map[string]interface{}{
				"name":      nn.Name,
				"namespace": nn.Namespace,
			},
		},
	}
}
