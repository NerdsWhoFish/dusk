package tokens_test

import (
	"testing"

	"github.com/NerdsWhoFish/dusk/internal/tokens"
)

func TestCount(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want int
	}{
		{"empty", "", 0},
		{"english", "Hello world!", 3},
		{"word", "antidisestablishmentarianism", 6},
		{"arithmetic", "2 + 2 = 4", 7},
		{"unicode", "お誕生日おめでとう", 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := tokens.Count(tc.text)
			if err != nil || got != tc.want {
				t.Fatalf("Count(%q) = %d, %v; want %d", tc.text, got, err, tc.want)
			}
		})
	}
}
