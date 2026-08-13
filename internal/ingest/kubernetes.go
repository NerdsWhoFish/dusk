package ingest

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"
)

// Kubernetes observes one cluster: what is running, and what it runs on.
type Kubernetes struct {
	// Cluster names the cluster in refs and in the ingester's own name, so
	// two clusters do not collide in one catalog.
	Cluster string

	// Namespace is what an entity's ref is namespaced by. Defaults to the
	// cluster name, which keeps a multi-cluster catalog unambiguous.
	Namespace string

	// Skip are Kubernetes namespaces not worth cataloguing. A cluster's own
	// plumbing is not somebody's service.
	Skip []string

	Every  time.Duration
	client kubernetes.Interface
}

// DefaultSkip leaves out the cluster's own machinery, which nobody writes a
// catalog entry for and which buries the services somebody does want to
// describe. Override Skip to catalogue them anyway.
var DefaultSkip = []string{
	"kube-system", "kube-public", "kube-node-lease",
	"flux-system", "cnpg-system", "cert-manager", "local-path-storage",
}

// NewKubernetes connects to a cluster. An empty kubeconfig means in-cluster
// credentials, which is how Dusk observes the cluster it is deployed to.
func NewKubernetes(cluster, kubeconfig string) (*Kubernetes, error) {
	config, err := restConfig(cluster, kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("ingest: reach cluster %q: %w", cluster, err)
	}

	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("ingest: build a client for cluster %q: %w", cluster, err)
	}

	return &Kubernetes{
		Cluster: cluster, Namespace: cluster, Skip: DefaultSkip,
		Every: time.Hour, client: client,
	}, nil
}

// restConfig resolves credentials for one cluster, from the context named
// after it rather than the kubeconfig's current-context. A missing context is
// an error: falling back would observe one cluster and label it as another.
func restConfig(cluster, kubeconfig string) (*rest.Config, error) {
	if kubeconfig == "" {
		return rest.InClusterConfig()
	}

	loader := &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig}
	raw, err := loader.Load()
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", kubeconfig, err)
	}
	if _, ok := raw.Contexts[cluster]; !ok {
		return nil, fmt.Errorf("%q has no context named %q, only %s. Name the cluster after its context, or Dusk would observe a different one and label it %q",
			kubeconfig, cluster, strings.Join(contextNames(raw.Contexts), ", "), cluster)
	}

	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loader, &clientcmd.ConfigOverrides{CurrentContext: cluster},
	).ClientConfig()
}

func contextNames[T any](contexts map[string]T) []string {
	names := make([]string, 0, len(contexts))
	for name := range contexts {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// Name scopes this cluster's observations, so two clusters do not overwrite
// each other.
func (k *Kubernetes) Name() string { return "kubernetes:" + k.Cluster }

// Interval defaults to hourly: a cluster changes often, but not so often that
// a stale hour misleads anybody, and every run is a full list.
func (k *Kubernetes) Interval() time.Duration {
	if k.Every <= 0 {
		return time.Hour
	}
	return k.Every
}

// Observe turns the cluster's Nodes and Services into entities. Either listing
// failing fails the whole run, because a partial view would read as things
// having been deleted (ADR-0011).
func (k *Kubernetes) Observe(ctx context.Context) (*Observation, error) {
	nodes, err := k.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	services, err := k.client.CoreV1().Services(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list services: %w", err)
	}

	observation := &Observation{}
	clusterRef := k.ref("cluster", k.Cluster)

	observation.Entities = append(observation.Entities, &duskv1alpha1.Entity{
		Ref: clusterRef, Kind: "cluster", Namespace: k.namespace(), Name: k.Cluster,
		Title:       k.Cluster,
		Description: fmt.Sprintf("Kubernetes cluster with %d node(s), observed by Dusk.", len(nodes.Items)),
	})

	for _, node := range nodes.Items {
		hostRef := k.ref("host", node.Name)
		observation.Entities = append(observation.Entities, &duskv1alpha1.Entity{
			Ref: hostRef, Kind: "host", Namespace: k.namespace(), Name: node.Name,
			Title:       node.Name,
			Description: describeNode(node),
		})
		observation.Relations = append(observation.Relations, &duskv1alpha1.Relation{
			From: hostRef, To: clusterRef, Type: "part_of",
		})
	}

	for _, service := range services.Items {
		if k.skipped(service.Namespace) || k.plumbing(service) {
			continue
		}

		serviceRef := k.ref("service", service.Namespace+"-"+service.Name)
		observation.Entities = append(observation.Entities, &duskv1alpha1.Entity{
			Ref: serviceRef, Kind: "service", Namespace: k.namespace(),
			Name:        service.Namespace + "-" + service.Name,
			Title:       service.Name,
			Description: describeService(service),
		})
		observation.Relations = append(observation.Relations, &duskv1alpha1.Relation{
			From: serviceRef, To: clusterRef, Type: "runs_on",
		})
	}

	return observation, nil
}

// ref builds a catalog ref. ADR-0018 puts normalization at the edge, so the
// shape a reader sees is decided here rather than anywhere downstream.
func (k *Kubernetes) ref(kind, name string) string {
	return kind + ":" + k.namespace() + "/" + slug(name)
}

func (k *Kubernetes) namespace() string {
	if k.Namespace != "" {
		return slug(k.Namespace)
	}
	return slug(k.Cluster)
}

// plumbing reports a Service that exists to make another Service work: a
// headless one backing a StatefulSet, an ExternalName aliasing something
// already catalogued, or the API itself. None is worth writing down.
func (k *Kubernetes) plumbing(service corev1.Service) bool {
	switch {
	case service.Spec.ClusterIP == corev1.ClusterIPNone:
		return true
	case service.Spec.Type == corev1.ServiceTypeExternalName:
		return true
	case service.Namespace == "default" && service.Name == "kubernetes":
		return true
	}
	return false
}

func (k *Kubernetes) skipped(namespace string) bool {
	for _, candidate := range k.Skip {
		if candidate == namespace {
			return true
		}
	}
	return false
}

func describeNode(node corev1.Node) string {
	info := node.Status.NodeInfo
	return fmt.Sprintf("Kubernetes node running %s on %s/%s, kubelet %s. Observed by Dusk.",
		info.OSImage, info.OperatingSystem, info.Architecture, info.KubeletVersion)
}

func describeService(service corev1.Service) string {
	var ports []string
	for _, port := range service.Spec.Ports {
		ports = append(ports, fmt.Sprintf("%d/%s", port.Port, port.Protocol))
	}

	description := fmt.Sprintf("Kubernetes %s service in namespace `%s`",
		strings.ToLower(string(service.Spec.Type)), service.Namespace)
	if len(ports) > 0 {
		description += ", serving " + strings.Join(ports, ", ")
	}
	return description + ". Observed by Dusk."
}

// slug makes a Kubernetes name safe to put in a ref. Kubernetes names are
// already lowercase alphanumeric with dashes, so this is a guard rather than
// a transformation.
func slug(name string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		default:
			return '-'
		}
	}, name)
	return strings.Trim(cleaned, "-")
}
