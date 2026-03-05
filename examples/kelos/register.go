package main

import (
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"

	mr "github.com/aslakknutsen/make-reconcile"
)

func RegisterAll(mgr *mr.Manager) {
	// Primary watches
	tasks := mr.Watch[*Task](mgr)
	taskSpawners := mr.Watch[*TaskSpawner](mgr)

	// Dependency watches
	workspaces := mr.Watch[*Workspace](mgr)
	agentConfigs := mr.Watch[*AgentConfig](mgr)
	secrets := mr.Watch[*corev1.Secret](mgr)

	// Output watches (also used in status reconcilers)
	jobs := mr.Watch[*batchv1.Job](mgr)
	deployments := mr.Watch[*appsv1.Deployment](mgr)
	cronJobs := mr.Watch[*batchv1.CronJob](mgr)
	pods := mr.Watch[*corev1.Pod](mgr)

	// ── Task controller ──
	//
	// Task → Job: the core mapping. Workspace and AgentConfig are resolved
	// via Fetch, creating automatic dependency tracking.
	// Dependencies between Tasks are also resolved via Fetch — when a
	// dependency Task reaches Succeeded, the dependent Task's reconciler
	// re-runs and creates the Job. This replaces the original's
	// RequeueAfter polling with event-driven reconciliation.
	mr.Reconcile(mgr, tasks, JobReconciler(tasks, workspaces, agentConfigs, secrets))

	// Task status from Job + Pods + dependency Tasks.
	mr.ReconcileStatus(mgr, tasks, TaskStatusReconciler(jobs, pods, tasks))

	// ── TaskSpawner controller ──
	//
	// The conditional Deployment vs CronJob output is the highlight here.
	// When a TaskSpawner switches from polling to cron (or vice versa),
	// the reconciler for the old type returns nil, and make-reconcile
	// deletes it. No manual deleteStaleResource logic needed.
	mr.Reconcile(mgr, taskSpawners, DeploymentReconciler(workspaces, secrets))
	mr.Reconcile(mgr, taskSpawners, CronJobReconciler(workspaces, secrets))

	// RBAC: ServiceAccount and RoleBinding per TaskSpawner.
	// With SSA, multiple TaskSpawners producing the same-named SA is safe —
	// last writer wins, and the content is identical. But ownership is tricky:
	// each TaskSpawner would set itself as owner, meaning deletion of any one
	// TaskSpawner could GC the shared SA. This is the singleton output problem
	// identified in the gatewayclass analysis.
	mr.Reconcile(mgr, taskSpawners, ServiceAccountReconciler)
	mr.Reconcile(mgr, taskSpawners, RoleBindingReconciler)

	mr.ReconcileStatus(mgr, taskSpawners, TaskSpawnerStatusReconciler(deployments, cronJobs))

	// ── What's NOT modeled ──
	//
	// TaskReconciler gaps (significant — ~60% of original logic is imperative):
	//
	// 1. Branch locking (BranchLocker): In-memory mutual exclusion across Tasks.
	//    No make-reconcile primitive for cross-primary coordination.
	//
	// 2. Pod log reading: readOutputs streams kubelet logs to extract outputs
	//    and results. Not a k8s object — can't Fetch it.
	//
	// 3. TTL-based self-deletion: Controller deletes its own Task after
	//    TTLSecondsAfterFinished expires. make-reconcile deletes outputs,
	//    not primaries.
	//
	// 4. GitHub App token refresh: Creates a derived Secret with a short-lived
	//    token. The refresh cycle doesn't fit the declarative model.
	//
	// 5. Cycle detection in dependency graph: walkDeps traverses the Task
	//    dependency DAG. Pure computation, but coupled to the "fail early"
	//    imperative — could be modeled as a status condition but awkwardly.
	//
	// 6. Prometheus metrics: task_created_total, task_duration_seconds, etc.
	//    No hook point in make-reconcile for emitting metrics.
	//
	// 7. Event recording: Kubernetes Events for lifecycle transitions.
	//    Same gap as identified in gatewayclass and coraza analyses.
	//
	// TaskSpawnerReconciler gaps (minor — ~90% maps cleanly):
	//
	// 1. Workspace → Secret resolution chain: The original checks if the
	//    Secret is a GitHub App secret to decide on token-refresher sidecar.
	//    Expressible via Fetch (Workspace → Secret), but the GitHub App
	//    detection is imperative logic inside the reconciler.
	//
	// 2. Manual diff logic: updateDeployment/updateCronJob compare container
	//    specs field-by-field. SSA eliminates this entirely — just apply the
	//    desired state.
	//
	// 3. Event recording: Same as above.
	//
	// 4. Singleton RBAC: ensureSpawnerRBAC creates namespace-scoped singletons
	//    shared across all TaskSpawners. The owner reference problem means
	//    deleting one TaskSpawner could GC resources needed by others.
}

// unused prevents "imported and not used" for rbacv1
var _ = rbacv1.GroupName
