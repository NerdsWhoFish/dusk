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
	"strings"
	"sync/atomic"
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

// SocketDir is where a Dusk puts its socket directory. Short on purpose: a
// unix socket address is capped near 104 bytes. A variable so a test can move
// it; the directory each Dusk binds in is minted underneath it (ADR-0054).
var SocketDir = "/tmp/dusk-plugins"

// maxSocketPath is the portable floor for a socket address, below the 108 that
// Linux allows and equal to what macOS and BSD do.
const maxSocketPath = 104

// startTimeout bounds how long a plugin gets to serve its socket before the
// host gives up. A plugin that cannot answer Describe cannot be scheduled.
const startTimeout = 15 * time.Second

// stopGrace is how long a plugin gets to shut down politely before it is
// killed.
const stopGrace = 5 * time.Second

// defaultInterval is used when a plugin is installed with no interval of its
// own. Hourly matches what the in-tree Kubernetes ingester chose.
const defaultInterval = time.Hour

// Exec is everything one plugin process needs to be started.
type Exec struct {
	ID     string
	Binary string

	// Dir is where the socket is bound. One directory per Dusk rather than one
	// per machine, so two of them cannot remove each other's (ADR-0054).
	Dir string

	Config *structpb.Struct
	Log    *slog.Logger

	// Kept carries what the previous process printed into the new one, so a
	// restart does not erase what the crash said on its way out.
	Kept *output
}

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
	output *output

	// exited is closed once the process has ended, and exitErr is why. One
	// goroutine owns Wait and everything else reads its result through this,
	// because a second Wait on one Cmd races the first.
	exited  chan struct{}
	exitErr error

	// stopped says Dusk asked for this exit, which is what tells the
	// supervisor a shutdown from a crash.
	stopped atomic.Bool
}

// maxAssetBytes bounds a plugin's JavaScript. A view is tens of kilobytes; a
// plugin sending more than this is not shipping a view.
const maxAssetBytes = 16 << 20

// Start execs a plugin and returns it ready to run. It calls Describe first,
// because a plugin that cannot describe itself cannot be scheduled, and failing
// here beats failing later in a sweep nobody is watching.
func Start(ctx context.Context, spec Exec) (*Running, error) {
	log := spec.Log
	if log == nil {
		log = slog.Default()
	}

	socket, err := socketFor(spec.Dir, spec.ID)
	if err != nil {
		return nil, err
	}

	token, err := mintToken()
	if err != nil {
		return nil, err
	}

	printed := spec.Kept
	if printed == nil {
		printed = &output{}
	}

	cmd := exec.Command(spec.Binary)
	cmd.Env = append(os.Environ(), SocketEnv+"="+socket, TokenEnv+"="+token)
	cmd.Stdout = logWriter{log: log, id: spec.ID, stream: "stdout", kept: printed}
	cmd.Stderr = logWriter{log: log, id: spec.ID, stream: "stderr", kept: printed}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("plugin: start %s: %w", spec.ID, err)
	}

	running := &Running{
		ID: spec.ID, config: spec.Config, interval: defaultInterval,
		cmd: cmd, socket: socket, log: log, output: printed,
		exited: make(chan struct{}),
	}

	// The one Wait. Everything that wants to know whether the process is still
	// there reads the channel, so nothing has to call Wait a second time and
	// lose the race for its result.
	go func() {
		running.exitErr = cmd.Wait()
		close(running.exited)
	}()

	conn, err := grpc.NewClient("unix://"+socket,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(presentToken(token)),
		grpc.WithStreamInterceptor(presentTokenStream(token)))
	if err != nil {
		running.stop()
		return nil, fmt.Errorf("plugin: connect to %s: %w", spec.ID, err)
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

// View is one place a plugin renders itself: either declared, and drawn by
// Dusk's own React, or an element whose JavaScript Dusk serves from its own
// origin (ADR-0020).
type View struct {
	Plugin  string   `json:"plugin"`
	Element string   `json:"element,omitempty"`
	Title   string   `json:"title,omitempty"`
	Source  string   `json:"source,omitempty"`
	Kinds   []string `json:"kinds,omitempty"`

	// Slot is where it mounts: an entity page, or the plugin's own.
	Slot string `json:"slot,omitempty"`

	// Spec makes this a declared view, needing no JavaScript from the plugin
	// and therefore no trust decision.
	Spec *ViewSpec `json:"spec,omitempty"`

	// Problem is why this contribution cannot render where it mounts. It is
	// shown in place of the view, because a contribution that silently draws
	// nothing is indistinguishable from one whose answer is empty.
	Problem string `json:"problem,omitempty"`
}

// declaredInPluginSlot is why a declared view cannot render on a plugin's own
// page. Without it the view falls through to the plugin's own "nothing to
// show" text, which reads as an empty answer rather than a broken contribution.
const declaredInPluginSlot = "a declared view draws a result set and this page has none, " +
	"so it cannot be shown here. Declare it on a home page as a `view` block with a query, " +
	"or ship it as an element the plugin draws itself"

// ViewSpec is a declared view. The vocabulary is closed on purpose: Dusk
// renders it, so an unknown layout has no rendering, and this is deliberately
// not a layout language.
type ViewSpec struct {
	Layout string      `json:"layout"`
	Fields []ViewField `json:"fields"`
	Empty  string      `json:"empty,omitempty"`
}

// ViewField is one thing a declared view shows.
type ViewField struct {
	Source string `json:"source"`
	Label  string `json:"label,omitempty"`
	Format string `json:"format,omitempty"`
}

// Slot names where a contribution mounts.
const (
	SlotEntity = "entity"
	SlotPlugin = "plugin"
)

// Views is what this plugin contributes to the UI.
func (r *Running) Views() []View {
	views := make([]View, 0, len(r.Describe.GetUi()))
	for _, ui := range r.Describe.GetUi() {
		view := View{
			Plugin: r.ID, Element: ui.GetElement(), Title: ui.GetTitle(),
			Kinds: ui.GetAppliesToKinds(), Slot: slotOf(ui.GetSlot()),
			Spec: specOf(ui.GetSpec()),
		}

		// A declared view has no asset to serve. An element with no asset is a
		// plugin that named JavaScript nobody could fetch, and mounting a tag
		// nothing defines renders an empty box with no explanation.
		if view.Spec == nil {
			asset, ok := r.assets[ui.GetAsset()]
			if !ok {
				continue
			}
			view.Source = "/plugin-assets/" + r.ID + "/" + asset.SHA + ".js"
		}

		// Refused rather than mounted: the spec goes, so nothing downstream
		// can draw it, and the reason takes its place (ADR-0064).
		if view.Spec != nil && view.Slot == SlotPlugin {
			view.Spec, view.Problem = nil, declaredInPluginSlot
		}
		views = append(views, view)
	}
	return views
}

func slotOf(slot duskv1alpha1.UISlot) string {
	if slot == duskv1alpha1.UISlot_UI_SLOT_PLUGIN {
		return SlotPlugin
	}
	return SlotEntity
}

func specOf(spec *duskv1alpha1.ViewSpec) *ViewSpec {
	if spec == nil {
		return nil
	}

	declared := &ViewSpec{
		Layout: strings.ToLower(strings.TrimPrefix(spec.GetLayout().String(), "VIEW_LAYOUT_")),
		Empty:  spec.GetEmpty(),
		Fields: make([]ViewField, 0, len(spec.GetFields())),
	}
	for _, field := range spec.GetFields() {
		declared.Fields = append(declared.Fields, ViewField{
			Source: field.GetSource(),
			Label:  field.GetLabel(),
			Format: strings.ToLower(strings.TrimPrefix(field.GetFormat().String(), "VIEW_FORMAT_")),
		})
	}
	return declared
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
		case <-r.exited:
			// Waiting out the whole timeout for a process that has already gone
			// spends fifteen seconds saying nothing, and buries the reason: a
			// plugin that exits on start almost always prints why first.
			return nil, fmt.Errorf("plugin: %s exited before it answered: %s%s", r.ID, r.wentAway(), r.said())
		case <-deadline.Done():
			return nil, fmt.Errorf("plugin: %s did not answer within %s: %w", r.ID, startTimeout, last)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// up reports whether the process is there to be asked. Every call that crosses
// the socket asks first, because a transport error names a path and a syscall
// where an operator needs to be told the plugin is not running.
func (r *Running) up() error {
	select {
	case <-r.exited:
		return fmt.Errorf("plugin: %s is not running: %s", r.ID, r.wentAway())
	default:
		return nil
	}
}

// wentAway says how the process ended, and is safe to read only once exited is
// closed. Wait reports a non-zero status as an error, so no error here means
// the plugin left of its own accord and said it was fine.
func (r *Running) wentAway() string {
	if r.exitErr != nil {
		return r.exitErr.Error()
	}
	return "its process exited"
}

// said is the tail of what the plugin printed, for a start failure whose cause
// is almost always in it.
func (r *Running) said() string {
	lines := r.output.recent()
	if len(lines) == 0 {
		return ""
	}
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}

	texts := make([]string, 0, len(lines))
	for _, line := range lines {
		texts = append(texts, line.Text)
	}
	return ". It printed: " + strings.Join(texts, "; ")
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

// Source identifies the upstream system this instance observes, so two pointed
// at one server queue behind each other. No key fields means no sharing, so a
// plugin that has not thought about this is not throttled by accident.
func (i *Instance) Source() string {
	declared := i.Describe.GetBudget().GetKeyFields()
	if len(declared) == 0 {
		return ""
	}

	config := i.config.AsMap()
	parts := make([]string, 0, len(declared)+1)
	parts = append(parts, i.Running.Name())
	for _, field := range declared {
		parts = append(parts, fmt.Sprint(config[field]))
	}
	return strings.Join(parts, "\x00")
}

// Budget is what the plugin says its source tolerates.
func (i *Instance) Budget() ingest.Budget {
	declared := i.Describe.GetBudget()
	return ingest.Budget{
		Concurrent: int(declared.GetMaxConcurrent()),
		Spacing:    time.Duration(declared.GetMinSpacingSeconds()) * time.Second,
	}
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
	// An observation is complete by contract, so anything it leaves out is
	// deleted. A dead or restarting process must fail, never answer empty
	// (ADR-0011, ADR-0054).
	if err := r.up(); err != nil {
		return nil, fmt.Errorf("plugin: %s could not be asked to observe: %w", name, err)
	}

	stream, err := r.client.Ingest(ctx, &duskv1alpha1.IngestRequest{Config: config})
	if err != nil {
		return nil, fmt.Errorf("plugin: %s ingest: %w", name, err)
	}

	observation := &ingest.Observation{}
	for {
		response, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			// A process that died just after its last batch is reported as a
			// failure even though the stream ended cleanly. That is the safe
			// direction: a failed run keeps what the catalog had.
			if err := r.up(); err != nil {
				return nil, fmt.Errorf("plugin: %s stopped part way through observing: %w", name, err)
			}
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

// Stop shuts the plugin down, politely first. It marks the exit as Dusk's
// doing, so the supervisor does not read a shutdown as a crash and start it
// again.
func (r *Running) Stop() { r.stop() }

func (r *Running) stop() {
	r.stopped.Store(true)

	if r.conn != nil {
		_ = r.conn.Close()
	}
	if r.cmd == nil || r.cmd.Process == nil {
		return
	}

	_ = r.cmd.Process.Signal(os.Interrupt)
	select {
	case <-r.exited:
	case <-time.After(stopGrace):
		_ = r.cmd.Process.Kill()
		<-r.exited
	}
	_ = os.Remove(r.socket)
}

// socketFor names a plugin's socket and refuses one too long to bind, which
// fails as "invalid argument" and names neither the path nor the limit.
func socketFor(dir, id string) (string, error) {
	if dir == "" {
		dir = SocketDir
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("plugin: make the socket directory: %w", err)
	}

	socket := filepath.Join(dir, id+".sock")
	if len(socket) >= maxSocketPath {
		return "", fmt.Errorf("plugin: socket path for %s is %d bytes and the limit is %d", id, len(socket), maxSocketPath)
	}
	_ = os.Remove(socket)
	return socket, nil
}

// logWriter puts a plugin's output in Dusk's log, attributed, and keeps it
// where the plugin's own page can show it. A plugin that prints to stderr and
// is never read is a plugin nobody can debug.
type logWriter struct {
	log    *slog.Logger
	id     string
	stream string
	kept   *output
}

func (w logWriter) Write(p []byte) (int, error) {
	w.log.Info("plugin output", "plugin", w.id, "stream", w.stream, "message", string(p))
	w.kept.write(w.stream, string(p))
	return len(p), nil
}
