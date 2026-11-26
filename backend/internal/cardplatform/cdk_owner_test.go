package cardplatform

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGPTCardSelectionCannotUseUnmarkedCard(t *testing.T) {
	var selected int64
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "private-owner-test-key" {
			t.Error("missing server-only authorization")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/openapi/v1/cards":
			q := r.URL.Query()
			if q.Get("remark") != "GPT" || q.Get("status") != "ACTIVE" || q.Get("archived") != "0" {
				t.Error("inventory was not narrowed")
			}
			fmt.Fprint(w, `{"code":0,"data":{"total":5,"list":[{"id":11,"remark":"gpt备用","status":"ACTIVE"},{"id":12,"remark":"GPT","status":"ACTIVE"},{"id":3,"remark":"customer GPT","status":"ACTIVE"},{"id":4,"remark":" GpT-01 ","status":"ACTIVE"},{"id":5,"remark":"GPT","status":"FROZEN"}]}}`)
		case "/openapi/v1/gpt-direct/card-pool/schedule":
			q := r.URL.Query()
			if q.Get("product") != "gpt" || q.Get("plan") != "go" || q.Get("card_mode") != "auto_existing" {
				t.Error("incorrect plan scheduling request")
			}
			fmt.Fprint(w, `{"code":0,"data":{"candidates":[{"card_id":3,"rank":1,"skip":false},{"card_id":11,"rank":2,"skip":true},{"card_id":4,"rank":3,"skip":false},{"card_id":5,"rank":4,"skip":false},{"card_id":12,"rank":5,"skip":false}]}}`)
		case "/openapi/v1/gpt-direct/cdks/redeem":
			if r.Header.Get("X-Redemption-Device") != "same-device" {
				t.Error("device was not preserved")
			}
			var body OwnerCDKRedeemRequest
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("malformed request")
			}
			selected = body.CardID
			fmt.Fprint(w, `{"code":0,"data":{"order":{"order_id":7}}}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer app.Close()
	ctx := context.Background()
	client := New(Config{SiteBase: app.URL, APIKey: "private-owner-test-key"})
	allowed, err := client.ActiveCardsWithRemarkPrefix(ctx, "GPT")
	if err != nil || len(allowed) != 3 || !allowed[4] || !allowed[11] || !allowed[12] {
		t.Fatal("case-insensitive remark prefix filtering failed", err)
	}
	pick, err := client.PickExistingGPTCard(ctx, "go", allowed)
	if err != nil || pick != 4 {
		t.Fatalf("wrong eligible card: %d %v", pick, err)
	}
	if code, _, err := client.OwnerCDKRedeem(ctx, OwnerCDKRedeemRequest{CardID: pick}, "same-device"); err != nil || code != 200 || selected != 4 {
		t.Fatal("owner redemption did not lock the approved card", err)
	}
	delete(allowed, 4)
	delete(allowed, 12)
	if pick, err := client.PickExistingGPTCard(ctx, "go", allowed); err != nil || pick != 0 {
		t.Fatal("a skipped candidate must not be used", err)
	}
	if _, _, err := client.OwnerCDKRedeem(ctx, OwnerCDKRedeemRequest{}, "same-device"); err == nil {
		t.Fatal("owner redemption must reject fallback selection")
	}
}
