package plugin

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"google.golang.org/protobuf/types/known/structpb"

	duskv1alpha1 "github.com/FetchHQ/dusk-plugin-sdk/gen/dusk/v1alpha1"

	"github.com/FetchHQ/dusk/internal/ingest"
)

// Rotation is the slice of the ingest scheduler a plugin needs, so installing
// one starts it observing without a restart.
type Rotation interface {
	Add(ingest.Ingester)
	Remove(name string)
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

// Available lists the marketplace, annotated with what is installed here.
func (m *Manager) Available(ctx context.Context) ([]Offer, error) {
	listings, err := m.Market.List(ctx)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	offers := make([]Offer, 0, len(listings))
	for _, listing := range listings {
		offer := Offer{Listing: listing}
		if record, err := m.Store.Read(listing.ID); err == nil {
			offer.Installed = true
			offer.InstalledVersion = record.Version
			offer.UpdateAvailable = listing.Version != "" && listing.Version != record.Version
		}
		_, offer.Running = m.running[listing.ID]
		offers = append(offers, offer)
	}
	return offers, nil
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
	record, err := m.Store.Read(id)
	if err != nil {
		return fmt.Errorf("plugin: %q is not installed", id)
	}

	record.Config = config
	if err := m.Store.Write(*record); err != nil {
		return err
	}

	m.stop(id)
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

	m.Rota.Add(running)
	m.log().Info("plugin started",
		"plugin", record.ID, "version", running.Version, "scope", running.Name())
	return nil
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
	m.Rota.Remove(running.Name())
	running.Stop()
}
