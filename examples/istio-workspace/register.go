package main

import (
	appsv1 "k8s.io/api/apps/v1"

	mr "github.com/aslakknutsen/make-reconcile"
)

func RegisterAll(mgr *mr.Manager) {
	sessions := mr.Watch[*Session](mgr, mr.WithGenerationChanged())

	deployments := mr.Watch[*appsv1.Deployment](mgr)
	destinationRules := mr.Watch[*DestinationRule](mgr)
	virtualServices := mr.Watch[*VirtualService](mgr)

	// --- What CAN be modeled ---
	//
	// Session → cloned Deployments (one per Ref).
	// The original clones the target Deployment with modified labels and
	// image. Fetch on the original Deployment creates a dependency so
	// changes to it trigger re-cloning. nil-means-delete handles cleanup
	// when a Ref is removed from the Session.
	mr.ReconcileMany(mgr, sessions, ClonedDeploymentReconciler(deployments))

	// Session → DestinationRules (one per Ref).
	// The original creates a new DR with a subset pointing to the cloned
	// pod version. This is a clean 1:N mapping.
	mr.ReconcileMany(mgr, sessions, SessionDestinationRuleReconciler(destinationRules))

	// Session → status (aggregate readiness of cloned resources).
	mr.ReconcileStatus(mgr, sessions, SessionStatusReconciler(deployments, destinationRules))

	// --- What CANNOT be modeled ---
	//
	// These are the core operations of istio-workspace that don't fit.
	// They are listed here to make the gaps explicit.
	//
	// 1. VirtualService mutation (the main traffic routing mechanism):
	//
	//    The original adds a header-match HTTP route to existing
	//    VirtualServices. This is a partial mutation of a resource the
	//    controller does not own. make-reconcile's SSA model would
	//    overwrite the entire VirtualService spec, destroying routes
	//    managed by other controllers or users.
	//
	//    The framework would need a "foreign resource patch" primitive
	//    that applies only specific fields via a scoped field manager.
	//    This is related to the "foreign resource status" roadmap item
	//    but generalized to arbitrary spec fields.
	//
	//    Attempted workaround: create a separate VirtualService per
	//    session. This works for gateway-connected VS (the original does
	//    this too) but not for mesh-internal routing where you must modify
	//    the existing VS to insert the header-match rule ahead of the
	//    default route.
	//
	_ = virtualServices // watched but unused — can't mutate existing VS

	// 2. Gateway host injection:
	//
	//    The original adds session-prefixed hostnames (e.g. "mysession.app.example.com")
	//    to existing Gateway .spec.servers[].hosts. Same problem as #1:
	//    partial mutation of an unowned resource. SSA would clobber other hosts.
	//
	// 3. Finalizer-based revert (not delete):
	//
	//    On Session deletion, the original doesn't just delete created
	//    resources — it reverts mutations. The added VirtualService route
	//    is removed. The added Gateway host is removed. make-reconcile's
	//    deletion model is "delete the output resource entirely", which
	//    works for the cloned Deployment and session DR but not for
	//    reverting partial mutations on shared resources.
	//
	//    The framework would need finalizer management with custom cleanup
	//    callbacks. This is already on the roadmap.
	//
	// 4. Dynamic resource discovery chain:
	//
	//    The original's Locator pipeline discovers resources by following
	//    relationships:
	//      Deployment → (label match) → Service → (hostname) → VirtualService → (gateway ref) → Gateway
	//
	//    Each step uses the output of the previous step. Fetch can look up
	//    resources by name/namespace/label, but "find Services whose
	//    .spec.selector matches these labels" requires FetchAll + manual
	//    filter. The chain works but is verbose and loses the framework's
	//    ability to create precise dependency keys.
	//
	// 5. Service discovery by selector matching:
	//
	//    ServiceLocator finds Services whose selector matches the target
	//    Deployment's pod template labels. This is a reverse label match
	//    (the Service selects the Deployment, not the other way around).
	//    Fetch/FetchAll can filter by the resource's own labels, not by
	//    the resource's selector field. You'd need FetchAll + iterate.
	//
	// 6. Template engine for Deployment transformation:
	//
	//    The original uses a pluggable template engine (Go templates or
	//    JSON patches loaded from files) to transform the cloned
	//    Deployment. This is pure business logic that works inside the
	//    reconciler function, but the "load templates from a file path"
	//    pattern has no framework equivalent. It's just code you'd write
	//    in the reconciler body.
	//
	// 7. Progressive status updates:
	//
	//    The original calls Status().Update() after each locator,
	//    validator, and modificator step — sometimes 10+ times in one
	//    Reconcile call. make-reconcile runs ReconcileStatus once after
	//    all output reconcilers. The user sees the final state, not the
	//    progression. For debugging, this is a meaningful loss.
	//
	// 8. Validation as a gate:
	//
	//    The original runs validators between location and modification.
	//    If validation fails (e.g. no DestinationRule found), modification
	//    is skipped entirely. make-reconcile has no inter-reconciler
	//    gating — each sub-reconciler runs independently. You could check
	//    preconditions inside the reconciler and return nil, but there's no
	//    way to prevent other reconcilers from running.
	//
	// 9. Multi-Session coordination on shared resources:
	//
	//    Multiple Sessions can target the same Deployment. The original
	//    uses annotation-based ref markers to track which Session owns
	//    which mutation. make-reconcile uses OwnerReferences which imply
	//    single ownership and trigger GC — wrong for shared resources
	//    where multiple Sessions add routes to the same VirtualService.
}
