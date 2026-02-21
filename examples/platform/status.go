package main

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"

	mr "github.com/aslakknutsen/make-reconcile"
)

// RegisterStatus registers a status reconciler that aggregates the health of
// all sub-resources into the Platform's .status. It Fetches the key Deployments
// and StatefulSets to check their ready replica counts, then reports an overall
// Ready/Components/Message on the Platform status.
func RegisterStatus(
	mgr *mr.Manager,
	platforms *mr.Collection[*Platform],
	deployments *mr.Collection[*appsv1.Deployment],
) {
	mr.ReconcileStatus(mgr, platforms, func(hc *mr.HandlerContext, p *Platform) *PlatformStatus {
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

		if p.Spec.Cache.Enabled {
			cacheDeploy := mr.Fetch(hc, deployments, mr.FilterName(p.Name+"-cache", p.Namespace))
			total++
			if cacheDeploy != nil && cacheDeploy.Status.ReadyReplicas > 0 {
				ready++
			}
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
	})
}
