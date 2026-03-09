# make-reconcile

Think Make, but for Kubernetes reconciliation.

Not the task/phony-based Make, but core Make: if a file changes and something depends on it, we know how to rebuild it. Applied to Kubernetes: if a sub-resource changes and we know the dependency graph, we reconcile only what's affected — not everything.

## The Problem

Standard Kubernetes controllers watch a primary resource and all its related resources. When *any* watched resource changes, the reconciler is invoked and typically re-checks the state of *every* sub-resource. For simple controllers this is fine. For large operators managing 100+ resources (think OpenShift AI, Istio, cluster-wide platform operators), this means:

- High API server load from redundant GET/LIST calls
- Reconciler latency proportional to total resource count, not change count
- Code complexity from a monolithic reconcile function handling every resource type

## The Approach

`make-reconcile` flips the model. Instead of one `Reconcile()` function that handles everything, you register **sub-reconcilers** — small pure functions that each produce the desired state of one output resource type. Dependencies between sub-reconcilers are tracked automatically via `Fetch`, inspired by [Istio's KRT framework](https://github.com/istio/istio/tree/master/pkg/kube/krt).

When a resource changes, the framework knows exactly which sub-reconcilers depend on it and re-runs only those. A periodic full resync still runs as a safety net.

## Quick Start

```go
package main

import (
    appsv1 "k8s.io/api/apps/v1"
    corev1 "k8s.io/api/core/v1"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

    mr "github.com/aslakknutsen/make-reconcile"
)

func main() {
    mgr, _ := mr.NewManager(cfg, scheme)

    apps := mr.Watch[*MyApp](mgr)
    configMaps := mr.Watch[*corev1.ConfigMap](mgr)

    // "To make the Deployment for this MyApp:"
    mr.Reconcile(mgr, apps, func(hc *mr.HandlerContext, app *MyApp) *appsv1.Deployment {
        // Fetch a dependency — tracked automatically.
        // If this ConfigMap changes, only this sub-reconciler re-runs.
        cm := mr.Fetch(hc, configMaps, mr.FilterName(app.Spec.ConfigRef, app.Namespace))
        _ = cm

        return &appsv1.Deployment{
            ObjectMeta: metav1.ObjectMeta{
                Name:      app.Name,
                Namespace: app.Namespace,
            },
            Spec: deploymentSpec(app),
        }
    })

    // "To make the Service for this MyApp:"
    mr.Reconcile(mgr, apps, func(hc *mr.HandlerContext, app *MyApp) *corev1.Service {
        return &corev1.Service{
            ObjectMeta: metav1.ObjectMeta{
                Name:      app.Name,
                Namespace: app.Namespace,
            },
            Spec: serviceSpec(app),
        }
    })

    mgr.Start(ctx)
}
```

## Core Concepts

### Watch

`Watch[T](mgr)` registers a GVK with the manager's cache and returns a `Collection` handle. Use it for your primary resource and any resource you need to `Fetch` inside sub-reconcilers.

### Reconcile / ReconcileMany

`Reconcile[P, T](mgr, primary, fn)` registers a sub-reconciler. The function receives the primary resource and returns the desired state of one output resource. The framework handles:

- Server-side apply (create or update)
- OwnerReference injection
- Deletion when the function returns `nil`

`ReconcileMany` is the same but the function returns `[]T` for one-to-many mappings.

### Fetch

`Fetch[T](hc, collection, filters...)` reads a resource from the cache inside a sub-reconciler. The framework tracks this call. If the fetched resource changes later, this sub-reconciler is automatically re-invoked — no manual watch wiring needed.

`FetchAll[T](hc, collection, filters...)` fetches all matching resources with a broader dependency scope.

### Nil Means Delete

Returning `nil` from a sub-reconciler signals that the output resource should not exist. If it previously existed, the framework deletes it. This makes conditional resources trivial:

```go
mr.Reconcile(mgr, apps, func(hc *mr.HandlerContext, app *MyApp) *appsv1.Deployment {
    if !app.Spec.EnableWorker {
        return nil
    }
    return &appsv1.Deployment{/* worker deployment */}
})
```

### ReconcileStatus

`ReconcileStatus[P, S](mgr, primary, fn)` registers a status reconciler. The function receives the primary resource and returns a status struct `*S`. The framework JSON-marshals it and patches the primary's `.status` subresource via server-side apply. Return `nil` to skip the status write.

Status reconcilers run **after** all output reconcilers for the same primary, so Fetch calls on output resources reflect the latest applied state (subject to informer lag).

```go
mr.ReconcileStatus(mgr, apps, func(hc *mr.HandlerContext, app *MyApp) *MyAppStatus {
    deploy := mr.Fetch(hc, deployments, mr.FilterName(app.Name, app.Namespace))
    if deploy != nil && deploy.Status.ReadyReplicas >= app.Spec.Replicas {
        return &MyAppStatus{Ready: true}
    }
    return &MyAppStatus{Ready: false}
})
```

### ReconcilePatch / ReconcilePatchMany

`ReconcilePatch[P, T](mgr, primary, target, fn)` registers a sub-reconciler that contributes fields to a **foreign resource** — one you don't own. The function returns a partial object whose Name/Namespace identify the target. The framework applies only the contributed fields via server-side apply with a reconciler-specific field manager, so multiple controllers can write to the same target without conflict.

No OwnerReference or managed-by label is set on the target. On primary deletion, the framework automatically reverts contributions (releases field ownership) before removing the finalizer.

`ReconcilePatchMany` is the same for the one-to-many case.

```go
gateways := mr.Watch[*gwv1.Gateway](mgr)

mr.ReconcilePatch(mgr, apps, gateways, func(hc *mr.HandlerContext, app *MyApp) *gwv1.Gateway {
    if app.Spec.RouteHost == "" {
        return nil // remove contribution
    }
    return &gwv1.Gateway{
        ObjectMeta: metav1.ObjectMeta{
            Name:      "shared-gateway",
            Namespace: "istio-system",
        },
        Spec: gwv1.GatewaySpec{
            Listeners: []gwv1.Listener{{
                Name:     gwv1.SectionName(app.Name),
                Hostname: hostPtr(app.Spec.RouteHost),
                Port:     443,
                Protocol: gwv1.HTTPSProtocolType,
            }},
        },
    }
})
```

### OnDelete (Finalizer Management)

`OnDelete[P](mgr, primary, fn)` registers a cleanup callback that runs when a primary resource is being deleted. The framework automatically manages a finalizer on the primary: it adds the finalizer on first reconciliation and removes it after the cleanup callback succeeds.

The callback receives a `HandlerContext` so it can `Fetch` resources needed for cleanup. Return `nil` to indicate success (finalizer removed). Return an error to retry on the next event (finalizer stays).

Only one `OnDelete` registration is allowed per primary GVK. Calling `OnDelete` again for the same primary type replaces the previous callback.

```go
mr.OnDelete(mgr, platforms, func(hc *mr.HandlerContext, p *Platform) error {
    vs := mr.Fetch(hc, virtualServices, mr.FilterName(p.Name+"-vs", p.Namespace))
    if vs != nil {
        // revert injected route
    }
    return nil
})
```

### Watch and Reconcile Predicates

Watch predicates filter informer events before they reach the dependency tracker. Use `WithGenerationChanged()` to suppress status-only updates (breaks the status write feedback loop) or `WithEventPredicate()` for custom filtering.

```go
apps := mr.Watch[*MyApp](mgr, mr.WithGenerationChanged())
```

Reconcile predicates filter at the sub-reconciler level. `WithPredicate()` runs after fetching the primary but before calling the reconciler function. If it returns false, the reconciler is skipped and stale outputs are cleaned up.

```go
mr.Reconcile(mgr, apps, func(hc *mr.HandlerContext, app *MyApp) *appsv1.Deployment {
    return &appsv1.Deployment{/* ... */}
}, mr.WithPredicate(func(app *MyApp) bool {
    return app.Spec.Tier == "premium"
}))
```

### Kubernetes Events

When configured with `WithEventRecorder()`, the framework emits Kubernetes events on primary resources for successful applies, stale output deletions, and reconciliation errors. Sub-reconcilers can also emit custom events via `HandlerContext.RecordEvent`.

```go
mgr, _ := mr.NewManager(cfg, scheme, mr.WithEventRecorder(recorder))

mr.Reconcile(mgr, apps, func(hc *mr.HandlerContext, app *MyApp) *appsv1.Deployment {
    cm := mr.Fetch(hc, configMaps, mr.FilterName(app.Spec.ConfigRef, app.Namespace))
    if cm == nil {
        hc.RecordEvent("Warning", "ConfigMapNotFound", "config %s not found", app.Spec.ConfigRef)
        return nil
    }
    return &appsv1.Deployment{/* ... */}
})
```

### Pending Watches (Missing CRDs)

If a CRD is not installed at startup, the manager defers its watch and retries registration in the background. This avoids hard failures when optional CRDs are installed later. Configure the retry interval with `WithPendingWatchRetryInterval()`.

### Multi-Owner Tracking

`WithAnnotationOwnership()` switches from OwnerReferences to annotation-based contributor tracking. Instead of relying on Kubernetes GC, the framework maintains a contributor list in an annotation on the output resource. The output is only deleted when the last contributor is removed.

This is useful when multiple primaries produce the same output (e.g. shared ConfigMaps, shared Gateway listeners). The framework adds finalizers automatically for cleanup. Composes with `OnDelete` callbacks.

Use `WithManagerID()` to scope orphan GC when running multiple independent managers in the same cluster.

```go
mgr, _ := mr.NewManager(cfg, scheme, mr.WithAnnotationOwnership())
```

### Full Resync

A periodic full reconcile walks all primary resources and re-runs all sub-reconcilers regardless of events. This is the safety net — if an event is missed, the full resync catches drift. Configurable via `WithResyncPeriod()`.

## How It Maps to Make

| Make | make-reconcile |
|------|---------------|
| Target file | Output resource (Deployment, Service, ...) |
| Prerequisites | `Fetch` dependencies |
| Recipe | Sub-reconciler function |
| `make` checks timestamps | Framework tracks Fetch dependencies |
| Incremental rebuild | Targeted sub-reconciler re-run |
| `make clean && make` | Periodic full resync |

## API Reference

```go
// Create a manager.
mgr, err := mr.NewManager(cfg, scheme,
    mr.WithResyncPeriod(10*time.Minute),
    mr.WithLogger(logger),
    mr.WithManagerID("my-controller"),
    mr.WithEventRecorder(recorder),
    mr.WithAnnotationOwnership(),
    mr.WithPendingWatchRetryInterval(30*time.Second),
)

// Watch a resource type, optionally with predicates.
pods := mr.Watch[*corev1.Pod](mgr)
apps := mr.Watch[*MyApp](mgr, mr.WithGenerationChanged())
configMaps := mr.Watch[*corev1.ConfigMap](mgr, mr.WithEventPredicate(mr.EventPredicate{
    Update: func(old, new client.Object) bool { return old.GetResourceVersion() != new.GetResourceVersion() },
}))

// Register a 1:1 sub-reconciler (with optional predicate).
mr.Reconcile[P, T](mgr, primary, func(hc *mr.HandlerContext, p P) T { ... })
mr.Reconcile[P, T](mgr, primary, fn, mr.WithPredicate(func(p P) bool { ... }))

// Register a 1:N sub-reconciler.
mr.ReconcileMany[P, T](mgr, primary, func(hc *mr.HandlerContext, p P) []T { ... })

// Register a status reconciler (runs after output reconcilers).
mr.ReconcileStatus[P, S](mgr, primary, func(hc *mr.HandlerContext, p P) *S { ... })

// Register a foreign-resource patch reconciler (scoped SSA, no OwnerRef).
mr.ReconcilePatch[P, T](mgr, primary, target, func(hc *mr.HandlerContext, p P) T { ... })
mr.ReconcilePatchMany[P, T](mgr, primary, target, func(hc *mr.HandlerContext, p P) []T { ... })

// Register a cleanup callback with automatic finalizer management.
mr.OnDelete[P](mgr, primary, func(hc *mr.HandlerContext, p P) error { ... })

// Inside a sub-reconciler: fetch a single dependency.
obj := mr.Fetch[T](hc, collection, mr.FilterName("name", "namespace"))

// Inside a sub-reconciler: fetch all matching.
objs := mr.FetchAll[T](hc, collection, mr.FilterNamespace("default"))

// Inside a sub-reconciler: emit a Kubernetes event on the primary.
hc.RecordEvent("Warning", "ConfigMissing", "config %s not found", name)

// Filters.
mr.FilterName(name, namespace)
mr.FilterNamespace(namespace)
mr.FilterLabel(map[string]string{"app": "foo"})

// Start the manager (blocks until ctx is cancelled).
mgr.Start(ctx)
```

## Future Enhancements

These are tracked directions for the project. Contributions welcome.

- **Foreign resource status** (e.g. Gateway API policy attachment): write status entries to foreign resources via `c.Status().Patch(...)` with field-manager-scoped SSA. `ReconcilePatch` currently applies to the main resource only; status-subresource SSA on resources you don't own is not yet supported.
- **Conditional watches**: only start informers for GVKs when a feature is enabled in the CR, reducing memory/API load for optional components
- **Dry-run mode**: return the planned mutations without applying, useful for debugging, testing, and CI validation
- **Metrics and tracing**: OpenTelemetry integration per sub-reconciler — latency, re-run count, apply count, dependency graph size
- **Dependency graph visualization**: auto-generate mermaid diagrams from the tracker at runtime, exposable via a debug endpoint
- **Health/readiness integration**: expose sub-reconciler health as a manager health signal, surfaceable via standard health endpoints
- **Rate limiting and backoff**: per sub-reconciler rate limiting on re-runs to prevent thundering herds
- **Diff reporting**: surface what changed between desired and actual state for debugging, e.g. via structured logging or events
- **Multi-cluster support**: Watch/Fetch across multiple clusters for multi-cluster operator patterns
- **Rust port**: explore Rust's type system for better ergonomics (Istio's KRT is exploring this too — derive macros for Equals/Key, trait-based Collection)
