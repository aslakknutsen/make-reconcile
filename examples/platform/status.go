package main

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"

	mr "github.com/aslakknutsen/make-reconcile"
)

// StatusReconciler returns a handler that aggregates the health of all
// sub-resources into the Platform's .status by Fetching key Deployments.
func StatusReconciler(deployments *mr.Collection[*appsv1.Deployment]) func(*mr.HandlerContext, *Platform) *PlatformStatus {
	return func(hc *mr.HandlerContext, p *Platform) *PlatformStatus {
		var ready, total int

		appDeploy := mr.Fetch(hc, deployments, mr.FilterName(p.Name+"-app", p.Namespace))
		total++
		if appDeploy != nil && appDeploy.Status.ReadyReplicas > 0 {
			ready++
		}

		dbDeploy := mr.Fetch(hc, deployments, mr.FilterName(p.Name+"-db", p.Namespace))
		total++
		if dbDeploy != nil && dbDeploy.Status.ReadyReplicas > 0 {
			ready++
		}

		if p.Spec.Monitoring.Enabled {
			monDeploy := mr.Fetch(hc, deployments, mr.FilterName(p.Name+"-monitoring", p.Namespace))
			total++
			if monDeploy != nil && monDeploy.Status.ReadyReplicas > 0 {
				ready++
			}
		}

		for _, w := range p.Spec.Workers {
			workerDeploy := mr.Fetch(hc, deployments, mr.FilterName(p.Name+"-worker-"+w.Name, p.Namespace))
			total++
			if workerDeploy != nil && workerDeploy.Status.ReadyReplicas > 0 {
				ready++
			}
		}

		return &PlatformStatus{
			Ready:      ready == total,
			Components: total,
			Message:    fmt.Sprintf("%d/%d components ready", ready, total),
		}
	}
}
