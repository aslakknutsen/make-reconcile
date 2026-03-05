package main

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var KelosGV = schema.GroupVersion{Group: "kelos.dev", Version: "v1alpha1"}

// Task

type TaskPhase string

const (
	TaskPhasePending   TaskPhase = "Pending"
	TaskPhaseRunning   TaskPhase = "Running"
	TaskPhaseSucceeded TaskPhase = "Succeeded"
	TaskPhaseFailed    TaskPhase = "Failed"
	TaskPhaseWaiting   TaskPhase = "Waiting"
)

type Task struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              TaskSpec   `json:"spec,omitempty"`
	Status            TaskStatus `json:"status,omitempty"`
}

type TaskSpec struct {
	Type           string              `json:"type"`
	Prompt         string              `json:"prompt"`
	Model          string              `json:"model,omitempty"`
	Image          string              `json:"image,omitempty"`
	Branch         string              `json:"branch,omitempty"`
	DependsOn      []string            `json:"dependsOn,omitempty"`
	Credentials    Credentials         `json:"credentials"`
	WorkspaceRef   *WorkspaceReference `json:"workspaceRef,omitempty"`
	AgentConfigRef *AgentConfigRef     `json:"agentConfigRef,omitempty"`
}

type Credentials struct {
	Type      string          `json:"type"`
	SecretRef SecretReference `json:"secretRef"`
}

type SecretReference struct {
	Name string `json:"name"`
}

type WorkspaceReference struct {
	Name string `json:"name"`
}

type AgentConfigRef struct {
	Name string `json:"name"`
}

type TaskStatus struct {
	Phase          TaskPhase         `json:"phase,omitempty"`
	JobName        string            `json:"jobName,omitempty"`
	PodName        string            `json:"podName,omitempty"`
	StartTime      *metav1.Time      `json:"startTime,omitempty"`
	CompletionTime *metav1.Time      `json:"completionTime,omitempty"`
	Message        string            `json:"message,omitempty"`
	Outputs        []string          `json:"outputs,omitempty"`
	Results        map[string]string `json:"results,omitempty"`
}

func (t *Task) DeepCopyObject() runtime.Object {
	c := *t
	return &c
}

type TaskList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Task `json:"items"`
}

func (l *TaskList) DeepCopyObject() runtime.Object {
	c := *l
	return &c
}

// TaskSpawner

type TaskSpawnerPhase string

const (
	TaskSpawnerPhasePending   TaskSpawnerPhase = "Pending"
	TaskSpawnerPhaseRunning   TaskSpawnerPhase = "Running"
	TaskSpawnerPhaseSuspended TaskSpawnerPhase = "Suspended"
)

type TaskSpawner struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              TaskSpawnerSpec   `json:"spec,omitempty"`
	Status            TaskSpawnerStatus `json:"status,omitempty"`
}

type TaskSpawnerSpec struct {
	When         When         `json:"when"`
	TaskTemplate TaskTemplate `json:"taskTemplate"`
	Suspend      *bool        `json:"suspend,omitempty"`
}

type When struct {
	GitHubIssues *GitHubIssues `json:"githubIssues,omitempty"`
	Cron         *Cron         `json:"cron,omitempty"`
}

type GitHubIssues struct {
	Labels []string `json:"labels,omitempty"`
}

type Cron struct {
	Schedule string `json:"schedule"`
}

type TaskTemplate struct {
	Type         string              `json:"type"`
	Credentials  Credentials         `json:"credentials"`
	Model        string              `json:"model,omitempty"`
	WorkspaceRef *WorkspaceReference `json:"workspaceRef,omitempty"`
}

type TaskSpawnerStatus struct {
	Phase          TaskSpawnerPhase `json:"phase,omitempty"`
	DeploymentName string           `json:"deploymentName,omitempty"`
	CronJobName    string           `json:"cronJobName,omitempty"`
	Message        string           `json:"message,omitempty"`
}

func (t *TaskSpawner) DeepCopyObject() runtime.Object {
	c := *t
	return &c
}

type TaskSpawnerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TaskSpawner `json:"items"`
}

func (l *TaskSpawnerList) DeepCopyObject() runtime.Object {
	c := *l
	return &c
}

// Workspace (data-only, no status subresource)

type Workspace struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              WorkspaceSpec `json:"spec,omitempty"`
}

type WorkspaceSpec struct {
	Repo      string          `json:"repo"`
	Ref       string          `json:"ref,omitempty"`
	SecretRef *SecretReference `json:"secretRef,omitempty"`
}

func (w *Workspace) DeepCopyObject() runtime.Object {
	c := *w
	return &c
}

type WorkspaceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Workspace `json:"items"`
}

func (l *WorkspaceList) DeepCopyObject() runtime.Object {
	c := *l
	return &c
}

// AgentConfig (data-only, no status subresource)

type AgentConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              AgentConfigSpec `json:"spec,omitempty"`
}

type AgentConfigSpec struct {
	AgentsMD string `json:"agentsMD,omitempty"`
}

func (a *AgentConfig) DeepCopyObject() runtime.Object {
	c := *a
	return &c
}

type AgentConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentConfig `json:"items"`
}

func (l *AgentConfigList) DeepCopyObject() runtime.Object {
	c := *l
	return &c
}

func AddToScheme(s *runtime.Scheme) {
	s.AddKnownTypes(KelosGV,
		&Task{}, &TaskList{},
		&TaskSpawner{}, &TaskSpawnerList{},
		&Workspace{}, &WorkspaceList{},
		&AgentConfig{}, &AgentConfigList{},
	)
	metav1.AddToGroupVersion(s, KelosGV)
}
