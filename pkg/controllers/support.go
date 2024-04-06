package controllers

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	rayv1 "github.com/ray-project/kuberay/ray-operator/apis/ray/v1"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	v1 "k8s.io/client-go/applyconfigurations/meta/v1"
	networkingv1ac "k8s.io/client-go/applyconfigurations/networking/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"

	routeapply "github.com/openshift/client-go/route/applyconfigurations/route/v1"
)

func serviceNameFromCluster(cluster *rayv1.RayCluster) string {
	return cluster.Name + "-head-svc"
}

func desiredRayClientRoute(cluster *rayv1.RayCluster) *routeapply.RouteApplyConfiguration {
	return routeapply.Route(rayClientNameFromCluster(cluster), cluster.Namespace).
		WithLabels(map[string]string{"ray.io/cluster-name": cluster.Name}).
		WithSpec(routeapply.RouteSpec().
			WithHost(rayClientNameFromCluster(cluster) + "-" + cluster.Namespace).
			WithTo(routeapply.RouteTargetReference().WithKind("Service").WithName(serviceNameFromCluster(cluster)).WithWeight(100)).
			WithPort(routeapply.RoutePort().WithTargetPort(intstr.FromString("client"))).
			WithTLS(routeapply.TLSConfig().WithTermination("passthrough")),
		).
		WithOwnerReferences(
			v1.OwnerReference().WithUID(cluster.UID).WithName(cluster.Name).WithKind(cluster.Kind).WithAPIVersion(cluster.APIVersion),
		)
}

// Create an Ingress object for the RayCluster
func desiredRayClientIngress(cluster *rayv1.RayCluster, ingressDomain string) *networkingv1ac.IngressApplyConfiguration {
	return networkingv1ac.Ingress(rayClientNameFromCluster(cluster), cluster.Namespace).
		WithLabels(map[string]string{"ray.io/cluster-name": cluster.Name}).
		WithAnnotations(map[string]string{
			"nginx.ingress.kubernetes.io/rewrite-target":  "/",
			"nginx.ingress.kubernetes.io/ssl-redirect":    "true",
			"nginx.ingress.kubernetes.io/ssl-passthrough": "true",
		}).
		WithOwnerReferences(v1.OwnerReference().
			WithAPIVersion(cluster.APIVersion).
			WithKind(cluster.Kind).
			WithName(cluster.Name).
			WithUID(types.UID(cluster.UID))).
		WithSpec(networkingv1ac.IngressSpec().
			WithIngressClassName("nginx").
			WithRules(networkingv1ac.IngressRule().
				WithHost(rayClientNameFromCluster(cluster) + "-" + cluster.Namespace + "." + ingressDomain).
				WithHTTP(networkingv1ac.HTTPIngressRuleValue().
					WithPaths(networkingv1ac.HTTPIngressPath().
						WithPath("/").
						WithPathType(networkingv1.PathTypeImplementationSpecific).
						WithBackend(networkingv1ac.IngressBackend().
							WithService(networkingv1ac.IngressServiceBackend().
								WithName(serviceNameFromCluster(cluster)).
								WithPort(networkingv1ac.ServiceBackendPort().
									WithNumber(10001),
								),
							),
						),
					),
				),
			),
		)
}

// Create an Ingress object for the RayCluster
func desiredClusterIngress(cluster *rayv1.RayCluster, ingressHost string) *networkingv1ac.IngressApplyConfiguration {
	return networkingv1ac.Ingress(dashboardNameFromCluster(cluster), cluster.Namespace).
		WithLabels(map[string]string{"ray.io/cluster-name": cluster.Name}).
		WithOwnerReferences(v1.OwnerReference().
			WithAPIVersion(cluster.APIVersion).
			WithKind(cluster.Kind).
			WithName(cluster.Name).
			WithUID(types.UID(cluster.UID))).
		WithSpec(networkingv1ac.IngressSpec().
			WithRules(networkingv1ac.IngressRule().
				WithHost(ingressHost). // KinD hostname or ingressDomain
				WithHTTP(networkingv1ac.HTTPIngressRuleValue().
					WithPaths(networkingv1ac.HTTPIngressPath().
						WithPath("/").
						WithPathType(networkingv1.PathTypePrefix).
						WithBackend(networkingv1ac.IngressBackend().
							WithService(networkingv1ac.IngressServiceBackend().
								WithName(serviceNameFromCluster(cluster)).
								WithPort(networkingv1ac.ServiceBackendPort().
									WithName(ingressServicePortName),
								),
							),
						),
					),
				),
			),
		)
}

// isOnKindCluster checks if the current cluster is a KinD cluster.
// It searches for a node with a label commonly used by KinD clusters.
func isOnKindCluster(clientset *kubernetes.Clientset) (bool, error) {
	nodes, err := clientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{
		LabelSelector: "kubernetes.io/hostname=kind-control-plane",
	})
	if err != nil {
		return false, err
	}
	// If we find one or more nodes with the label, assume it's a KinD cluster.
	return len(nodes.Items) > 0, nil
}

// getDiscoveryClient returns a discovery client for the current reconciler
func getDiscoveryClient(config *rest.Config) (*discovery.DiscoveryClient, error) {
	return discovery.NewDiscoveryClientForConfig(config)
}

// Check where we are running. We are trying to distinguish here whether
// this is vanilla kubernetes cluster or Openshift
func getClusterType(ctx context.Context, clientset *kubernetes.Clientset, cluster *rayv1.RayCluster, ingressDomain string) (bool, string) {
	// The discovery package is used to discover APIs supported by a Kubernetes API server.
	logger := ctrl.LoggerFrom(ctx)
	config, err := ctrl.GetConfig()
	if err != nil && config == nil {
		logger.Info("Cannot retrieve config, assuming we're on Vanilla Kubernetes")
		return false, fmt.Sprintf("ray-dashboard-%s-%s.%s", cluster.Name, cluster.Namespace, ingressDomain)
	}
	dclient, err := getDiscoveryClient(config)
	if err != nil && dclient == nil {
		logger.Info("Cannot retrieve a DiscoveryClient, assuming we're on Vanilla Kubernetes")
		return false, fmt.Sprintf("ray-dashboard-%s-%s.%s", cluster.Name, cluster.Namespace, ingressDomain)
	}
	apiGroupList, err := dclient.ServerGroups()
	if err != nil {
		logger.Info("Error while querying ServerGroups, assuming we're on Vanilla Kubernetes")
		return false, ""
	}
	for i := 0; i < len(apiGroupList.Groups); i++ {
		if strings.HasSuffix(apiGroupList.Groups[i].Name, ".openshift.io") {
			logger.Info("We detected being on OpenShift!")
			return true, ""
		}
	}
	onKind, _ := isOnKindCluster(clientset)
	if onKind && ingressDomain == "" {
		logger.Info("We detected being on a KinD cluster!")
		return false, "kind"
	}
	logger.Info("We detected being on Vanilla Kubernetes!")
	return false, fmt.Sprintf("ray-dashboard-%s-%s.%s", cluster.Name, cluster.Namespace, ingressDomain)
}

func (r *RayClusterReconciler) isRayDashboardOAuthEnabled() bool {
	if r.Config != nil && r.Config.KubeRay != nil && r.Config.KubeRay.RayDashboardOAuthEnabled != nil {
		return *r.Config.KubeRay.RayDashboardOAuthEnabled
	}
	return true
}

func annotationBoolVal(ctx context.Context, cluster *rayv1.RayCluster, annotation string, defaultValue bool) bool {
	logger := ctrl.LoggerFrom(ctx)
	val, exists := cluster.ObjectMeta.Annotations[annotation]
	if !exists || val == "" {
		return defaultValue
	}
	boolVal, err := strconv.ParseBool(val)
	if err != nil {
		logger.Error(err, "Could not convert annotation value to bool", "annotation", annotation, "value", val)
		return defaultValue
	}
	return boolVal
}
