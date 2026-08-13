// Package plugin runs installed plugins and puts them in reach of the catalog.
//
// A running plugin is an ordinary ingest.Ingester, so scheduling, backoff, the
// never-delete rule and provenance all come from ingest rather than being
// reimplemented for a second kind of source (ADR-0039, ADR-0040).
package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/structpb"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/ingest"
)

// SocketEnv is how a plugin is told where to serve. The plugin contract is
// gRPC on a socket the host provides, so the host names it.
const SocketEnv = "DUSK_PLUGIN_SOCKET"

// TokenEnv carries a secret minted per start. Every socket shares one directory
// and one user, so any plugin can dial another's; this makes doing so useless
// and keeps composition going through Dusk.
const TokenEnv = "DUSK_PLUGIN_TOKEN"

// SocketDir is short on purpose. A unix socket address is capped near 104
// bytes, and putting sockets under the data directory would blow that on any
// deployment whose data path is nested.
const SocketDir = "/tmp/dusk-plugins"

// maxSocketPath is the portable floor for a socket address, below the 108 that
// Linux allows and equal to what macOS and BSD do.
const maxSocketPath = 104

// startTimeout bounds how long a plugin gets to serve its socket before the
// host gives up. A plugin that cannot answer Describe cannot be scheduled.
const startTimeout = 15 * time.Second

// defaultInterval is used when a plugin is installed with no interval of its
// own. Hourly matches what the in-tree Kubernetes ingester chose.
const defaultInterval = time.Hour

// Running is one plugin process and the connection to it.
type Running struct {
	ID       string
	Version  string
	Describe *duskv1alpha1.DescribeResponse

	config   *structpb.Struct
	interval time.Duration

	cmd    *exec.Cmd
	conn   *grpc.ClientConn
	client duskv1alpha1.PluginServiceClient
	socket string
	log    *slog.Logger
	assets map[string]Asset
}

// maxAssetBytes bounds a plugin's JavaScript. A view is tens of kilobytes; a
// plugin sending more than this is not shipping a view.
const maxAssetBytes = 16 << 20

// Start execs a plugin and returns it ready to run. It calls Describe first,
// because a plugin that cannot describe itself cannot be scheduled, and failing
// here beats failing later in a sweep nobody is watching.
func Start(ctx context.Context, id, binary string, config *structpb.Struct, log *slog.Logger) (*Running, error) {
	if log == nil {
		log = slog.Default()
	}

	socket, err := socketFor(id)
	if err != nil {
		return nil, err
	}

	token, err := mintToken()
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(binary)
	cmd.Env = append(os.Environ(), SocketEnv+"="+socket, TokenEnv+"="+token)
	cmd.Stdout = logWriter{log: log, id: id, stream: "stdout"}
	cmd.Stderr = logWriter{log: log, id: id, stream: "stderr"}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("plugin: start %s: %w", id, err)
	}

	running := &Running{
		ID: id, config: config, interval: defaultInterval,
		cmd: cmd, socket: socket, log: log,
	}

	conn, err := grpc.NewClient("unix://"+socket,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(presentToken(token)),
		grpc.WithStreamInterceptor(presentTokenStream(token)))
	if err != nil {
		running.stop()
		return nil, fmt.Errorf("plugin: connect to %s: %w", id, err)
	}
	running.conn = conn
	running.client = duskv1alpha1.NewPluginServiceClient(conn)

	described, err := running.describe(ctx)
	if err != nil {
		running.stop()
		return nil, err
	}
	running.Describe = described
	running.Version = described.GetVersion()

	// Fetched once at start rather than per request: the asset belongs to this
	// build of this plugin, so it cannot change while the process lives.
	if err := running.fetchAssets(ctx); err != nil {
		running.stop()
		return nil, err
	}
	return running, nil
}

// Asset is a plugin's JavaScript, content addressed so it can be served
// immutable without ever pinning a browser to a stale copy.
type Asset struct {
	SHA  string
	Body []byte
}

// View is one place a plugin renders itself, resolved to a URL Dusk serves.
type View struct {
	Plugin  string   `json:"plugin"`
	Element string   `json:"element"`
	Title   string   `json:"title,omitempty"`
	Source  string   `json:"source"`
	Kinds   []string `json:"kinds,omitempty"`
}

// Views is what this plugin contributes to the UI.
func (r *Running) Views() []View {
	views := make([]View, 0, len(r.Describe.GetUi()))
	for _, ui := range r.Describe.GetUi() {
		asset, ok := r.assets[ui.GetAsset()]
		if !ok {
			continue
		}
		views = append(views, View{
			Plugin: r.ID, Element: ui.GetElement(), Title: ui.GetTitle(),
			Source: "/plugin-assets/" + r.ID + "/" + asset.SHA + ".js",
			Kinds:  ui.GetAppliesToKinds(),
		})
	}
	return views
}

// Asset returns a fetched asset by its digest.
func (r *Running) Asset(sha string) (Asset, bool) {
	for _, asset := range r.assets {
		if asset.SHA == sha {
			return asset, true
		}
	}
	return Asset{}, false
}

func (r *Running) fetchAssets(ctx context.Context) error {
	r.assets = map[string]Asset{}

	for _, ui := range r.Describe.GetUi() {
		name := ui.GetAsset()
		if name == "" {
			continue
		}

		body, err := r.asset(ctx, name)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		r.assets[name] = Asset{SHA: hex.EncodeToString(sum[:]), Body: body}
	}
	return nil
}

// asset collects the chunks GetAsset streams, bounded so a plugin cannot fill
// memory with something the browser was never going to run.
func (r *Running) asset(ctx context.Context, name string) ([]byte, error) {
	stream, err := r.client.GetAsset(ctx, &duskv1alpha1.GetAssetRequest{Name: name})
	if err != nil {
		return nil, fmt.Errorf("plugin: %s asset %q: %w", r.ID, name, err)
	}

	var body []byte
	for {
		response, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return body, nil
		}
		if err != nil {
			return nil, fmt.Errorf("plugin: %s asset %q: %w", r.ID, name, err)
		}

		body = append(body, response.GetChunk()...)
		if len(body) > maxAssetBytes {
			return nil, fmt.Errorf("plugin: %s asset %q is larger than %d bytes", r.ID, name, maxAssetBytes)
		}
	}
}

// describe retries, because the process is racing to bind its socket and a
// connection refused during that window means "not yet" rather than "broken".
func (r *Running) describe(ctx context.Context) (*duskv1alpha1.DescribeResponse, error) {
	deadline, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()

	var last error
	for {
		described, err := r.client.Describe(deadline, &duskv1alpha1.DescribeRequest{})
		if err == nil {
			return described, nil
		}
		last = err

		select {
		case <-deadline.Done():
			return nil, fmt.Errorf("plugin: %s did not answer within %s: %w", r.ID, startTimeout, last)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// Name is the ingester name, and therefore the scope its observations live
// under. Prefixed so a plugin cannot collide with an in-tree ingester.
func (r *Running) Name() string { return "plugin:" + r.ID }

// Instance is one configured use of a plugin, with its own scope and its own
// place in the rotation. Instances share a process, and fail apart: an
// unreachable cluster backs its own instance off without staling the others.
type Instance struct {
	*Running

	// instance names this configuration. Empty is the plugin's own name, which
	// is what a single-instance install looks like.
	instance string
	config   *structpb.Struct
}

// Name scopes this instance's observations.
func (i *Instance) Name() string {
	if i.instance == "" {
		return i.Running.Name()
	}
	return i.Running.Name() + ":" + i.instance
}

// Observe uses this instance's own configuration, not the plugin's.
func (i *Instance) Observe(ctx context.Context) (*ingest.Observation, error) {
	return i.observe(ctx, i.config, i.Name())
}

// Interval is how often this plugin should be asked to observe.
func (r *Running) Interval() time.Duration {
	if r.interval <= 0 {
		return defaultInterval
	}
	return r.interval
}

// Observe collects one Ingest stream into a complete view. A partial batch is
// refused rather than stored, because ingest.Run treats what it is given as
// everything the source has and would delete the rest.
func (r *Running) Observe(ctx context.Context) (*ingest.Observation, error) {
	return r.observe(ctx, r.config, r.Name())
}

func (r *Running) observe(ctx context.Context, config *structpb.Struct, name string) (*ingest.Observation, error) {
	stream, err := r.client.Ingest(ctx, &duskv1alpha1.IngestRequest{Config: config})
	if err != nil {
		return nil, fmt.Errorf("plugin: %s ingest: %w", name, err)
	}

	observation := &ingest.Observation{}
	for {
		response, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return observation, nil
		}
		if err != nil {
			return nil, fmt.Errorf("plugin: %s ingest: %w", r.ID, err)
		}

		batch := response.GetBatch()
		if batch.GetPartial() {
			return nil, fmt.Errorf("plugin: %s reported a partial view, which cannot be told apart from things having been deleted", r.ID)
		}
		observation.Entities = append(observation.Entities, batch.GetEntities()...)
		observation.Relations = append(observation.Relations, batch.GetRelations()...)
	}
}

// Stop shuts the plugin down, politely first.
func (r *Running) Stop() { r.stop() }

func (r *Running) stop() {
	if r.conn != nil {
		_ = r.conn.Close()
	}
	if r.cmd == nil || r.cmd.Process == nil {
		return
	}

	_ = r.cmd.Process.Signal(os.Interrupt)
	done := make(chan struct{})
	go func() {
		_ = r.cmd.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = r.cmd.Process.Kill()
		<-done
	}
	_ = os.Remove(r.socket)
}

// socketFor names a plugin's socket and refuses one too long to bind, which
// fails as "invalid argument" and names neither the path nor the limit.
func socketFor(id string) (string, error) {
	if err := os.MkdirAll(SocketDir, 0o700); err != nil {
		return "", fmt.Errorf("plugin: make the socket directory: %w", err)
	}

	socket := filepath.Join(SocketDir, id+".sock")
	if len(socket) >= maxSocketPath {
		return "", fmt.Errorf("plugin: socket path for %s is %d bytes and the limit is %d", id, len(socket), maxSocketPath)
	}
	_ = os.Remove(socket)
	return socket, nil
}

// logWriter puts a plugin's output in Dusk's log, attributed. A plugin that
// prints to stderr and is never read is a plugin nobody can debug.
type logWriter struct {
	log    *slog.Logger
	id     string
	stream string
}

func (w logWriter) Write(p []byte) (int, error) {
	w.log.Info("plugin output", "plugin", w.id, "stream", w.stream, "message", string(p))
	return len(p), nil
}
