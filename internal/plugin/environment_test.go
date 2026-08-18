package plugin

import (
	"strings"
	"testing"
)

func TestADR0042_PluginsReceiveKubernetesServiceDiscoveryButNotDeploymentSecrets(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.43.0.1")
	t.Setenv("KUBERNETES_SERVICE_PORT", "443")
	t.Setenv("DUSK_MCP_TOKEN", "must-not-leak")
	t.Setenv("DUSK_ENCRYPTION_KEY", "must-not-leak-either")

	got := map[string]string{}
	for _, pair := range pluginEnvironment("/tmp/plugin.sock", "private-token") {
		name, value, _ := strings.Cut(pair, "=")
		got[name] = value
	}

	if got["KUBERNETES_SERVICE_HOST"] != "10.43.0.1" || got["KUBERNETES_SERVICE_PORT"] != "443" {
		t.Errorf("Kubernetes discovery = %q:%q", got["KUBERNETES_SERVICE_HOST"], got["KUBERNETES_SERVICE_PORT"])
	}
	for _, secret := range []string{"DUSK_MCP_TOKEN", "DUSK_ENCRYPTION_KEY"} {
		if _, leaked := got[secret]; leaked {
			t.Errorf("plugin inherited %s", secret)
		}
	}
}
