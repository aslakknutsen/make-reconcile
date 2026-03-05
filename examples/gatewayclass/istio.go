package main

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mr "github.com/aslakknutsen/make-reconcile"
)

const (
	OpenShiftGatewayClassControllerName = "openshift.io/gateway-controller"
	OpenShiftDefaultGatewayClassName    = "openshift-default"

	istioVersionOverrideAnnotation = "unsupported.do-not-use.openshift.io/istio-version"

	defaultIstioVersion    = "v1.27.7"
	defaultIstioName       = "openshift-gateway"
	defaultIstioNamespace  = "openshift-ingress"
	operandNamespace       = "openshift-ingress"
)

// IstioReconciler produces the Istio CR that tells the Sail operator to install istiod.
//
// Corresponds to ensureIstioOLM + desiredIstio in the original controller.
// In the original, this is ~80 lines of ensure/create/update/diff logic.
// Here it's a pure function: return desired state, framework handles apply/delete.
func IstioReconciler(hc *mr.HandlerContext, gc *GatewayClass) *Istio {
	istioVersion := defaultIstioVersion
	if v, ok := gc.Annotations[istioVersionOverrideAnnotation]; ok {
		istioVersion = v
	}

	return &Istio{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "sailoperator.io/v1",
			Kind:       "Istio",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      defaultIstioName,
			Namespace: defaultIstioNamespace,
		},
		Spec: IstioSpec{
			Version:   istioVersion,
			Namespace: operandNamespace,
			PilotEnv:  pilotEnv(),
		},
	}
}

func pilotEnv() map[string]string {
	return map[string]string{
		"PILOT_ENABLE_GATEWAY_API":                         "true",
		"PILOT_ENABLE_ALPHA_GATEWAY_API":                   "false",
		"PILOT_ENABLE_GATEWAY_API_STATUS":                  "true",
		"PILOT_ENABLE_GATEWAY_API_DEPLOYMENT_CONTROLLER":   "true",
		"PILOT_ENABLE_GATEWAY_API_GATEWAYCLASS_CONTROLLER": "false",
		"PILOT_GATEWAY_API_DEFAULT_GATEWAYCLASS_NAME":      OpenShiftDefaultGatewayClassName,
		"PILOT_GATEWAY_API_CONTROLLER_NAME":                OpenShiftGatewayClassControllerName,
		"PILOT_MULTI_NETWORK_DISCOVER_GATEWAY_API":         "false",
		"ENABLE_GATEWAY_API_MANUAL_DEPLOYMENT":             "false",
		"PILOT_ENABLE_GATEWAY_API_CA_CERT_ONLY":            "true",
		"PILOT_ENABLE_GATEWAY_API_COPY_LABELS_ANNOTATIONS": "false",
	}
}
