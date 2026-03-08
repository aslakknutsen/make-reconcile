# istio-workspace evaluation

Analysis of [istio-workspace](https://github.com/maistra/istio-workspace) controller patterns against make-reconcile.

## What istio-workspace does

istio-workspace manages `Session` CRDs that enable developers to route traffic to modified versions of services in an Istio mesh. When a developer creates a Session targeting a Deployment:

1. **Locate** the target Deployment, its Services, VirtualServices, DestinationRules, and connected Gateways
2. **Clone** the Deployment with modified labels/image
3. **Create** a new DestinationRule subset pointing to the clone
4. **Mutate** existing VirtualServices to add header-based routing rules
5. **Mutate** existing Gateways to add session-prefixed hostnames
6. On deletion, **revert** all mutations and delete created resources

## Architecture pattern

The controller uses a **Locator → Validator → Modificator** pipeline:

- **Locators** discover existing cluster resources by following relationships (Deployment → Service → VirtualService → Gateway)
- **Validators** check preconditions (target found, VirtualService exists, DestinationRule exists)
- **Modificators** create new resources or mutate existing ones

Data flows through a shared `LocatorStore` within a single Reconcile call. Each Locator adds entries that subsequent Locators and Modificators read.

## What fits

~30% of the controller logic maps to make-reconcile.

### Deployment cloning (fits well)

Session → cloned Deployments is a clean 1:N mapping. Fetch the original Deployment, transform it, return the clones. `nil`-means-delete handles cleanup when Refs are removed. The original's template engine is just Go code inside the reconciler.

### DestinationRule creation (fits well)

Session → session-scoped DestinationRules. The original creates a new DR rather than mutating the existing one, so this maps directly to ReconcileMany.

### Gateway-connected VirtualService creation (partial fit)

For VirtualServices connected to a Gateway, the original creates a new VS (not mutating the existing one). This part maps to ReconcileMany. But locating which VirtualServices are gateway-connected requires FetchAll + manual filtering.

### Status computation (partial fit)

The aggregate success/failure state can be a ReconcileStatus. But progressive status updates (the original calls Status().Update() ~10 times per reconcile) are lost.

## What doesn't fit

~70% of the controller logic conflicts with make-reconcile's model.

### VirtualService mutation (fundamental mismatch)

The core routing mechanism adds an HTTP route with a header match to an **existing** VirtualService the controller does not own. SSA would overwrite the entire VirtualService, destroying routes from other controllers. This needs a "foreign resource patch" primitive — apply specific fields without owning the whole resource.

### Gateway host injection (fundamental mismatch)

Same problem. Adding `session.app.example.com` to an existing Gateway's server hosts is a partial mutation of a shared resource.

### Revert semantics (fundamental mismatch)

On deletion, the controller removes the route it added to the VirtualService and the host it added to the Gateway. make-reconcile's deletion model is "delete the output resource", not "undo a partial mutation". The cloned Deployment and session DR are handled fine, but the VirtualService and Gateway reverts have no framework equivalent.

### Dynamic discovery chain (poor fit)

The Locator chain follows relationships: Deployment → Service (by label selector match) → VirtualService (by hostname) → Gateway (by reference). Each step uses the previous step's output. Fetch can't express "find Services whose .spec.selector matches these labels" — you need FetchAll + iterate, which creates broad dependencies and loses precision.

### Multi-Session shared resources (mismatch)

Multiple Sessions can target the same Deployment and add routes to the same VirtualService. The original uses annotation-based ref markers for multi-owner tracking. make-reconcile uses OwnerReferences (single owner, GC semantics) — deleting one Session could GC resources still needed by another.

### Progressive status updates (limitation)

The original updates status after each pipeline step. make-reconcile runs ReconcileStatus once after all outputs. Users lose visibility into intermediate states.

### Validation gating (no equivalent)

The original skips modification if validation fails. make-reconcile sub-reconcilers are independent — no way to gate one reconciler on another's success.

## New features that would help

1. **Foreign resource patch**: Apply specific fields on unowned resources with scoped SSA field managers. Generalizes the "foreign resource status" roadmap item to arbitrary spec fields.

2. **Finalizer management with custom cleanup**: Run user-defined cleanup on primary deletion instead of just deleting outputs. Already on the roadmap.

3. **Multi-owner tracking**: Track multiple primaries that contribute to the same output resource without OwnerReference GC semantics.

4. **Rich Fetch predicates**: Support queries like "resources whose .spec.selector matches these labels" beyond name/namespace/label filters.

## Verdict

istio-workspace is a **poor fit** for make-reconcile in its current form. The controller's core pattern — discover existing resources via a chain, mutate them partially, then revert mutations on cleanup — is fundamentally at odds with the "return desired state for resources you own" model.

The mismatch is structural, not incidental. istio-workspace is a **mutation controller** (modify what exists) while make-reconcile is a **desired-state controller** (declare what should exist). These are different reconciliation paradigms.

The ~30% that fits (Deployment cloning, DR creation) works well and is cleaner than the original. But the ~70% that doesn't fit is the actual value of the controller — the traffic routing logic.
