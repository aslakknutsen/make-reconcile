package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	mr "github.com/aslakknutsen/make-reconcile"
)

// DatabaseSecretReconciler returns a handler that produces the database
// credentials Secret. It Fetches the existing Secret first — if it already
// exists, the reconciler re-applies the same data so SSA is a no-op.
// Without this, generatePassword() would produce a new password every
// reconcile, causing perpetual Secret→ConfigMap→Deployment cascades.
func DatabaseSecretReconciler(secrets *mr.Collection[*corev1.Secret]) func(*mr.HandlerContext, *Platform) *corev1.Secret {
	return func(hc *mr.HandlerContext, p *Platform) *corev1.Secret {
		secretName := p.Name + "-db"
		existing := mr.Fetch(hc, secrets, mr.FilterName(secretName, p.Namespace))

		password := generatePassword()
		if existing != nil {
			password = string(existing.Data["password"])
		}

		return &corev1.Secret{
			TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
			ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: p.Namespace, Labels: componentLabels(p.Name, "database")},
			StringData: map[string]string{
				"username": "app",
				"password": password,
			},
		}
	}
}

func DatabaseStatefulSetReconciler(hc *mr.HandlerContext, p *Platform) *appsv1.StatefulSet {
	labels := componentLabels(p.Name, "database")
	sel := selectorLabels(p.Name, "database")

	var one int32 = 1
	return &appsv1.StatefulSet{
		TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "StatefulSet"},
		ObjectMeta: metav1.ObjectMeta{Name: p.Name + "-db", Namespace: p.Namespace, Labels: labels},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &one,
			ServiceName: p.Name + "-db",
			Selector:    &metav1.LabelSelector{MatchLabels: sel},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: sel},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "postgres",
						Image: p.Spec.Database.Image,
						Ports: []corev1.ContainerPort{{Name: "pg", ContainerPort: p.Spec.Database.Port}},
						EnvFrom: []corev1.EnvFromSource{{
							SecretRef: &corev1.SecretEnvSource{
								LocalObjectReference: corev1.LocalObjectReference{Name: p.Name + "-db"},
							},
						}},
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "data",
							MountPath: "/var/lib/postgresql/data",
						}},
					}},
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{{
				ObjectMeta: metav1.ObjectMeta{Name: "data"},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse(p.Spec.Database.StorageSize),
						},
					},
				},
			}},
		},
	}
}

func DatabaseServiceReconciler(hc *mr.HandlerContext, p *Platform) *corev1.Service {
	labels := componentLabels(p.Name, "database")
	sel := selectorLabels(p.Name, "database")

	return &corev1.Service{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
		ObjectMeta: metav1.ObjectMeta{Name: p.Name + "-db", Namespace: p.Namespace, Labels: labels},
		Spec: corev1.ServiceSpec{
			ClusterIP: "None",
			Selector:  sel,
			Ports: []corev1.ServicePort{{
				Name:       "pg",
				Port:       p.Spec.Database.Port,
				TargetPort: intstr.FromString("pg"),
			}},
		},
	}
}

func generatePassword() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("fallback-%d", 0)
	}
	return hex.EncodeToString(b)
}
