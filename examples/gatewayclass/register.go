package main

import (
	mr "github.com/aslakknutsen/make-reconcile"
)

func RegisterAll(mgr *mr.Manager) {
	gatewayClasses := mr.Watch[*GatewayClass](mgr)
	istios := mr.Watch[*Istio](mgr)

	// GatewayClass -> Istio CR
	//
	// The original controller does this through ensureIstioOLM which
	// manually checks existence, creates or updates, diffs, and logs.
	// Here the framework handles all of that via SSA.
	mr.Reconcile(mgr, gatewayClasses, IstioReconciler)

	// GatewayClass -> status conditions
	//
	// The original embeds status computation inside the main Reconcile
	// method and patches it inline. Here it's a separate reconciler that
	// only re-runs when the Istio CR's status (its dependency) changes.
	mr.ReconcileStatus(mgr, gatewayClasses, StatusReconciler(istios))

	// --- Things from the original controller that are NOT modeled here ---
	//
	// 1. OLM Subscription (ensureServiceMeshOperatorSubscription):
	//    Could be another mr.Reconcile producing a Subscription CR.
	//    Straightforward mapping, same pattern as IstioReconciler.
	//
	// 2. InstallPlan approval (ensureServiceMeshOperatorInstallPlan):
	//    This mutates an existing OLM-created resource rather than
	//    producing a new one. Does not fit the "return desired state" model.
	//    Would need a side-effect hook or manual client call.
	//
	// 3. Sail Library mode (sailInstaller.Apply):
	//    Delegates to an external Helm installer. No k8s object is returned.
	//    Fundamentally imperative — cannot be expressed as a pure function
	//    returning desired state.
	//
	// 4. Migration logic (ensureOSSMtoSailLibraryMigration):
	//    One-time procedural sequence (check, delete, wait, proceed).
	//    Temporal/stateful — doesn't map to declarative reconciliation.
	//
	// 5. Dynamic watch creation (sync.Once for Istio CRD):
	//    The original lazily adds a watch after the CRD is installed.
	//    make-reconcile requires all watches registered before Start().
	//
	// 6. Custom event source (SailLibrarySource channel bridge):
	//    The Sail Library pushes events via a Go channel.
	//    make-reconcile has no concept of external event sources.
	//
	// 7. Predicate-based filtering (isOurGatewayClass, notIstioGatewayClass):
	//    The original filters events to only process GatewayClasses with
	//    a specific controllerName. make-reconcile processes all instances
	//    of a watched type. Filtering would need to be done inside the
	//    reconciler function (early return nil).
}
