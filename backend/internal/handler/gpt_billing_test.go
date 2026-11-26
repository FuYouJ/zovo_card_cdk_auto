package handler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tuzi/cdk-recharge-system/internal/config"
	"github.com/tuzi/cdk-recharge-system/internal/db"
)

func TestBillingUsesOwnerCredentialWhenGeneralKeyMissing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DB_PATH", filepath.Join(dir, "billing.sqlite"))
	t.Setenv("INSTALL_MODE", "wizard")
	t.Setenv("SETUP_BOOTSTRAP_TOKEN", "isolated-billing-install")
	t.Setenv("CARD_API_KEY", "")
	t.Setenv("CARD_API_BASE", "https://zovocard.com")
	t.Setenv("GPT_REDEEM_API_ORIGIN", "https://zovocard.com")
	if err := db.Init(&config.DatabaseConfig{}); err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	file := filepath.Join(dir, "key")
	if err := os.WriteFile(file, []byte("test-owner-key"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GPT_REDEEM_API_KEY_FILE", file)
	if client, err := authenticatedCardPlatformClient(); err != nil || client == nil {
		t.Fatalf("billing could not reuse the owner credential: %v", err)
	}

	t.Setenv("GPT_REDEEM_API_ORIGIN", "https://sandbox.zovocard.com")
	if _, err := authenticatedCardPlatformClient(); err == nil {
		t.Fatal("accepted a key file for a different card platform environment")
	}
	t.Setenv("CARD_API_KEY", "test-billing-key")
	if client, err := authenticatedCardPlatformClient(); err != nil || client == nil {
		t.Fatalf("configured billing credential should have priority: %v", err)
	}
}
