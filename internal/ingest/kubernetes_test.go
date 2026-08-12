package ingest

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

func service(namespace, name string, mutate func(*corev1.Service)) corev1.Service {
	svc := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, ClusterIP: "10.0.0.1"},
	}
	if mutate != nil {
		mutate(&svc)
	}
	return svc
}

func observe(t *testing.T, services ...corev1.Service) *Observation {
	t.Helper()

	objects := make([]runtime.Object, 0, len(services)+1)
	for i := range services {
		objects = append(objects, &services[i])
	}
	objects = append(objects, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}})

	k := &Kubernetes{
		Cluster: "mini-2", Namespace: "mini-2", Skip: DefaultSkip,
		client: fake.NewSimpleClientset(objects...),
	}
	observation, err := k.Observe(t.Context())
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	return observation
}

// The catalog is for things somebody would describe. A cluster's own machinery
// is not, and every row of it stands between the reader and something real.
func TestObserveLeavesOutClusterMachinery(t *testing.T) {
	observation := observe(t,
		service("karakeep", "karakeep", nil),
		service("flux-system", "source-controller", nil),
		service("cnpg-system", "cnpg-webhook-service", nil),
		service("kube-system", "kube-dns", nil),
		service("default", "kubernetes", nil),
		service("spacelift", "postgres-r", func(s *corev1.Service) {
			s.Spec.ClusterIP = corev1.ClusterIPNone
		}),
		service("glance", "glance-external", func(s *corev1.Service) {
			s.Spec.Type = corev1.ServiceTypeExternalName
			s.Spec.ExternalName = "glance.example.com"
		}),
	)

	var names []string
	for _, entity := range observation.Entities {
		if entity.GetKind() == "service" {
			names = append(names, entity.GetName())
		}
	}

	if len(names) != 1 || names[0] != "karakeep-karakeep" {
		t.Errorf("services = %v, want only the one worth cataloguing", names)
	}
}

// The cluster and its nodes are always worth having: they are what everything
// else hangs off.
func TestObserveKeepsTheClusterAndItsNodes(t *testing.T) {
	observation := observe(t, service("karakeep", "karakeep", nil))

	kinds := map[string]int{}
	for _, entity := range observation.Entities {
		kinds[entity.GetKind()]++
	}

	if kinds["cluster"] != 1 || kinds["host"] != 1 {
		t.Errorf("kinds = %v, want one cluster and one host", kinds)
	}
	if len(observation.Relations) != 2 {
		t.Errorf("relations = %d, want the node in the cluster and the service on it", len(observation.Relations))
	}
}
