package main

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	mr "github.com/aslakknutsen/make-reconcile"
)

// TaskStatusReconciler derives Task status from the owned Job and its Pods.
// When the Job or Pods change, this re-runs via Fetch dependency tracking.
func TaskStatusReconciler(
	jobs *mr.Collection[*batchv1.Job],
	pods *mr.Collection[*corev1.Pod],
	tasks *mr.Collection[*Task],
) func(*mr.HandlerContext, *Task) *TaskStatus {
	return func(hc *mr.HandlerContext, task *Task) *TaskStatus {
		// Dependency-based waiting: check dep status and report why we're blocked.
		for _, depName := range task.Spec.DependsOn {
			dep := mr.Fetch(hc, tasks, mr.FilterName(depName, task.Namespace))
			if dep == nil {
				return &TaskStatus{
					Phase:   TaskPhaseWaiting,
					Message: fmt.Sprintf("Waiting for dependency %q to be created", depName),
				}
			}
			if dep.Status.Phase == TaskPhaseFailed {
				return &TaskStatus{
					Phase:   TaskPhaseFailed,
					Message: fmt.Sprintf("Dependency %q failed", depName),
				}
			}
			if dep.Status.Phase != TaskPhaseSucceeded {
				return &TaskStatus{
					Phase:   TaskPhaseWaiting,
					Message: fmt.Sprintf("Waiting for dependency %q", depName),
				}
			}
		}

		job := mr.Fetch(hc, jobs, mr.FilterName(task.Name, task.Namespace))
		if job == nil {
			return &TaskStatus{Phase: TaskPhasePending, Message: "Job not yet created"}
		}

		// Discover Pod name
		podName := task.Status.PodName
		if podName == "" {
			matchingPods := mr.FetchAll(hc, pods,
				mr.FilterNamespace(task.Namespace),
				mr.FilterLabel(map[string]string{"kelos.dev/task": task.Name}),
			)
			if len(matchingPods) > 0 {
				podName = matchingPods[0].Name
			}
		}

		if job.Status.Active > 0 {
			return &TaskStatus{
				Phase:   TaskPhaseRunning,
				JobName: job.Name,
				PodName: podName,
			}
		}

		if job.Status.Succeeded > 0 {
			return &TaskStatus{
				Phase:   TaskPhaseSucceeded,
				JobName: job.Name,
				PodName: podName,
				Message: "Task completed successfully",
				// NOTE: Outputs and Results cannot be populated here — they
				// require reading Pod logs via the kubelet API, which is not
				// a k8s object and can't be fetched via make-reconcile.
			}
		}

		for _, c := range job.Status.Conditions {
			if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
				return &TaskStatus{
					Phase:   TaskPhaseFailed,
					JobName: job.Name,
					PodName: podName,
					Message: "Task failed",
				}
			}
		}

		return &TaskStatus{
			Phase:   TaskPhasePending,
			JobName: job.Name,
			PodName: podName,
		}
	}
}

// TaskSpawnerStatusReconciler derives TaskSpawner status from its
// Deployment or CronJob.
func TaskSpawnerStatusReconciler(
	deployments *mr.Collection[*appsv1.Deployment],
	cronJobs *mr.Collection[*batchv1.CronJob],
) func(*mr.HandlerContext, *TaskSpawner) *TaskSpawnerStatus {
	return func(hc *mr.HandlerContext, ts *TaskSpawner) *TaskSpawnerStatus {
		isSuspended := ts.Spec.Suspend != nil && *ts.Spec.Suspend

		if ts.Spec.When.Cron != nil {
			cj := mr.Fetch(hc, cronJobs, mr.FilterName(ts.Name, ts.Namespace))
			if cj == nil {
				return &TaskSpawnerStatus{Phase: TaskSpawnerPhasePending}
			}
			phase := TaskSpawnerPhaseRunning
			msg := ""
			if isSuspended {
				phase = TaskSpawnerPhaseSuspended
				msg = "Suspended by user"
			}
			return &TaskSpawnerStatus{
				Phase:       phase,
				CronJobName: cj.Name,
				Message:     msg,
			}
		}

		deploy := mr.Fetch(hc, deployments, mr.FilterName(ts.Name, ts.Namespace))
		if deploy == nil {
			return &TaskSpawnerStatus{Phase: TaskSpawnerPhasePending}
		}
		phase := TaskSpawnerPhaseRunning
		msg := ""
		if isSuspended {
			phase = TaskSpawnerPhaseSuspended
			msg = "Suspended by user"
		}
		return &TaskSpawnerStatus{
			Phase:          phase,
			DeploymentName: deploy.Name,
			Message:        msg,
		}
	}
}
