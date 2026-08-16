package plugin

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/events"
	"github.com/NerdsWhoFish/dusk/internal/ingest"
	"github.com/NerdsWhoFish/dusk/pkg/proof"
	"github.com/NerdsWhoFish/dusk/pkg/secret"
)

// Rotation is the slice of the ingest scheduler a plugin needs, so installing
// one starts it observing without a restart.
type Rotation interface {
	Add(ingest.Ingester)
	Remove(name string)

	// Status is what each ingester last did. Without it a plugin reads as
	// "running" while every observation it attempts fails, which is the quiet
	// failure this catalog exists to prevent.
	Status() []ingest.Result
}

// Health is what a plugin's last run did, per configured instance.
type Health struct {
	// Instance is empty for the plugin's own configuration.
	Instance string `json:"instance,omitempty"`

	// Scope is where this instance's observations are stored. It is the join
	// onto how old they are, which only the index knows and only durably.
	Scope string `json:"scope,omitempty"`

	Entities  int       `json:"entities"`
	Relations int       `json:"relations"`
	At        time.Time `json:"at"`

	// Problem is why the last run failed. The catalog still serves what that
	// plugin last observed, so this is a warning rather than an outage.
	Problem string `json:"problem,omitempty"`

	// Failures is how many runs in a row have failed and Next is when the
	// rotation tries again, so "failing" is readable as a duration.
	Failures int       `json:"failures,omitempty"`
	Next     time.Time `json:"next"`
}

// Catalog is the slice of the index an action needs: what the entity is, and
// which plugin observed it. Declared here so invocation does not depend on how
// the graph is stored.
type Catalog interface {
	Get(ctx context.Context, gitRef, ref string) (*duskv1alpha1.Entity, error)
	ObservedBy(ctx context.Context, gitRef, ref string) ([]string, error)
}

// Manager owns what is installed and what is running.
type Manager struct {
	Store  *Store
	Market *Market
	Rota   Rotation
	Log    *slog.Logger

	// Catalog, Proof and Events are what actions need and observation does not,
	// so a deployment with no actions can leave them nil.
	Catalog Catalog
	Proof   *proof.Store
	Events  *events.Log

	// Restarts is how hard the supervisor tries to keep a plugin up. Zero
	// fields take the defaults (ADR-0054).
	Restarts RestartPolicy

	// Now exists so a test can assert on an event without matching a timestamp
	// it cannot predict.
	Now func() time.Time

	mu         sync.Mutex
	running    map[string]*Running
	supervised map[string]*supervision

	// sockets is this Dusk's own directory under SocketDir, minted on the first
	// start and removed on Stop. Unexported because Stop deletes it, and a
	// deployment pointing it somewhere of its own would lose that directory.
	sockets string
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// Offer is a marketplace listing with what Dusk knows about it locally.
type Offer struct {
	Listing

	Installed        bool   `json:"installed"`
	InstalledVersion string `json:"installed_version,omitempty"`

	// UpdateAvailable is a fact, not an action. An update never applies itself
	// (ADR-0042): a plugin that changed under an operator would change what it
	// does after the decision to trust it was made.
	UpdateAvailable bool `json:"update_available"`

	Running bool   `json:"running"`
	Problem string `json:"problem,omitempty"`

	// Process is what the supervisor knows about the plugin's own process:
	// whether it is up, how it last died, and whether anything is still trying.
	Process *Process `json:"process,omitempty"`

	// Health is what each of this plugin's instances last did. A plugin can be
	// running and failing every run, and the page has to say so.
	Health []Health `json:"health,omitempty"`

	// Fields is what the plugin says it needs configuring, so the UI renders a
	// form from a typed description rather than knowing about any plugin.
	Fields []Field `json:"fields,omitempty"`

	// Actions are what it can do, each denied until somebody turns it on. They
	// ride along with the offer because enabling one happens on this page.
	Actions []Action `json:"actions,omitempty"`

	// Views are what it renders on its own page, for anything that is about the
	// plugin rather than about one entity.
	Views []View `json:"views,omitempty"`

	Config    map[string]any            `json:"config,omitempty"`
	Instances map[string]map[string]any `json:"instances,omitempty"`

	// Set names which sensitive fields hold a value, by instance, with the
	// empty key being the plugin's own configuration. The names, never the
	// values: a write-only field still has to be able to say it is filled in.
	Set map[string][]string `json:"set,omitempty"`
}

// Field is one setting on a plugin's configuration form.
type Field struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Help     string `json:"help,omitempty"`
	Type     string `json:"type"`
	Required bool   `json:"required"`

	// Sensitive fields are entered here and nowhere else. ADR-0041 keeps them
	// off every surface an agent can reach.
	Sensitive bool `json:"sensitive"`
}

func fieldsOf(described *duskv1alpha1.DescribeResponse) []Field {
	declared := described.GetConfigFields()
	fields := make([]Field, 0, len(declared))
	for _, field := range declared {
		fields = append(fields, Field{
			Name:      field.GetName(),
			Label:     field.GetLabel(),
			Help:      field.GetHelp(),
			Type:      strings.ToLower(strings.TrimPrefix(field.GetType().String(), "CONFIG_FIELD_TYPE_")),
			Required:  field.GetRequired(),
			Sensitive: field.GetSensitive(),
		})
	}
	return fields
}

func (m *Manager) log() *slog.Logger {
	if m.Log != nil {
		return m.Log
	}
	return slog.Default()
}

// Restore starts every plugin already on disk. Called at boot, before anything
// reaches for the network, so an offline Dusk comes up with what it had.
func (m *Manager) Restore(ctx context.Context) {
	installed, err := m.Store.List()
	if err != nil {
		m.log().Error("could not read installed plugins", "error", err)
		return
	}

	for _, record := range installed {
		if err := m.launch(ctx, record); err != nil {
			// Not fatal. One broken plugin must not stop the others, and the
			// catalog serves whatever it last observed regardless.
			m.log().Error("could not start an installed plugin",
				"plugin", record.ID, "version", record.Version, "error", err)
		}
	}
}

// Available joins what is installed here with what the marketplace offers.
// Installed plugins come from disk and are listed even when GitHub cannot be
// reached, so a rate limit cannot hide what somebody is running.
func (m *Manager) Available(ctx context.Context) ([]Offer, error) {
	listings, listErr := m.Market.List(ctx)

	installed, err := m.Store.List()
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	offers := make([]Offer, 0, len(listings)+len(installed))
	seen := map[string]bool{}

	for _, listing := range listings {
		offer := Offer{Listing: listing}
		if record, err := m.Store.Read(listing.ID); err == nil {
			m.describeInstalled(&offer, *record, listing.Version)
		}
		m.describeRunning(&offer, listing.ID)

		seen[listing.ID] = true
		offers = append(offers, offer)
	}

	// Anything installed the listing did not mention: unreachable marketplace,
	// or a plugin whose repository is gone. Either way it is still running here.
	for _, record := range installed {
		if seen[record.ID] {
			continue
		}
		offer := Offer{Listing: Listing{ID: record.ID, Repository: record.Repository}}
		m.describeInstalled(&offer, record, "")
		m.describeRunning(&offer, record.ID)
		offers = append(offers, offer)
	}

	return offers, listErr
}

// Refresh asks GitHub now rather than reusing the day-old listing, which is
// what "check for updates" means.
func (m *Manager) Refresh(ctx context.Context) ([]Offer, error) {
	if _, err := m.Market.Refresh(ctx); err != nil {
		return nil, err
	}
	return m.Available(ctx)
}

// Checked is when the marketplace was last fetched.
func (m *Manager) Checked() time.Time { return m.Market.Checked() }

func (m *Manager) describeInstalled(offer *Offer, record Installed, latest string) {
	offer.Installed = true
	offer.InstalledVersion = record.Version
	offer.UpdateAvailable = latest != "" && latest != record.Version
	offer.Config = record.Config
	offer.Instances = record.Instances
	offer.Set = m.setSecrets(record)

	// Set for an installed plugin whether or not it is up, because a plugin
	// that will not start is exactly the one whose process needs describing.
	offer.Process = m.process(record.ID)
}

// setSecrets names which sensitive fields hold a value. Without it a write-only
// field is indistinguishable from an empty one, and the only way to find out is
// to overwrite it.
func (m *Manager) setSecrets(record Installed) map[string][]string {
	secrets, err := m.Store.ReadSecrets(record.ID)
	if err != nil {
		m.log().Error("could not read a plugin's sealed configuration",
			"plugin", record.ID, "error", err)
		return nil
	}

	set := map[string][]string{}
	if names := secrets.Names(""); len(names) > 0 {
		set[""] = names
	}
	for instance := range record.Instances {
		if names := secrets.Names(instance); len(names) > 0 {
			set[instance] = names
		}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

// describeRunning fills in what only a live plugin can say. The configuration
// fields come from Describe, so a stopped one offers no form rather than a
// stale one.
func (m *Manager) describeRunning(offer *Offer, id string) {
	running, ok := m.running[id]
	if !ok {
		return
	}

	// The supervisor's word, not the map's: a process being restarted is still
	// held here, because what it described is still true of the installed
	// version and its form should not blink out for the length of a backoff.
	offer.Running = serving(m.process(id))
	offer.Fields = fieldsOf(running.Describe)
	offer.Health = m.healthOf(id)

	var enabled []string
	if record, err := m.Store.Read(id); err == nil {
		enabled = record.Enabled
	}
	offer.Actions = actionsOf(running, enabled)

	for _, view := range running.Views() {
		if view.Slot == SlotPlugin {
			offer.Views = append(offer.Views, view)
		}
	}
}

// healthOf pulls this plugin's runs out of the rotation's status. Names are
// `plugin:<id>` with `:instance` appended, so the prefix is the plugin and
// what follows is which of its configurations.
func (m *Manager) healthOf(id string) []Health {
	prefix := "plugin:" + id

	var health []Health
	for _, result := range m.Rota.Status() {
		if result.Ingester != prefix && !strings.HasPrefix(result.Ingester, prefix+":") {
			continue
		}

		entry := Health{
			Instance:  strings.TrimPrefix(strings.TrimPrefix(result.Ingester, prefix), ":"),
			Scope:     ingest.Scope(result.Ingester),
			Entities:  result.Entities,
			Relations: result.Relations,
			At:        result.At,
			Failures:  result.Failures,
			Next:      result.Next,
		}
		if result.Err != nil {
			entry.Problem = result.Err.Error()
		}
		health = append(health, entry)
	}
	return health
}

// Install downloads a plugin and starts it. Installing an already-installed
// plugin is how an update is applied, which is why it stops the old process
// before the new binary lands on top of it.
func (m *Manager) Install(ctx context.Context, id string) (*Installed, error) {
	listings, err := m.Market.List(ctx)
	if err != nil {
		return nil, err
	}

	var listing *Listing
	for i := range listings {
		if listings[i].ID == id {
			listing = &listings[i]
			break
		}
	}
	if listing == nil {
		return nil, fmt.Errorf("plugin: no plugin named %q is offered by the configured orgs", id)
	}

	m.stop(id)

	record, err := m.Market.Install(ctx, m.Store, *listing)
	if err != nil {
		return nil, err
	}
	if err := m.launch(ctx, *record); err != nil {
		return record, err
	}
	return record, nil
}

// Uninstall stops a plugin and removes it from disk.
func (m *Manager) Uninstall(id string) error {
	m.stop(id)
	m.forget(id)
	return m.Store.Remove(id)
}

// Settings is a plugin's non-sensitive configuration and the fields it
// declares, which is everything a caller needs to change one of them without
// having to know anything about the plugin.
func (m *Manager) Settings(id, instance string) (map[string]any, []Field, error) {
	record, err := m.Store.Read(id)
	if err != nil {
		return nil, nil, fmt.Errorf("plugin: %q is not installed", id)
	}

	described, running := m.Describe(id)
	if !running {
		return nil, nil, fmt.Errorf("plugin: %s is not running, so what it can be configured with is not known", id)
	}

	settings := record.Config
	if instance != "" {
		settings = record.Instances[instance]
	}
	return settings, fieldsOf(described), nil
}

// Configure saves one of a plugin's configurations and restarts it with it.
// Empty names the plugin's own; a named instance is how one plugin observes a
// second source without being installed twice.
func (m *Manager) Configure(ctx context.Context, id, instance string, config map[string]any) error {
	record, err := m.Store.Read(id)
	if err != nil {
		return fmt.Errorf("plugin: %q is not installed", id)
	}

	// Only the plugin knows which of its fields are secret, and it says so in
	// Describe. Guessing would either write a credential into the record or
	// seal something that was never sensitive.
	described, running := m.Describe(id)
	if !running {
		return fmt.Errorf("plugin: %s is not running, and only a running plugin can say which of its fields are secret", id)
	}

	secrets, err := m.Store.ReadSecrets(id)
	if err != nil {
		return err
	}

	plain, sealed := split(config, sensitiveOf(described), secrets.For(instance))

	// Stopped before the record changes, so the instances being removed from
	// the rotation are the ones that were added to it.
	m.stop(id)

	if instance == "" {
		record.Config = plain
		secrets.Config = sealed
	} else {
		if record.Instances == nil {
			record.Instances = map[string]map[string]any{}
		}
		if secrets.Instances == nil {
			secrets.Instances = map[string]map[string]secret.String{}
		}
		record.Instances[instance] = plain
		secrets.Instances[instance] = sealed
	}

	if err := m.Store.WriteSecrets(id, secrets); err != nil {
		return err
	}
	if err := m.Store.Write(*record); err != nil {
		return err
	}
	return m.launch(ctx, *record)
}

// Describe returns what a running plugin says about itself, which is where the
// UI gets the configuration form and the actions to offer.
func (m *Manager) Describe(id string) (*duskv1alpha1.DescribeResponse, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	running, ok := m.running[id]
	if !ok {
		return nil, false
	}
	return running.Describe, true
}

// Views returns what running plugins contribute for an entity kind. An empty
// kind list on a contribution means every kind, which is the common case.
func (m *Manager) Views(kind string) []View {
	m.mu.Lock()
	defer m.mu.Unlock()

	var views []View
	for _, running := range m.running {
		for _, view := range running.Views() {
			if view.Slot != SlotEntity {
				continue
			}
			if len(view.Kinds) == 0 || slices.Contains(view.Kinds, kind) {
				views = append(views, view)
			}
		}
	}
	return views
}

// Contributions is everything one plugin contributes to the UI, in both slots.
// A caller narrows by Slot: which of them a surface may mount is that surface's
// question, and asking it here once meant two answers that could disagree.
func (m *Manager) Contributions(id string) []View {
	m.mu.Lock()
	running, ok := m.running[id]
	m.mu.Unlock()

	if !ok {
		return nil
	}
	return running.Views()
}

// Asset returns a plugin's JavaScript by digest, for serving from Dusk's own
// origin so a plugin's view never reaches the network.
func (m *Manager) Asset(plugin, sha string) (Asset, bool) {
	m.mu.Lock()
	running, ok := m.running[plugin]
	m.mu.Unlock()

	if !ok {
		return Asset{}, false
	}
	return running.Asset(sha)
}

// Stop shuts every plugin down, for a graceful exit.
func (m *Manager) Stop() {
	m.mu.Lock()
	running := make([]*Running, 0, len(m.running))
	for id, plugin := range m.running {
		running = append(running, plugin)
		m.Rota.Remove(plugin.Name())
		delete(m.running, id)
	}
	// Every supervision, not only the ones with a process: a plugin waiting out
	// a restart has no entry in running and would otherwise start after this.
	for id := range m.supervised {
		m.halted(id)
	}
	sockets := m.sockets
	m.mu.Unlock()

	for _, plugin := range running {
		plugin.Stop()
	}
	if sockets != "" {
		_ = os.RemoveAll(sockets)
	}
}

func (m *Manager) start(ctx context.Context, record Installed) error {
	secrets, err := m.Store.ReadSecrets(record.ID)
	if err != nil {
		return err
	}

	config, err := configured(record.Config, secrets.For(""))
	if err != nil {
		return fmt.Errorf("plugin: %s has unusable configuration: %w", record.ID, err)
	}

	dir, err := m.socketDir()
	if err != nil {
		return err
	}

	running, err := Start(ctx, Exec{
		ID: record.ID, Binary: m.Store.Binary(record.ID), Dir: dir,
		Config: config, Log: m.log(), Kept: m.printed(record.ID),
	})
	if err != nil {
		return err
	}

	m.mu.Lock()
	if m.running == nil {
		m.running = map[string]*Running{}
	}
	m.running[record.ID] = running
	m.mu.Unlock()

	// Migration happens before anything is scheduled, and the rotation is built
	// from what it produced: stripping a credential out of the record without
	// picking up the sealed copy would start an instance with nothing.
	migrated, sealed, err := m.rehome(record, running.Describe)
	if err != nil {
		m.log().Error("could not seal a credential found in the plain record",
			"plugin", record.ID, "error", err)
	} else if sealed != nil {
		record, secrets = *migrated, sealed
	}

	for _, instance := range instancesOf(running, record, secrets) {
		m.Rota.Add(instance)
		m.log().Info("plugin started",
			"plugin", record.ID, "version", running.Version, "scope", instance.Name())
	}

	// Last, so nothing reads the plugin as up before what it observes is in the
	// rotation. Supervised for as long as Dusk runs rather than for as long as
	// the request that installed it, which ends when the browser is answered.
	go m.supervise(context.WithoutCancel(ctx), record.ID, running, m.up(record.ID))
	return nil
}

// socketDir is the directory this Dusk binds in, minted on first use. One per
// Dusk rather than one per machine: sockets are named by plugin id, so two
// sharing a directory each remove the other's on start and on stop (ADR-0054).
func (m *Manager) socketDir() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.sockets != "" {
		return m.sockets, nil
	}
	if err := os.MkdirAll(SocketDir, 0o700); err != nil {
		return "", fmt.Errorf("plugin: make the socket directory: %w", err)
	}

	dir, err := os.MkdirTemp(SocketDir, "")
	if err != nil {
		return "", fmt.Errorf("plugin: make this Dusk's socket directory: %w", err)
	}
	m.sockets = dir
	return dir, nil
}

// printed is what this plugin's last process said, handed to the next one so a
// restart does not erase the output that explains why it happened.
func (m *Manager) printed(id string) *output {
	m.mu.Lock()
	defer m.mu.Unlock()

	if running, ok := m.running[id]; ok {
		return running.output
	}
	return nil
}

// configured merges the sealed half back in. A plugin receives its credential
// here and only here, over its own socket, which is the one path ADR-0023
// allows a secret to take.
func configured(plain map[string]any, secrets map[string]secret.String) (*structpb.Struct, error) {
	merged := make(map[string]any, len(plain)+len(secrets))
	maps.Copy(merged, plain)
	for name, value := range secrets {
		merged[name] = value.Reveal()
	}
	return structpb.NewStruct(merged)
}

// rehome seals a credential an older Dusk wrote in the clear, answering with
// what it produced. It builds fresh maps rather than editing what it was given:
// a map is shared, and stripping one in place emptied the caller's instances.
func (m *Manager) rehome(record Installed, described *duskv1alpha1.DescribeResponse) (*Installed, *Secrets, error) {
	sensitive := sensitiveOf(described)
	if len(sensitive) == 0 {
		return nil, nil, nil
	}

	secrets, err := m.Store.ReadSecrets(record.ID)
	if err != nil {
		return nil, nil, err
	}

	moved := false
	move := func(plain map[string]any, stored map[string]secret.String) (map[string]any, map[string]secret.String) {
		kept, sealed := split(plain, sensitive, stored)
		if len(kept) != len(plain) {
			moved = true
		}
		return kept, sealed
	}

	migrated := record
	migrated.Config, secrets.Config = move(record.Config, secrets.Config)

	if len(record.Instances) > 0 {
		migrated.Instances = make(map[string]map[string]any, len(record.Instances))
		secrets.Instances = map[string]map[string]secret.String{}
		for name, config := range record.Instances {
			migrated.Instances[name], secrets.Instances[name] = move(config, secrets.Instances[name])
		}
	}

	if !moved {
		return nil, nil, nil
	}
	if err := m.Store.WriteSecrets(record.ID, secrets); err != nil {
		return nil, nil, err
	}
	m.log().Info("moved a plugin credential out of the plain record and into the sealed store",
		"plugin", record.ID)

	if err := m.Store.Write(migrated); err != nil {
		return nil, nil, err
	}
	return &migrated, secrets, nil
}

// instancesOf turns a record into everything that should be in the rotation.
// The plugin's own config is always one, so an install with no named instances
// behaves exactly as it did before there were any.
func instancesOf(running *Running, record Installed, secrets *Secrets) []*Instance {
	instances := []*Instance{{Running: running, config: running.config}}

	for name, config := range record.Instances {
		built, err := configured(config, secrets.For(name))
		if err != nil {
			continue
		}
		instances = append(instances, &Instance{Running: running, instance: name, config: built})
	}
	return instances
}

func (m *Manager) stop(id string) {
	m.mu.Lock()
	running, ok := m.running[id]
	if ok {
		delete(m.running, id)
	}
	// Ended before the process is signalled, so a supervisor that wakes on the
	// exit already knows this was asked for.
	m.halted(id)
	m.mu.Unlock()

	if !ok {
		return
	}

	// Every instance, not just the plugin's own: leaving one behind puts a
	// dead connection in the rotation, which fails forever rather than loudly.
	record, err := m.Store.Read(id)
	if err == nil {
		for name := range record.Instances {
			m.Rota.Remove((&Instance{Running: running, instance: name}).Name())
		}
	}
	m.Rota.Remove(running.Name())
	running.Stop()
}
