package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tuzi/cdk-recharge-system/internal/config"
	"github.com/tuzi/cdk-recharge-system/internal/db"
)

func TestGPTCardPolicyUsesVerifiedPreviewAndKeepsOriginalAllocation(t *testing.T) {
	t.Setenv("DB_PATH", filepath.Join(t.TempDir(), "redeem.sqlite"))
	t.Setenv("INSTALL_MODE", "wizard")
	t.Setenv("SETUP_BOOTSTRAP_TOKEN", "isolated-test-install-token")
	t.Setenv(restrictedCDKRemarkEnv, "GPT")
	t.Setenv("GPT_REDEEM_API_ORIGIN", "https://zovocard.com")
	t.Setenv("GPT_REDEEM_API_KEY_FILE", "")
	if err := db.Init(&config.DatabaseConfig{}); err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	router := gin.New()
	router.POST("/redeem", PublicCDKRedeem)
	request := func(body string) int {
		r := httptest.NewRequest(http.MethodPost, "/redeem", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Redemption-Device", "device")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w.Code
	}
	body := `{"redemption_token":"verified-token","preflight_token":"account-check","product":"grok","card_id":123}`
	if got := request(body); got != http.StatusConflict {
		t.Fatalf("unverified client data bypassed policy: %d", got)
	}
	ctx := context.Background()
	preview := json.RawMessage(`{"code":0,"data":{"redemption_token":"verified-token","product":"gpt","plan":"go"}}`)
	if err := rememberOwnerPreview(ctx, preview, "device"); err != nil {
		t.Fatal(err)
	}
	if got := request(body); got != http.StatusServiceUnavailable {
		t.Fatalf("missing key took the public path: %d", got)
	}
	token, device := ownerDigest("verified-token"), ownerDigest("device")
	now := time.Now().Unix()
	for _, card := range []int64{11, 12} {
		s, err := db.LockCDKOwnerCard(ctx, token, device, ownerDigest("account-check"), card, now)
		if err != nil || s.CardID != 11 || s.PreflightDigest != ownerDigest("account-check") {
			t.Fatal("retry changed the original selected card", err)
		}
	}
}
