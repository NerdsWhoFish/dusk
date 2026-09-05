package plugin

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// maxAsset bounds a download. A plugin binary is tens of megabytes at worst,
// and without a ceiling a wrong URL fills the volume the index lives on.
const maxAsset = 256 << 20

// ErrInvalidID identifies an ID that cannot safely name one plugin directory.
var ErrInvalidID = errors.New("plugin: invalid plugin ID")

var pluginID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func validateID(id string) error {
	if !pluginID.MatchString(id) {
		return fmt.Errorf("%w: %q", ErrInvalidID, id)
	}
	return nil
}

// Installed is the record of a plugin on disk, written beside its binary so
// what is running is answerable without inspecting the binary (ADR-0042).
type Installed struct {
	ID          string    `json:"id"`
	Repository  string    `json:"repository"`
	Version     string    `json:"version"`
	SHA256      string    `json:"sha256"`
	InstalledAt time.Time `json:"installed_at"`

	// Config is what the plugin is configured with. Sensitive fields are not
	// here: those live in the encrypted store (ADR-0023).
	Config map[string]any `json:"config,omitempty"`

	// Instances are additional configurations of the same plugin, each with its
	// own scope. One Kubernetes plugin observes one cluster, so a second
	// cluster is a second instance rather than a second install.
	Instances map[string]map[string]any `json:"instances,omitempty"`

	// Enabled names the actions somebody deliberately turned on. Declaring one
	// does not grant it: installing a plugin must not hand over capability by
	// itself (ADR-0015).
	Enabled []string `json:"enabled,omitempty"`
}

// Store is where installed plugins live between restarts. In Kubernetes this
// is the PVC, so a rollout does not re-download anything.
type Store struct {
	Dir string

	// Master seals the sensitive half of a plugin's configuration. Without it
	// a plugin declaring a credential field cannot be configured at all, which
	// is the correct refusal: the alternative is writing it in the clear.
	Master []byte
}

func (s *Store) dir(id string) string    { return filepath.Join(s.Dir, id) }
func (s *Store) binary(id string) string { return filepath.Join(s.dir(id), "plugin") }
func (s *Store) record(id string) string { return filepath.Join(s.dir(id), "installed.json") }

func (s *Store) versionBinary(id, digest string) string {
	return filepath.Join(s.dir(id), "versions", digest, "plugin")
}

// Binary is the path to an installed plugin's executable.
func (s *Store) Binary(id string) string { return s.binary(id) }

// BinaryFor resolves the immutable binary selected by record. Records written
// before versioned storage fall back to the legacy path until their next update.
func (s *Store) BinaryFor(record Installed) string {
	if record.SHA256 != "" {
		versioned := s.versionBinary(record.ID, record.SHA256)
		if _, err := os.Stat(versioned); err == nil || !errors.Is(err, os.ErrNotExist) {
			return versioned
		}
		if _, err := os.Stat(s.binary(record.ID)); err != nil && errors.Is(err, os.ErrNotExist) {
			return versioned
		}
	}
	return s.binary(record.ID)
}

// List returns every installed plugin, so Dusk starts from disk rather than
// from GitHub and boots with no network at all.
func (s *Store) List() ([]Installed, error) {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("plugin: read %s: %w", s.Dir, err)
	}

	var installed []Installed
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		record, err := s.Read(entry.Name())
		if err != nil {
			continue
		}
		installed = append(installed, *record)
	}
	return installed, nil
}

// Read returns one plugin's record.
func (s *Store) Read(id string) (*Installed, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	body, err := os.ReadFile(s.record(id))
	if err != nil {
		return nil, err
	}

	var record Installed
	if err := json.Unmarshal(body, &record); err != nil {
		return nil, fmt.Errorf("plugin: read the record for %s: %w", id, err)
	}
	if record.ID != id {
		return nil, fmt.Errorf("plugin: record ID %q does not match directory %q", record.ID, id)
	}
	return &record, nil
}

// Write records an installed plugin.
func (s *Store) Write(record Installed) error {
	if err := validateID(record.ID); err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir(record.ID), 0o700); err != nil {
		return fmt.Errorf("plugin: make the directory for %s: %w", record.ID, err)
	}

	body, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(s.dir(record.ID), s.record(record.ID), body)
}

func (s *Store) stageBinary(id, digest string, binary []byte) error {
	if err := validateID(id); err != nil {
		return err
	}
	versioned := s.versionBinary(id, digest)
	versionDir := filepath.Dir(versioned)
	if err := os.MkdirAll(versionDir, 0o700); err != nil {
		return fmt.Errorf("plugin: make the directory for %s: %w", id, err)
	}

	if info, err := os.Stat(versioned); err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o100 == 0 {
			return fmt.Errorf("plugin: immutable binary %s is not an executable regular file", versioned)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("plugin: inspect %s: %w", id, err)
	}

	if err := atomicWriteMode(versionDir, versioned, binary, 0o700); err != nil {
		return fmt.Errorf("plugin: write %s: %w", id, err)
	}
	return nil
}

// Remove deletes an installed plugin from disk.
func (s *Store) Remove(id string) error {
	if err := validateID(id); err != nil {
		return err
	}
	if err := os.RemoveAll(s.dir(id)); err != nil {
		return fmt.Errorf("plugin: remove %s: %w", id, err)
	}
	return nil
}

// Stage downloads and verifies a release, then writes its binary under an
// immutable digest path without changing which version is active.
func (m *Market) Stage(ctx context.Context, store *Store, listing Listing) (*Installed, error) {
	if err := validateID(listing.ID); err != nil {
		return nil, err
	}
	release, err := m.latest(ctx, listing.Repository)
	if err != nil {
		return nil, fmt.Errorf("plugin: find a release for %s: %w", listing.Repository, err)
	}

	wanted := assetName(listing.ID)
	asset, ok := find(release.Assets, wanted)
	if !ok {
		return nil, fmt.Errorf("plugin: %s %s publishes no asset named %q, so there is nothing to install for this machine",
			listing.Repository, release.TagName, wanted)
	}

	archive, err := m.download(ctx, asset.URL)
	if err != nil {
		return nil, err
	}

	sum := sha256.Sum256(archive)
	digest := hex.EncodeToString(sum[:])
	if err := m.verify(ctx, release, wanted, digest); err != nil {
		return nil, err
	}

	binary, err := extract(archive, listing.ID)
	if err != nil {
		return nil, err
	}

	if err := store.stageBinary(listing.ID, digest, binary); err != nil {
		return nil, err
	}

	record := Installed{
		ID: listing.ID, Repository: listing.Repository,
		Version: release.TagName, SHA256: digest, InstalledAt: time.Now(),
	}

	// Installing over an existing plugin is how an update is applied, so its
	// configuration has to survive. Writing a fresh record loses it, and the
	// operator is left retyping a credential to install a bug fix.
	if previous, err := store.Read(listing.ID); err == nil {
		record.Config = previous.Config
		record.Instances = previous.Instances
		record.Enabled = previous.Enabled
	}

	return &record, nil
}

// verify checks the download against the release's checksum file. A release
// with no checksums is refused rather than trusted: the plugin release
// convention requires GoReleaser precisely so this file exists.
func (m *Market) verify(ctx context.Context, release *releaseJSON, asset, digest string) error {
	sums, ok := find(release.Assets, "checksums.txt")
	if !ok {
		return fmt.Errorf("plugin: release %s publishes no checksums.txt, so the download cannot be verified", release.TagName)
	}

	body, err := m.download(ctx, sums.URL)
	if err != nil {
		return fmt.Errorf("plugin: read checksums: %w", err)
	}

	for line := range strings.SplitSeq(string(body), "\n") {
		published, name, found := strings.Cut(strings.TrimSpace(line), "  ")
		if !found || name != asset {
			continue
		}
		if published != digest {
			return fmt.Errorf("plugin: %s does not match its published checksum: got %s, expected %s", asset, digest, published)
		}
		return nil
	}
	return fmt.Errorf("plugin: checksums.txt does not mention %s", asset)
}

func (m *Market) download(ctx context.Context, url string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if token := m.token(ctx); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := m.httpClient().Do(request)
	if err != nil {
		return nil, fmt.Errorf("plugin: download %s: %w", url, err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("plugin: download %s: %s", url, response.Status)
	}
	return io.ReadAll(io.LimitReader(response.Body, maxAsset))
}

// extract pulls the plugin binary out of the release archive, refusing a path
// that would escape the directory it is being written into.
func extract(archive []byte, id string) ([]byte, error) {
	zipped, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("plugin: read the archive: %w", err)
	}
	defer func() { _ = zipped.Close() }()

	wanted := Prefix + id
	reader := tar.NewReader(zipped)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("plugin: the archive contains no file named %q", wanted)
		}
		if err != nil {
			return nil, fmt.Errorf("plugin: read the archive: %w", err)
		}

		name := filepath.Base(header.Name)
		if header.Typeflag != tar.TypeReg || name != wanted {
			continue
		}
		return io.ReadAll(io.LimitReader(reader, maxAsset))
	}
}

func find(assets []assetJSON, name string) (assetJSON, bool) {
	for _, asset := range assets {
		if asset.Name == name {
			return asset, true
		}
	}
	return assetJSON{}, false
}
