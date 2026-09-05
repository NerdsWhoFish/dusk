package web_test

import "testing"

func TestCatalogSemanticsAndMutedContrast(t *testing.T) {
	for _, measured := range render(t, 1280, 800, false) {
		for _, issue := range measured.Accessibility {
			t.Errorf("%s: %s", measured.Page, issue)
		}
	}
}
