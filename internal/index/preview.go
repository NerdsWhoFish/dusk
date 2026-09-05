package index

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"

	"gorm.io/gorm"
)

var previewPattern = regexp.MustCompile(`^refs/pull/([A-Za-z0-9-]+)/([A-Za-z0-9_.-]+)/([1-9][0-9]*)/head$`)
var legacyPreviewPattern = regexp.MustCompile(`^refs/pull/([1-9][0-9]*)/head$`)

// ErrPreviewAmbiguous refuses a repository-local PR number shared by two repos.
var ErrPreviewAmbiguous = errors.New("preview number belongs to more than one repository; open its repository-specific preview link")

type previewRow struct {
	Repository string `gorm:"primaryKey"`
	GitRef     string `gorm:"primaryKey"`
}

func (previewRow) TableName() string { return "preview_scopes" }

// PreviewRef names a repository's PR snapshot independently of its head commit.
func PreviewRef(repository string, number int) string {
	return fmt.Sprintf("refs/pull/%s/%d/head", repository, number)
}

// ParsePreviewRef reads the repository and PR from a qualified preview ref.
func ParsePreviewRef(ref string) (repository string, number int, ok bool) {
	parts := previewPattern.FindStringSubmatch(ref)
	if parts == nil || parts[2] == "." || parts[2] == ".." {
		return "", 0, false
	}
	number, err := strconv.Atoi(parts[3])
	if err != nil {
		return "", 0, false
	}
	return parts[1] + "/" + parts[2], number, true
}

// IsPreviewRef also recognizes old links, resolved only when unambiguous.
func IsPreviewRef(ref string) bool {
	_, _, ok := ParsePreviewRef(ref)
	return ok || legacyPreviewPattern.MatchString(ref)
}

// ResolvePreview distinguishes empty snapshots from missing or closed previews.
func (db *DB) ResolvePreview(ctx context.Context, ref string) (string, error) {
	query := db.gorm.WithContext(ctx).Model(&previewRow{})
	if _, _, ok := ParsePreviewRef(ref); ok {
		query = query.Where("git_ref = ?", ref)
	} else if parts := legacyPreviewPattern.FindStringSubmatch(ref); parts != nil {
		query = query.Where("git_ref LIKE ?", "refs/pull/%/%/"+parts[1]+"/head")
	} else {
		return "", fmt.Errorf("index: invalid preview ref %q", ref)
	}
	var matches []previewRow
	if err := query.Limit(2).Find(&matches).Error; err != nil {
		return "", fmt.Errorf("index: resolve preview: %w", err)
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("index: preview is unavailable or closed: %w", ErrNotFound)
	case 1:
		return matches[0].GitRef, nil
	default:
		return "", ErrPreviewAmbiguous
	}
}

func putPreview(tx *gorm.DB, repository, gitRef string) error {
	owner, _, preview := ParsePreviewRef(gitRef)
	if !preview {
		return nil
	}
	if owner != repository {
		return errors.New("index: preview ref belongs to a different repository")
	}
	if err := tx.Create(&previewRow{Repository: repository, GitRef: gitRef}).Error; err != nil {
		return fmt.Errorf("index: record preview: %w", err)
	}
	return nil
}
