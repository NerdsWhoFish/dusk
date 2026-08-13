package plugin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"
	sdk "github.com/NerdsWhoFish/dusk-plugin-sdk/plugin"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/NerdsWhoFish/dusk/internal/ingest"
	"github.com/NerdsWhoFish/dusk/internal/plugin"
	"github.com/NerdsWhoFish/dusk/pkg/vault"
)

// standInEnv makes this test binary serve as a plugin instead of running tests.
const standInEnv = "DUSK_TEST_PLUGIN"

// TestMain lets the test binary stand in for a plugin process. Dusk execs a
// binary and speaks gRPC to it, so a fake that is not a real process tests a
// different thing; re-execing this one costs no build step and no testdata.
func TestMain(m *testing.M) {
	spec, standIn := os.LookupEnv(standInEnv)
	if !standIn {
		os.Exit(m.Run())
	}

	if err := standInPlugin(spec); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// standIn is how a test says what the plugin it installs should be.
type standIn struct {
	ID        string   `json:"id"`
	Version   string   `json:"version"`
	Kinds     []string `json:"kinds"`
	Fields    []string `json:"fields"`
	Sensitive []string `json:"sensitive"`

	Actions []standInAction `json:"actions"`

	// Fail makes every Ingest error, for exercising the never-delete rule and
	// the failure reporting around it.
	Fail string `json:"fail"`
}

type standInAction struct {
	Name     string   `json:"name"`
	Class    int32    `json:"class"`
	Kinds    []string `json:"kinds"`
	Async    bool     `json:"async"`
	Then     []string `json:"then"`
	Preview  bool     `json:"preview"`
	Refuse   string   `json:"refuse"`
	Produces []string `json:"produces"`
}

func standInPlugin(encoded string) error {
	var spec standIn
	if err := json.Unmarshal([]byte(encoded), &spec); err != nil {
		return fmt.Errorf("decode the stand-in spec: %w", err)
	}
	return sdk.Run(&standInServer{spec: spec}, sdk.Options{Version: spec.Version})
}

type standInServer struct {
	duskv1alpha1.UnimplementedPluginServiceServer
	spec standIn
}

func (s *standInServer) Describe(context.Context, *duskv1alpha1.DescribeRequest) (*duskv1alpha1.DescribeResponse, error) {
	sensitive := map[string]bool{}
	for _, name := range s.spec.Sensitive {
		sensitive[name] = true
	}

	fields := make([]*duskv1alpha1.ConfigField, 0, len(s.spec.Fields))
	for _, name := range s.spec.Fields {
		fields = append(fields, &duskv1alpha1.ConfigField{
			Name:      name,
			Label:     name,
			Type:      duskv1alpha1.ConfigFieldType_CONFIG_FIELD_TYPE_STRING,
			Sensitive: sensitive[name],
		})
	}

	actions := make([]*duskv1alpha1.ActionDescriptor, 0, len(s.spec.Actions))
	for _, action := range s.spec.Actions {
		then := make([]*duskv1alpha1.Next, 0, len(action.Then))
		for _, name := range action.Then {
			then = append(then, &duskv1alpha1.Next{Action: name})
		}
		actions = append(actions, &duskv1alpha1.ActionDescriptor{
			Name:           action.Name,
			Description:    action.Name + " the thing",
			Class:          duskv1alpha1.ActionClass(action.Class),
			AppliesToKinds: action.Kinds,
			Async:          action.Async,
			ProofFrom:      "get",
			Then:           then,
		})
	}

	return &duskv1alpha1.DescribeResponse{
		PluginId:      s.spec.ID,
		Version:       s.spec.Version,
		SchemaVersion: "v1alpha1",
		EmitsKinds:    s.spec.Kinds,
		ConfigFields:  fields,
		Actions:       actions,
	}, nil
}

func (s *standInServer) ValidateConfig(context.Context, *duskv1alpha1.ValidateConfigRequest) (*duskv1alpha1.ValidateConfigResponse, error) {
	return &duskv1alpha1.ValidateConfigResponse{Ok: true}, nil
}

// Ingest emits one entity whose attributes are the configuration it was handed,
// which is how a test sees what actually crossed the socket.
func (s *standInServer) Ingest(request *duskv1alpha1.IngestRequest, stream duskv1alpha1.PluginService_IngestServer) error {
	if s.spec.Fail != "" {
		return fmt.Errorf("%s", s.spec.Fail)
	}

	kind := "thing"
	if len(s.spec.Kinds) > 0 {
		kind = s.spec.Kinds[0]
	}

	return stream.Send(&duskv1alpha1.IngestResponse{Batch: &duskv1alpha1.IngestBatch{
		SchemaVersion: "v1alpha1",
		Entities: []*duskv1alpha1.Entity{{
			Ref:        kind + ":" + s.spec.ID + "/one",
			Kind:       kind,
			Namespace:  s.spec.ID,
			Name:       "one",
			Title:      "One",
			Attributes: request.GetConfig(),
			Provenance: &duskv1alpha1.Provenance{Source: s.spec.ID},
		}},
	}})
}

func (s *standInServer) find(name string) (standInAction, bool) {
	for _, action := range s.spec.Actions {
		if action.Name == name {
			return action, true
		}
	}
	return standInAction{}, false
}

func (s *standInServer) DryRun(_ context.Context, request *duskv1alpha1.DryRunRequest) (*duskv1alpha1.DryRunResponse, error) {
	action, ok := s.find(request.GetAction())
	if !ok || !action.Preview {
		return &duskv1alpha1.DryRunResponse{Supported: false}, nil
	}
	return &duskv1alpha1.DryRunResponse{
		Supported: true,
		Summary:   "would " + request.GetAction() + " " + request.GetRef(),
	}, nil
}

func (s *standInServer) Invoke(_ context.Context, request *duskv1alpha1.InvokeRequest) (*duskv1alpha1.InvokeResponse, error) {
	action, ok := s.find(request.GetAction())
	if !ok {
		return &duskv1alpha1.InvokeResponse{Done: true, Ok: false, Message: "no such action"}, nil
	}
	if action.Refuse != "" {
		return &duskv1alpha1.InvokeResponse{Done: true, Ok: false, Message: action.Refuse}, nil
	}

	response := &duskv1alpha1.InvokeResponse{
		Done:    !action.Async,
		Ok:      true,
		Message: "did " + action.Name,
		Detail:  detailOf(request),
	}
	if action.Async {
		response.Handle = "handle-" + action.Name
	}
	for _, next := range action.Produces {
		response.Then = append(response.Then, &duskv1alpha1.Next{Action: next})
	}
	return response, nil
}

// detailOf echoes what the plugin was given, so a test can prove the instance's
// own configuration reached it rather than the plugin's first one.
func detailOf(request *duskv1alpha1.InvokeRequest) *structpb.Struct {
	detail, err := structpb.NewStruct(map[string]any{
		"ref":    request.GetRef(),
		"proof":  request.GetProofToken(),
		"config": request.GetConfig().AsMap(),
		"params": request.GetParams().AsMap(),
	})
	if err != nil {
		return nil
	}
	return detail
}

func (s *standInServer) Status(_ context.Context, request *duskv1alpha1.StatusRequest) (*duskv1alpha1.StatusResponse, error) {
	return &duskv1alpha1.StatusResponse{Done: true, Ok: true, Message: "finished " + request.GetHandle()}, nil
}

// install writes a stand-in plugin where the manager expects a binary and
// records it as installed, which is everything Restore needs to start it.
func install(t *testing.T, store *plugin.Store, spec standIn) {
	t.Helper()

	if spec.Version == "" {
		spec.Version = "v0.0.1"
	}

	encoded, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("encode the stand-in spec: %v", err)
	}
	t.Setenv(standInEnv, string(encoded))

	if err := os.MkdirAll(filepath.Join(store.Dir, spec.ID), 0o700); err != nil {
		t.Fatalf("make the plugin directory: %v", err)
	}
	// A symlink rather than a copy: exec follows it to this test binary, which
	// TestMain then runs as the plugin.
	if err := os.Symlink(os.Args[0], store.Binary(spec.ID)); err != nil {
		t.Fatalf("link the stand-in binary: %v", err)
	}
	if err := store.Write(plugin.Installed{ID: spec.ID, Version: spec.Version}); err != nil {
		t.Fatalf("record the install: %v", err)
	}
}

// rota records what joined the rotation, standing in for the ingest scheduler
// so the manager can be exercised without one.
type rota struct {
	mu      sync.Mutex
	joined  map[string]ingest.Ingester
	removed []string
	results []ingest.Result
}

func newRota() *rota { return &rota{joined: map[string]ingest.Ingester{}} }

func (r *rota) Add(ingester ingest.Ingester) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.joined[ingester.Name()] = ingester
}

func (r *rota) Remove(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.joined, name)
	r.removed = append(r.removed, name)
}

func (r *rota) Status() []ingest.Result {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.results
}

// observed runs one joined ingester and returns what it emitted, which is how
// a test sees the configuration that actually crossed the socket.
func (r *rota) observed(ctx context.Context, t *testing.T, name string) *ingest.Observation {
	t.Helper()

	r.mu.Lock()
	ingester, ok := r.joined[name]
	r.mu.Unlock()

	if !ok {
		t.Fatalf("nothing named %q joined the rotation, only %v", name, r.names())
	}

	observation, err := ingester.Observe(ctx)
	if err != nil {
		t.Fatalf("observe %s: %v", name, err)
	}
	return observation
}

func (r *rota) names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Sorted(maps.Keys(r.joined))
}

// manager builds a Manager over a temporary data directory, with a marketplace
// that offers nothing: these tests install by hand rather than over the wire.
func manager(t *testing.T) (*plugin.Manager, *rota) {
	t.Helper()

	master := make([]byte, vault.KeySize)
	for i := range master {
		master[i] = byte(i)
	}

	// An empty Market falls back to the real org on the real GitHub, which
	// would make these tests reach the network to list what they never install.
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(empty.Close)

	rotation := newRota()
	return &plugin.Manager{
		Store:  &plugin.Store{Dir: t.TempDir(), Master: master},
		Market: &plugin.Market{BaseURL: empty.URL, Orgs: []string{"nobody"}},
		Rota:   rotation,
		Log:    slog.New(slog.DiscardHandler),
	}, rotation
}
