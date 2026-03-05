package main

import (
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mr "github.com/aslakknutsen/make-reconcile"
)

const (
	defaultClaudeImage = "ghcr.io/kelos-dev/claude-code:latest"
	workspaceMountPath = "/workspace"
	agentUID           = int64(61100)
)

// JobReconciler produces a Job for each Task. Returns nil when the Task
// has already reached a terminal phase or when dependencies haven't
// succeeded yet — make-reconcile will delete the Job in the former case
// and simply skip creation in the latter.
func JobReconciler(
	tasks *mr.Collection[*Task],
	workspaces *mr.Collection[*Workspace],
	agentConfigs *mr.Collection[*AgentConfig],
	secrets *mr.Collection[*corev1.Secret],
) func(*mr.HandlerContext, *Task) *batchv1.Job {
	return func(hc *mr.HandlerContext, task *Task) *batchv1.Job {
		if task.Status.Phase == TaskPhaseSucceeded || task.Status.Phase == TaskPhaseFailed {
			return nil
		}

		// Dependency resolution via Fetch — when any dep Task changes,
		// this reconciler re-runs automatically.
		for _, depName := range task.Spec.DependsOn {
			dep := mr.Fetch(hc, tasks, mr.FilterName(depName, task.Namespace))
			if dep == nil {
				return nil
			}
			switch dep.Status.Phase {
			case TaskPhaseSucceeded:
				continue
			case TaskPhaseFailed:
				// Could set status via ReconcileStatus, but can't create the Job.
				return nil
			default:
				return nil
			}
		}

		var workspace *Workspace
		if task.Spec.WorkspaceRef != nil {
			workspace = mr.Fetch(hc, workspaces, mr.FilterName(task.Spec.WorkspaceRef.Name, task.Namespace))
			if workspace == nil {
				return nil
			}
		}

		var agentConfig *AgentConfig
		if task.Spec.AgentConfigRef != nil {
			agentConfig = mr.Fetch(hc, agentConfigs, mr.FilterName(task.Spec.AgentConfigRef.Name, task.Namespace))
			if agentConfig == nil {
				return nil
			}
		}

		return buildJob(task, workspace, agentConfig)
	}
}

func buildJob(task *Task, workspace *Workspace, agentConfig *AgentConfig) *batchv1.Job {
	image := defaultClaudeImage
	if task.Spec.Image != "" {
		image = task.Spec.Image
	}

	var envVars []corev1.EnvVar
	if task.Spec.Model != "" {
		envVars = append(envVars, corev1.EnvVar{Name: "KELOS_MODEL", Value: task.Spec.Model})
	}
	if task.Spec.Branch != "" {
		envVars = append(envVars, corev1.EnvVar{Name: "KELOS_BRANCH", Value: task.Spec.Branch})
	}

	envVars = append(envVars, corev1.EnvVar{
		Name: apiKeyEnvForType(task.Spec.Type),
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: task.Spec.Credentials.SecretRef.Name},
				Key:                  apiKeyEnvForType(task.Spec.Type),
			},
		},
	})

	mainContainer := corev1.Container{
		Name:    task.Spec.Type,
		Image:   image,
		Command: []string{"/kelos_entrypoint.sh"},
		Args:    []string{task.Spec.Prompt},
		Env:     envVars,
	}

	var initContainers []corev1.Container
	var volumes []corev1.Volume
	uid := agentUID
	var podSec *corev1.PodSecurityContext

	if workspace != nil {
		podSec = &corev1.PodSecurityContext{FSGroup: &uid}
		volumes = append(volumes, corev1.Volume{
			Name:         "workspace",
			VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		})
		mount := corev1.VolumeMount{Name: "workspace", MountPath: workspaceMountPath}

		cloneArgs := []string{"clone", "--no-single-branch", "--depth", "1", "--", workspace.Spec.Repo, workspaceMountPath + "/repo"}
		initContainers = append(initContainers, corev1.Container{
			Name:         "git-clone",
			Image:        "alpine/git:v2.47.2",
			Args:         cloneArgs,
			VolumeMounts: []corev1.VolumeMount{mount},
			SecurityContext: &corev1.SecurityContext{RunAsUser: &uid},
		})

		mainContainer.VolumeMounts = []corev1.VolumeMount{mount}
		mainContainer.WorkingDir = workspaceMountPath + "/repo"
	}

	if agentConfig != nil && agentConfig.Spec.AgentsMD != "" {
		mainContainer.Env = append(mainContainer.Env, corev1.EnvVar{
			Name:  "KELOS_AGENTS_MD",
			Value: agentConfig.Spec.AgentsMD,
		})
	}

	backoff := int32(1)
	labels := map[string]string{
		"app.kubernetes.io/name":       "kelos",
		"app.kubernetes.io/component":  "task",
		"app.kubernetes.io/managed-by": "kelos-controller",
		"kelos.dev/task":               task.Name,
	}

	return &batchv1.Job{
		TypeMeta: metav1.TypeMeta{APIVersion: "batch/v1", Kind: "Job"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      task.Name,
			Namespace: task.Namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoff,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					RestartPolicy:   corev1.RestartPolicyNever,
					SecurityContext: podSec,
					InitContainers:  initContainers,
					Volumes:         volumes,
					Containers:      []corev1.Container{mainContainer},
				},
			},
		},
	}
}

func apiKeyEnvForType(agentType string) string {
	switch agentType {
	case "codex":
		return "CODEX_API_KEY"
	case "gemini":
		return "GEMINI_API_KEY"
	default:
		return "ANTHROPIC_API_KEY"
	}
}

// NOTE: The following aspects of the original TaskReconciler cannot be
// modeled with make-reconcile and would need to remain imperative:
//
// - Branch locking: In-memory BranchLocker coordinates across Task instances.
//   make-reconcile has no cross-primary coordination primitive. The Fetch on
//   other Tasks gives dependency tracking but not mutual exclusion.
//
// - Pod log reading: readOutputs streams logs from the kubelet API to extract
//   structured results. This is a runtime API call, not a k8s object read.
//
// - TTL-based self-deletion: The controller deletes its own primary (Task)
//   after TTL expires. make-reconcile only deletes outputs, not primaries.
//
// - GitHub App token generation: Creates a short-lived Secret as a side effect.
//   Could model as a separate Reconcile(Task → Secret), but the token has
//   external TTL and needs refresh logic that doesn't fit the declarative model.
//
// - Prompt template resolution: Reads dependency Task outputs to expand Go
//   templates in the prompt. This couples Job creation to runtime data from
//   other Task statuses — expressible via Fetch, but the template execution
//   is imperative logic that would live inside the reconciler function.
//
// - Prometheus metrics: Counters and histograms for cost/tokens/duration.
//   make-reconcile has no metrics integration point.
//
// - Finalizer management: Used for branch lock cleanup on deletion. With
//   make-reconcile's owner references, Job GC is handled automatically, but
//   branch lock release needs custom deletion logic.
func _ () {} // anchor for the note above
