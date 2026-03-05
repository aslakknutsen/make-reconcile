package main

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mr "github.com/aslakknutsen/make-reconcile"
)

const (
	spawnerImage          = "ghcr.io/kelos-dev/kelos-spawner:latest"
	spawnerServiceAccount = "kelos-spawner"
	spawnerClusterRole    = "kelos-spawner-role"
)

// DeploymentReconciler produces a Deployment for polling-based TaskSpawners.
// Returns nil for cron-based spawners — make-reconcile will delete any
// previously-created Deployment, handling the Deployment↔CronJob switch.
func DeploymentReconciler(
	workspaces *mr.Collection[*Workspace],
	secrets *mr.Collection[*corev1.Secret],
) func(*mr.HandlerContext, *TaskSpawner) *appsv1.Deployment {
	return func(hc *mr.HandlerContext, ts *TaskSpawner) *appsv1.Deployment {
		if ts.Spec.When.Cron != nil {
			return nil
		}

		workspace := resolveWorkspace(hc, workspaces, ts)

		replicas := int32(1)
		if ts.Spec.Suspend != nil && *ts.Spec.Suspend {
			replicas = 0
		}

		labels := spawnerLabels(ts)
		args := spawnerArgs(ts, workspace)
		env := spawnerEnv(workspace)

		return &appsv1.Deployment{
			TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
			ObjectMeta: metav1.ObjectMeta{
				Name:      ts.Name,
				Namespace: ts.Namespace,
				Labels:    labels,
			},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Selector: &metav1.LabelSelector{MatchLabels: labels},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: labels},
					Spec: corev1.PodSpec{
						ServiceAccountName: spawnerServiceAccount,
						RestartPolicy:      corev1.RestartPolicyAlways,
						Containers: []corev1.Container{{
							Name:  "spawner",
							Image: spawnerImage,
							Args:  args,
							Env:   env,
						}},
					},
				},
			},
		}
	}
}

// CronJobReconciler produces a CronJob for cron-based TaskSpawners.
// Returns nil for polling-based spawners — make-reconcile cleans up stale CronJobs.
func CronJobReconciler(
	workspaces *mr.Collection[*Workspace],
	secrets *mr.Collection[*corev1.Secret],
) func(*mr.HandlerContext, *TaskSpawner) *batchv1.CronJob {
	return func(hc *mr.HandlerContext, ts *TaskSpawner) *batchv1.CronJob {
		if ts.Spec.When.Cron == nil {
			return nil
		}

		workspace := resolveWorkspace(hc, workspaces, ts)
		isSuspended := ts.Spec.Suspend != nil && *ts.Spec.Suspend

		labels := spawnerLabels(ts)
		args := append(spawnerArgs(ts, workspace), "--one-shot")
		env := spawnerEnv(workspace)
		backoff := int32(0)
		successHistory := int32(3)
		failHistory := int32(1)

		return &batchv1.CronJob{
			TypeMeta: metav1.TypeMeta{APIVersion: "batch/v1", Kind: "CronJob"},
			ObjectMeta: metav1.ObjectMeta{
				Name:      ts.Name,
				Namespace: ts.Namespace,
				Labels:    labels,
			},
			Spec: batchv1.CronJobSpec{
				Schedule:                   ts.Spec.When.Cron.Schedule,
				Suspend:                    &isSuspended,
				ConcurrencyPolicy:          batchv1.ForbidConcurrent,
				SuccessfulJobsHistoryLimit: &successHistory,
				FailedJobsHistoryLimit:     &failHistory,
				JobTemplate: batchv1.JobTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: labels},
					Spec: batchv1.JobSpec{
						BackoffLimit: &backoff,
						Template: corev1.PodTemplateSpec{
							ObjectMeta: metav1.ObjectMeta{Labels: labels},
							Spec: corev1.PodSpec{
								ServiceAccountName: spawnerServiceAccount,
								RestartPolicy:      corev1.RestartPolicyNever,
								Containers: []corev1.Container{{
									Name:  "spawner",
									Image: spawnerImage,
									Args:  args,
									Env:   env,
								}},
							},
						},
					},
				},
			},
		}
	}
}

// ServiceAccountReconciler ensures the shared spawner ServiceAccount
// exists in the TaskSpawner's namespace.
func ServiceAccountReconciler(hc *mr.HandlerContext, ts *TaskSpawner) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ServiceAccount"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      spawnerServiceAccount,
			Namespace: ts.Namespace,
		},
	}
}

// RoleBindingReconciler ensures the spawner RoleBinding exists.
func RoleBindingReconciler(hc *mr.HandlerContext, ts *TaskSpawner) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		TypeMeta: metav1.TypeMeta{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "RoleBinding"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      spawnerServiceAccount,
			Namespace: ts.Namespace,
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     spawnerClusterRole,
		},
		Subjects: []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      spawnerServiceAccount,
			Namespace: ts.Namespace,
		}},
	}
}

func resolveWorkspace(hc *mr.HandlerContext, workspaces *mr.Collection[*Workspace], ts *TaskSpawner) *Workspace {
	if ts.Spec.TaskTemplate.WorkspaceRef == nil {
		return nil
	}
	return mr.Fetch(hc, workspaces, mr.FilterName(ts.Spec.TaskTemplate.WorkspaceRef.Name, ts.Namespace))
}

func spawnerLabels(ts *TaskSpawner) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "kelos",
		"app.kubernetes.io/component":  "spawner",
		"app.kubernetes.io/managed-by": "kelos-controller",
		"kelos.dev/taskspawner":        ts.Name,
	}
}

func spawnerArgs(ts *TaskSpawner, ws *Workspace) []string {
	args := []string{
		"--taskspawner-name=" + ts.Name,
		"--taskspawner-namespace=" + ts.Namespace,
	}
	if ws != nil {
		owner, repo := parseGitHubRepo(ws.Spec.Repo)
		args = append(args, "--github-owner="+owner, "--github-repo="+repo)
	}
	return args
}

func spawnerEnv(ws *Workspace) []corev1.EnvVar {
	if ws == nil || ws.Spec.SecretRef == nil {
		return nil
	}
	return []corev1.EnvVar{{
		Name: "GITHUB_TOKEN",
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: ws.Spec.SecretRef.Name},
				Key:                  "GITHUB_TOKEN",
			},
		},
	}}
}

func parseGitHubRepo(repoURL string) (owner, repo string) {
	// Simplified — real implementation handles SSH, HTTPS, GHE
	return "owner", fmt.Sprintf("repo-from-%s", repoURL)
}
