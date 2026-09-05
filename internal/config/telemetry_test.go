package config_test

import (
	"testing"

	"github.com/NerdsWhoFish/dusk/internal/config"
)

func TestFaroCollectorConfiguration(t *testing.T) {
	for _, raw := range []string{"http://collector.example/collect", "https://user:secret@collector.example/collect", "https://collector.example/collect?token=secret", "https://collector.example/collect#secret"} {
		t.Run(raw, func(t *testing.T) {
			_, err := config.Load(env(map[string]string{
				"DUSK_PRIVATE_HOST": "https://dusk.example.com", "DUSK_ENCRYPTION_KEY": validKey(t), "DUSK_FARO_URL": raw,
			}))
			if err == nil {
				t.Fatal("accepted unsafe public collector configuration")
			}
		})
	}
	cfg, err := config.Load(env(map[string]string{
		"DUSK_PRIVATE_HOST": "https://dusk.example.com", "DUSK_ENCRYPTION_KEY": validKey(t),
		"DUSK_FARO_URL": "https://collector.example/collect/public-id", "DUSK_ENVIRONMENT": "staging",
	}))
	if err != nil || cfg.FaroURL != "https://collector.example/collect/public-id" || cfg.Environment != "staging" {
		t.Fatalf("collector configuration = %v, %v", cfg, err)
	}
}
