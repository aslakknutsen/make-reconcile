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
)

// Watch a resource type.
col := mr.Watch[*corev1.Pod](mgr)

// Register a 1:1 sub-reconciler.
mr.Reconcile[P, T](mgr, primaryCollection, func(hc *mr.HandlerContext, p P) T { ... })

// Register a 1:N sub-reconciler.
mr.ReconcileMany[P, T](mgr, primaryCollection, func(hc *mr.HandlerContext, p P) []T { ... })

// Inside a sub-reconciler: fetch a single dependency.
obj := mr.Fetch[T](hc, collection, mr.FilterName("name", "namespace"))

// Inside a sub-reconciler: fetch all matching.
objs := mr.FetchAll[T](hc, collection, mr.FilterNamespace("default"))

// Filters.
mr.FilterName(name, namespace)
mr.FilterNamespace(namespace)
mr.FilterLabel(map[string]string{"app": "foo"})

// Start the manager (blocks until ctx is cancelled).
mgr.Start(ctx)
```

## Future Enhancements

These are tracked directions for the project. Contributions welcome.

- **Status sub-reconcilers**: sub-reconcilers that compute status fields from observed sub-resource state, writing back to the primary resource's status subresource
- **Conditional watches**: only start informers for GVKs when a feature is enabled in the CR, reducing memory/API load for optional components
- **Dry-run mode**: return the planned mutations without applying, useful for debugging, testing, and CI validation
- **Metrics and tracing**: OpenTelemetry integration per sub-reconciler — latency, re-run count, apply count, dependency graph size
- **Dependency graph visualization**: auto-generate mermaid diagrams from the tracker at runtime, exposable via a debug endpoint
- **Finalizer management**: framework-managed finalizers for cleanup of external resources on primary deletion
- **Health/readiness integration**: expose sub-reconciler health as a manager health signal, surfaceable via standard health endpoints
- **Rate limiting and backoff**: per sub-reconciler rate limiting on re-runs to prevent thundering herds
- **Diff reporting**: surface what changed between desired and actual state for debugging, e.g. via structured logging or events
- **Multi-cluster support**: Watch/Fetch across multiple clusters for multi-cluster operator patterns
- **Rust port**: explore Rust's type system for better ergonomics (Istio's KRT is exploring this too — derive macros for Equals/Key, trait-based Collection)
