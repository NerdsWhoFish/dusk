package plugin

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	duskv1alpha1 "github.com/NerdsWhoFish/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/NerdsWhoFish/dusk/internal/ingest"
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

	Entities  int       `json:"entities"`
	Relations int       `json:"relations"`
	At        time.Time `json:"at"`

	// Problem is why the last run failed. The catalog still serves what that
	// plugin last observed, so this is a warning rather than an outage.
	Problem string `json:"problem,omitempty"`
}

// Manager owns what is installed and what is running.
type Manager struct {
	Store  *Store
	Market *Market
	Rota   Rotation
	Log    *slog.Logger

	mu      sync.Mutex
	running map[string]*Running
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

	// Health is what each of this plugin's instances last did. A plugin can be
	// running and failing every run, and the page has to say so.
	Health []Health `json:"health,omitempty"`

	// Fields is what the plugin says it needs configuring, so the UI renders a
	// form from a typed description rather than knowing about any plugin.
	Fields []Field `json:"fields,omitempty"`

	Config    map[string]any            `json:"config,omitempty"`
	Instances map[string]map[string]any `json:"instances,omitempty"`
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
		if err := m.start(ctx, record); err != nil {
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
}

// describeRunning fills in what only a live plugin can say. The configuration
// fields come from Describe, so a stopped one offers no form rather than a
// stale one.
func (m *Manager) describeRunning(offer *Offer, id string) {
	running, ok := m.running[id]
	if !ok {
		return
	}
	offer.Running = true
	offer.Fields = fieldsOf(running.Describe)
	offer.Health = m.healthOf(id)
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
			Entities:  result.Entities,
			Relations: result.Relations,
			At:        result.At,
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
	if err := m.start(ctx, *record); err != nil {
		return record, err
	}
	return record, nil
}

// Uninstall stops a plugin and removes it from disk.
func (m *Manager) Uninstall(id string) error {
	m.stop(id)
	return m.Store.Remove(id)
}

// Configure saves a plugin's configuration and restarts it with it.
func (m *Manager) Configure(ctx context.Context, id string, config map[string]any) error {
	return m.configure(ctx, id, "", config)
}

// ConfigureInstance saves one named configuration of a plugin, which is how the
// same plugin observes a second cluster without being installed twice.
func (m *Manager) ConfigureInstance(ctx context.Context, id, instance string, config map[string]any) error {
	return m.configure(ctx, id, instance, config)
}

func (m *Manager) configure(ctx context.Context, id, instance string, config map[string]any) error {
	record, err := m.Store.Read(id)
	if err != nil {
		return fmt.Errorf("plugin: %q is not installed", id)
	}

	// Stopped before the record changes, so the instances being removed from
	// the rotation are the ones that were added to it.
	m.stop(id)

	if instance == "" {
		record.Config = config
	} else {
		if record.Instances == nil {
			record.Instances = map[string]map[string]any{}
		}
		record.Instances[instance] = config
	}

	if err := m.Store.Write(*record); err != nil {
		return err
	}
	return m.start(ctx, *record)
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
			if len(view.Kinds) == 0 || slices.Contains(view.Kinds, kind) {
				views = append(views, view)
			}
		}
	}
	return views
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
	m.mu.Unlock()

	for _, plugin := range running {
		plugin.Stop()
	}
}

func (m *Manager) start(ctx context.Context, record Installed) error {
	config, err := structpb.NewStruct(record.Config)
	if err != nil {
		return fmt.Errorf("plugin: %s has unusable configuration: %w", record.ID, err)
	}

	running, err := Start(ctx, record.ID, m.Store.Binary(record.ID), config, m.log())
	if err != nil {
		return err
	}

	m.mu.Lock()
	if m.running == nil {
		m.running = map[string]*Running{}
	}
	m.running[record.ID] = running
	m.mu.Unlock()

	for _, instance := range instancesOf(running, record) {
		m.Rota.Add(instance)
		m.log().Info("plugin started",
			"plugin", record.ID, "version", running.Version, "scope", instance.Name())
	}
	return nil
}

// instancesOf turns a record into everything that should be in the rotation.
// The plugin's own config is always one, so an install with no named instances
// behaves exactly as it did before there were any.
func instancesOf(running *Running, record Installed) []*Instance {
	instances := []*Instance{{Running: running, config: running.config}}

	for name, config := range record.Instances {
		built, err := structpb.NewStruct(config)
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
	m.mu.Unlock()

	if !ok {
		return
	}

	// Every instance, not just the plugin's own: leaving one behind puts a
	// dead connection in the rotation, which fails forever rather than loudly.
	record, err := m.Store.Read(id)
	if err == nil {
		for _, instance := range instancesOf(running, *record) {
			m.Rota.Remove(instance.Name())
		}
	}
	m.Rota.Remove(running.Name())
	running.Stop()
}
