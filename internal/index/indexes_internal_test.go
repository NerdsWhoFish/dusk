package index

import (
	"reflect"
	"testing"
)

func TestDriftIndexesMatchItsCorrelatedPredicates(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	want := map[string][]string{
		"idx_entities_observed_ref":            {"observed", "ref", "repository", "git_ref"},
		"idx_entities_observed_kind_namespace": {"observed", "kind", "namespace", "repository", "git_ref"},
		"idx_aliases_ref_scope":                {"ref", "repository", "git_ref", "alias"},
		"idx_aliases_alias_scope":              {"alias", "repository", "git_ref", "ref"},
	}
	for name, columns := range want {
		var got []string
		if err := db.gorm.Raw("SELECT name FROM pragma_index_info(?) ORDER BY seqno", name).Scan(&got).Error; err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !reflect.DeepEqual(got, columns) {
			t.Errorf("%s columns = %v, want %v", name, got, columns)
		}
	}
}
