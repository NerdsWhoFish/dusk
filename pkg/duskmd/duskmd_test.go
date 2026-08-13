package duskmd_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/NerdsWhoFish/dusk-plugin-sdk/conformance"

	"github.com/NerdsWhoFish/dusk/pkg/duskmd"
)

var testProvenance = duskmd.Provenance{
	Version:    "0123456789abcdef0123456789abcdef01234567",
	ObservedAt: time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC),
}

const validRoot = `---
dusk: v1alpha1
namespace: platform
kind: service
name: checkout
title: Checkout API
relations:
  - type: runs_on
    to: host:platform/runner-1
attributes:
  tier: "1"
include:
  - services/*/dusk.md
---

# Checkout API

Takes money.
`

func TestParseRoot(t *testing.T) {
	file, err := duskmd.ParseRoot("dusk.md", []byte(validRoot), testProvenance)
	if err != nil {
		t.Fatalf("ParseRoot: %v", err)
	}

	entity := file.Entity
	for _, field := range []struct{ name, got, want string }{
		{"ref", entity.GetRef(), "service:platform/checkout"},
		{"kind", entity.GetKind(), "service"},
		{"namespace", entity.GetNamespace(), "platform"},
		{"name", entity.GetName(), "checkout"},
		{"title", entity.GetTitle(), "Checkout API"},
		{"description", entity.GetDescription(), "# Checkout API\n\nTakes money."},
		{"provenance.source", entity.GetProvenance().GetSource(), duskmd.Source},
		{"provenance.version", entity.GetProvenance().GetVersion(), testProvenance.Version},
	} {
		if field.got != field.want {
			t.Errorf("%s = %q, want %q", field.name, field.got, field.want)
		}
	}

	if got := entity.GetProvenance().GetObservedAt().AsTime(); !got.Equal(testProvenance.ObservedAt) {
		t.Errorf("provenance.observed_at = %v, want %v", got, testProvenance.ObservedAt)
	}
	if got := entity.GetAttributes().GetFields()["tier"].GetStringValue(); got != "1" {
		t.Errorf("attributes.tier = %q, want %q", got, "1")
	}
	if got, want := file.Include, []string{"services/*/dusk.md"}; len(got) != 1 || got[0] != want[0] {
		t.Errorf("include = %v, want %v", got, want)
	}

	if len(file.Relations) != 1 {
		t.Fatalf("relations = %d, want 1", len(file.Relations))
	}
	relation := file.Relations[0]
	if got, want := relation.GetTo(), "host:platform/runner-1"; got != want {
		t.Errorf("relation.to = %q, want %q", got, want)
	}
	if got, want := relation.GetType(), "runs_on"; got != want {
		t.Errorf("relation.type = %q, want %q", got, want)
	}
	if got := relation.GetProvenance().GetSource(); got != duskmd.Source {
		t.Errorf("relation.provenance.source = %q, want %q", got, duskmd.Source)
	}
}

// ADR-0026 derives the ref from kind, namespace and name rather than accepting
// it as a field, so the mismatch the SDK's conformance check exists to catch
// cannot be authored in the first place.
func TestADR0026_RefIsDerivedNotAuthored(t *testing.T) {
	t.Run("an authored ref is rejected", func(t *testing.T) {
		assertViolation(t, failRoot(t, "dusk.md", withField(validRoot, "ref: service:platform/checkout")), "ref", "derived")
	})

	t.Run("the derived ref round-trips through the SDK", func(t *testing.T) {
		file, err := duskmd.ParseRoot("dusk.md", []byte(validRoot), testProvenance)
		if err != nil {
			t.Fatalf("ParseRoot: %v", err)
		}
		kind, namespace, name, err := conformance.ParseRef(file.Entity.GetRef())
		if err != nil {
			t.Fatalf("ParseRef(%q): %v", file.Entity.GetRef(), err)
		}
		if kind != file.Entity.GetKind() || namespace != file.Entity.GetNamespace() || name != file.Entity.GetName() {
			t.Errorf("ref %q does not round-trip to (%q, %q, %q)",
				file.Entity.GetRef(), file.Entity.GetKind(), file.Entity.GetNamespace(), file.Entity.GetName())
		}
	})

	t.Run("a separator in an identity field is rejected", func(t *testing.T) {
		tests := []struct{ name, field, line string }{
			{"a colon in kind would truncate the ref", "kind", "kind: web:service"},
			{"a slash in kind would move the separator", "kind", "kind: web/service"},
			{"a slash in namespace would split the name", "namespace", "namespace: platform/eu"},
			{"a slash in name is rejected by the SDK", "name", "name: checkout/api"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				assertViolation(t, failRoot(t, "dusk.md", replaceField(validRoot, tt.field, tt.line)), tt.field, "must not contain")
			})
		}
	})
}

// ADR-0026 allows a file to declare only relations originating from its own
// entity, so a repository can never assert a fact about an entity it does not
// own. Authoring `from` is the way that rule would be broken.
func TestADR0026_RelationsOnlyOriginateFromTheFilesOwnEntity(t *testing.T) {
	t.Run("an authored from is rejected", func(t *testing.T) {
		file := strings.Replace(validRoot,
			"  - type: runs_on\n",
			"  - type: runs_on\n    from: service:platform/other\n", 1)
		errs := failRoot(t, "dusk.md", file)
		assertViolation(t, errs, "from", "this file's own entity")
	})

	t.Run("from is always the declaring entity", func(t *testing.T) {
		parsed, err := duskmd.ParseRoot("dusk.md", []byte(validRoot), testProvenance)
		if err != nil {
			t.Fatalf("ParseRoot: %v", err)
		}
		if got, want := parsed.Relations[0].GetFrom(), parsed.Entity.GetRef(); got != want {
			t.Errorf("relation.from = %q, want %q", got, want)
		}
	})
}

// ADR-0026 keeps includes one level deep. Recursive includes would reintroduce
// the unbounded crawl ADR-0004 rejected, one file at a time.
func TestADR0026_IncludeIsOnlyHonoredInTheRootFile(t *testing.T) {
	assertViolation(t, failIncluded(t, "services/api/dusk.md", validRoot, "platform"), "include", "root dusk.md")
}

// ADR-0026 rejects include patterns that leave the repository. Consent
// expressed by a file at a known location means nothing if that file can point
// anywhere on the host.
func TestADR0026_IncludeCannotEscapeTheRepository(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    string
	}{
		{"a parent segment is rejected", "../../etc/dusk.md", "must not contain a .. segment"},
		{"a parent segment mid-pattern is rejected", "services/../../dusk.md", "must not contain a .. segment"},
		{"an absolute path is rejected", "/etc/passwd", "must be relative"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := strings.Replace(validRoot, "  - services/*/dusk.md", "  - "+tt.pattern, 1)
			errs := failRoot(t, "dusk.md", file)
			assertViolation(t, errs, "include[0]", tt.want)
		})
	}
}

// ADR-0026 decodes strictly. A silently ignored misspelling produces a catalog
// that is confidently wrong, which is worse than one that fails to load.
func TestADR0026_UnknownFieldsAreRejected(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		field string
		want  string
	}{
		{"a misspelled field is named", "kidn: service", "kidn", "not a field this format defines"},
		{"a derived field says so", "description: inline", "description", "prose below the frontmatter"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertViolation(t, failRoot(t, "dusk.md", withField(validRoot, tt.line)), tt.field, tt.want)
		})
	}

	t.Run("an unknown field inside a relation is rejected", func(t *testing.T) {
		file := strings.Replace(validRoot,
			"  - type: runs_on\n",
			"  - type: runs_on\n    weight: 3\n", 1)
		errs := failRoot(t, "dusk.md", file)
		assertViolation(t, errs, "weight", "not a field this format defines")
	})
}

// ADR-0026 makes the prose the description and refuses a competing field,
// because two authorable sources for one value is how documentation rots.
func TestADR0026_DescriptionIsTheProse(t *testing.T) {
	file, err := duskmd.ParseRoot("dusk.md", []byte(validRoot), testProvenance)
	if err != nil {
		t.Fatalf("ParseRoot: %v", err)
	}
	if got, want := file.Entity.GetDescription(), "# Checkout API\n\nTakes money."; got != want {
		t.Errorf("description = %q, want %q", got, want)
	}
}

// ADR-0026 has included files inherit the root's namespace so that a repository
// owning many entities does not repeat it in every file.
func TestADR0026_NamespaceIsInheritedFromTheRoot(t *testing.T) {
	const included = `---
dusk: v1alpha1
kind: service
name: payments
---

Handles payments.
`

	t.Run("an included file inherits the root namespace", func(t *testing.T) {
		file, err := duskmd.ParseIncluded("services/payments/dusk.md", []byte(included), "platform", testProvenance)
		if err != nil {
			t.Fatalf("ParseIncluded: %v", err)
		}
		if got, want := file.Entity.GetRef(), "service:platform/payments"; got != want {
			t.Errorf("ref = %q, want %q", got, want)
		}
	})

	t.Run("an included file may override the namespace", func(t *testing.T) {
		file, err := duskmd.ParseIncluded("services/payments/dusk.md",
			[]byte(withField(included, "namespace: billing")), "platform", testProvenance)
		if err != nil {
			t.Fatalf("ParseIncluded: %v", err)
		}
		if got, want := file.Entity.GetRef(), "service:billing/payments"; got != want {
			t.Errorf("ref = %q, want %q", got, want)
		}
	})

	t.Run("a root file must declare its own namespace", func(t *testing.T) {
		assertViolation(t, failRoot(t, "dusk.md", included), "namespace", "required")
	})
}

func TestRequiredFields(t *testing.T) {
	tests := []struct {
		name  string
		file  string
		field string
		want  string
	}{
		{
			name:  "a missing schema version is rejected",
			file:  strings.Replace(validRoot, "dusk: v1alpha1\n", "", 1),
			field: "dusk",
			want:  `must be "v1alpha1"`,
		},
		{
			name:  "an unknown schema version is rejected",
			file:  replaceField(validRoot, "dusk", "dusk: v2"),
			field: "dusk",
			want:  `must be "v1alpha1"`,
		},
		{
			name:  "a missing kind is rejected",
			file:  strings.Replace(validRoot, "kind: service\n", "", 1),
			field: "kind",
			want:  "is required",
		},
		{
			name:  "a missing name is rejected",
			file:  strings.Replace(validRoot, "name: checkout\n", "", 1),
			field: "name",
			want:  "is required",
		},
		{
			name:  "a relation without a type is rejected",
			file:  strings.Replace(validRoot, "  - type: runs_on\n    to:", "  - to:", 1),
			field: "relations[0].type",
			want:  "is required",
		},
		{
			name:  "a relation pointing at a non-ref is rejected",
			file:  strings.Replace(validRoot, "to: host:platform/runner-1", "to: runner-1", 1),
			field: "relations[0].to",
			want:  "kind:namespace/name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertViolation(t, failRoot(t, "dusk.md", tt.file), tt.field, tt.want)
		})
	}
}

func TestMalformedFiles(t *testing.T) {
	tests := []struct {
		name string
		file string
		want string
	}{
		{"a file with no frontmatter is rejected", "# Just prose\n", "must open with a frontmatter block"},
		{"an unterminated frontmatter block is rejected", "---\nkind: service\n", "never closed"},
		{"an empty frontmatter block is rejected", "---\n---\n\nprose\n", "frontmatter is empty"},
		{"a non-mapping frontmatter is rejected", "---\n- a\n- b\n---\n", "must be a mapping"},
		{"invalid YAML is rejected", "---\nkind: [unterminated\n---\n", "not valid YAML"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := failRoot(t, "dusk.md", tt.file)
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tt.want)
			}
			if !strings.Contains(err.Error(), "dusk.md") {
				t.Errorf("error = %q, want it to name the file", err.Error())
			}
		})
	}
}

func TestFrontmatterDelimiters(t *testing.T) {
	tests := []struct {
		name string
		file string
	}{
		{"carriage returns are tolerated", "---\r\ndusk: v1alpha1\r\nnamespace: platform\r\nkind: service\r\nname: checkout\r\n---\r\n\r\nProse.\r\n"},
		{"a byte order mark is tolerated", "\xEF\xBB\xBF---\ndusk: v1alpha1\nnamespace: platform\nkind: service\nname: checkout\n---\n\nProse.\n"},
		{"a dot delimiter closes the block", "---\ndusk: v1alpha1\nnamespace: platform\nkind: service\nname: checkout\n...\n\nProse.\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, err := duskmd.ParseRoot("dusk.md", []byte(tt.file), testProvenance)
			if err != nil {
				t.Fatalf("ParseRoot: %v", err)
			}
			if got, want := file.Entity.GetDescription(), "Prose."; got != want {
				t.Errorf("description = %q, want %q", got, want)
			}
		})
	}
}

func TestTitleDefaultsToName(t *testing.T) {
	file, err := duskmd.ParseRoot("dusk.md", []byte(strings.Replace(validRoot, "title: Checkout API\n", "", 1)), testProvenance)
	if err != nil {
		t.Fatalf("ParseRoot: %v", err)
	}
	if got, want := file.Entity.GetTitle(), "checkout"; got != want {
		t.Errorf("title = %q, want %q", got, want)
	}
}

func TestAttributesAcceptYAMLScalars(t *testing.T) {
	const file = `---
dusk: v1alpha1
namespace: platform
kind: service
name: checkout
attributes:
  replicas: 3
  public: true
  since: 2026-01-02
  regions: [eu, us]
  owner:
    team: payments
---

Prose.
`

	parsed, err := duskmd.ParseRoot("dusk.md", []byte(file), testProvenance)
	if err != nil {
		t.Fatalf("ParseRoot: %v", err)
	}

	fields := parsed.Entity.GetAttributes().GetFields()
	if got, want := fields["replicas"].GetNumberValue(), 3.0; got != want {
		t.Errorf("replicas = %v, want %v", got, want)
	}
	if !fields["public"].GetBoolValue() {
		t.Error("public = false, want true")
	}
	// A YAML date resolves to a time.Time, which has no protobuf Struct
	// equivalent, so the parser records it in RFC 3339 rather than failing.
	if got, want := fields["since"].GetStringValue(), "2026-01-02T00:00:00Z"; got != want {
		t.Errorf("since = %q, want %q", got, want)
	}
	if got := fields["regions"].GetListValue().GetValues(); len(got) != 2 {
		t.Errorf("regions = %v, want 2 values", got)
	}
	if got, want := fields["owner"].GetStructValue().GetFields()["team"].GetStringValue(), "payments"; got != want {
		t.Errorf("owner.team = %q, want %q", got, want)
	}
}

func TestEveryViolationIsReportedAtOnce(t *testing.T) {
	const file = `---
dusk: v2
kidn: service
name: checkout
---

Prose.
`

	errs := failRoot(t, "dusk.md", file)
	if len(errs) < 4 {
		t.Fatalf("reported %d violations, want at least 4 (schema version, unknown field, missing kind, missing namespace): %v", len(errs), errs)
	}
}

func TestErrorNamesFileFieldAndLine(t *testing.T) {
	errs := failRoot(t, "services/api/dusk.md", replaceField(validRoot, "dusk", "dusk: v2"))
	violation := findViolation(t, errs, "dusk", `must be "v1alpha1"`)

	if violation.Path != "services/api/dusk.md" {
		t.Errorf("path = %q, want %q", violation.Path, "services/api/dusk.md")
	}
	// The dusk key is the first line of the frontmatter, which is line 2 of the
	// file: an author needs the line they can actually go and edit.
	if violation.Line != 2 {
		t.Errorf("line = %d, want 2", violation.Line)
	}
	if !strings.Contains(violation.Error(), `field "dusk"`) {
		t.Errorf("message = %q, want it to name the field", violation.Error())
	}
}

func failRoot(t *testing.T, filePath, content string) duskmd.Errors {
	t.Helper()
	file, err := duskmd.ParseRoot(filePath, []byte(content), testProvenance)
	return mustFail(t, file, err)
}

func failIncluded(t *testing.T, filePath, content, namespace string) duskmd.Errors {
	t.Helper()
	file, err := duskmd.ParseIncluded(filePath, []byte(content), namespace, testProvenance)
	return mustFail(t, file, err)
}

func mustFail(t *testing.T, file *duskmd.File, err error) duskmd.Errors {
	t.Helper()
	if err == nil {
		t.Fatalf("parsed successfully, want a violation: %+v", file)
	}
	errs, ok := errors.AsType[duskmd.Errors](err)
	if !ok {
		t.Fatalf("error is %T, want duskmd.Errors: %v", err, err)
	}
	return errs
}

func assertViolation(t *testing.T, errs duskmd.Errors, field, want string) {
	t.Helper()
	_ = findViolation(t, errs, field, want)
}

func findViolation(t *testing.T, errs duskmd.Errors, field, want string) duskmd.Error {
	t.Helper()
	for _, violation := range errs {
		if violation.Field == field && strings.Contains(violation.Message, want) {
			return violation
		}
	}
	t.Fatalf("no violation on field %q containing %q, got:\n%v", field, want, errs)
	return duskmd.Error{}
}

// withField inserts a line at the top of the frontmatter block.
func withField(file, line string) string {
	if line == "" {
		return file
	}
	return strings.Replace(file, "---\n", "---\n"+line+"\n", 1)
}

// replaceField swaps the line starting with the given key for a new one.
func replaceField(file, key, line string) string {
	for existing := range strings.SplitSeq(file, "\n") {
		if strings.HasPrefix(existing, key+":") {
			return strings.Replace(file, existing, line, 1)
		}
	}
	return file
}
