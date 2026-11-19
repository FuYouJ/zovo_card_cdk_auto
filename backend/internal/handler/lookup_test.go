package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tuzi/cdk-recharge-system/internal/config"
	"github.com/tuzi/cdk-recharge-system/internal/db"
)

func TestNormalizeLookupCodes(t *testing.T) {
	got := normalizeLookupCodes(append(
		[]string{"sxc-aaaa-bbbb-cccc-dddd", "SXC-AAAA-BBBB-CCCC-DDDD", "ab"},
		splitLookupText("sxc-eeee-ffff-gggg-hhhh\nsxc-iiii-jjjj-kkkk-llll,sxc-eeee-ffff-gggg-hhhh")...,
	))
	want := []string{
		"SXC-AAAA-BBBB-CCCC-DDDD",
		"SXC-EEEE-FFFF-GGGG-HHHH",
		"SXC-IIII-JJJJ-KKKK-LLLL",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizeLookupCodes = %#v, want %#v", got, want)
	}
}

func TestApplyLookupFailure(t *testing.T) {
	reusable := cdkLookupResult{CDKCode: "SXC-1"}
	applyLookupFailure(&reusable, "Session 无效", true)
	if reusable.Status != "failed" || !reusable.CanResubmit || reusable.Used {
		t.Fatalf("reusable: %+v", reusable)
	}
	if reusable.Notes != "Session 无效" {
		t.Fatalf("notes=%q", reusable.Notes)
	}

	locked := cdkLookupResult{CDKCode: "SXC-2"}
	applyLookupFailure(&locked, "", false)
	if locked.Status != "failed" || locked.CanResubmit || locked.Used {
		t.Fatalf("locked: %+v", locked)
	}
}

func TestSplitLookupTextEmpty(t *testing.T) {
	if got := splitLookupText("  \n"); got != nil {
		t.Fatalf("expected nil, got %#v", got)
	}
}

func TestCDKLookupFetchesAndCachesExactUpstreamStatus(t *testing.T) {
	const prefix = "ZC-0123456789"
	const code = prefix + "-0123456789"
	const sibling = prefix + "-9876543210"
	const missing = prefix + "-1111111111"
	const failing = "ZC-CCCCCCCCCC-3333333333"
	const local = "ZC-DDDDDDDDDD-5555555555"

	t.Setenv("DB_PATH", filepath.Join(t.TempDir(), "lookup.sqlite"))
	t.Setenv("INSTALL_MODE", "wizard")
	t.Setenv("SETUP_BOOTSTRAP_TOKEN", "isolated-lookup-install")
	oldDB := db.DB
	if err := db.Init(&config.DatabaseConfig{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(); db.DB = oldDB })

	var upstreamRequests atomic.Int32
	var consumed atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamRequests.Add(1)
		query := r.URL.Query()
		if r.Method != http.MethodGet || r.URL.Path != "/openapi/v1/gpt-direct/cdks" ||
			r.Header.Get("X-API-Key") != "isolated-status-key" || len(query) != 3 ||
			query.Get("page") != "1" || query.Get("page_size") != "20" {
			t.Error("unexpected upstream request")
			w.WriteHeader(400)
			return
		}
		if query.Get("q") == "ZC-CCCCCCCCCC" {
			w.WriteHeader(503)
			_, _ = w.Write([]byte(`{"code":503,"msg":"provider temporarily unavailable"}`))
			return
		}
		if query.Get("q") != prefix {
			t.Error("lookup did not send only the expected prefix")
			w.WriteHeader(400)
			return
		}
		status := "unused"
		if consumed.Load() {
			status = "consumed"
		}
		list := []map[string]any{
			{"id": 1, "code": sibling, "code_prefix": prefix, "status": "consumed", "plan": "go"},
			{"id": 2, "code_prefix": prefix, "status": "consumed"},
			{"id": 3, "full_code": code, "code_prefix": prefix, "status": status, "plan": "plus"},
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"list": list, "total": len(list)}})
	}))
	t.Cleanup(upstream.Close)
	t.Setenv("CARD_API_BASE", upstream.URL)
	t.Setenv("CARD_API_KEY", "isolated-status-key")

	if err := db.SaveCardplatformCDKCodeWithStatus(88, local, "", "plus", 0, "consumed"); err != nil {
		t.Fatal(err)
	}
	// A preflight binding alone is not proof that an unused card was redeemed.
	if err := db.BindCDKSession(code, "", `{"user":{"email":"preflight@example.test"}}`); err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/cdk", LookupCDKStatus)
	router.GET("/cdk", LookupCDKStatus)
	router.POST("/cdk/batch", LookupCDKStatusBatch)
	request := func(method, path, body string, want int) []byte {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != want || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("status=%d want=%d body=%s", w.Code, want, w.Body.String())
		}
		return w.Body.Bytes()
	}
	decode := func(data []byte, dest any) {
		t.Helper()
		if err := json.Unmarshal(data, dest); err != nil {
			t.Fatal(err)
		}
	}

	var single cdkLookupResult
	decode(request("POST", "/cdk", `{"code":" `+strings.ToLower(code)+` "}`, 200), &single)
	if single.Status != "unused" || single.Used || !single.CanResubmit || single.Plan != "plus" || upstreamRequests.Load() != 1 {
		t.Fatalf("initial result: %+v, upstream requests: %d", single, upstreamRequests.Load())
	}
	if _, _, _, _, ok := db.LookupStoredCDKByCode(code); ok {
		t.Fatal("public status lookup must not add a card to the issued inventory")
	}

	consumed.Store(true)
	var batch struct{ Results []cdkLookupResult }
	decode(request("POST", "/cdk/batch", `{"codes":["`+code+`","`+local+`","`+missing+`","`+failing+`"]}`, 200), &batch)
	if len(batch.Results) != 4 {
		t.Fatalf("batch returned %d results", len(batch.Results))
	}
	var statuses []string
	for _, result := range batch.Results {
		statuses = append(statuses, result.Status)
	}
	if !reflect.DeepEqual(statuses, []string{"unused", "used", "unknown", "unavailable"}) || upstreamRequests.Load() != 3 {
		t.Fatalf("batch statuses=%v upstream requests=%d", statuses, upstreamRequests.Load())
	}
	snapshot, err := db.GetCachedCDKStatus(context.Background(), code, time.Now().Add(-lookupCacheTTL))
	if err != nil || snapshot == nil || snapshot.Status != "unused" {
		t.Fatalf("cache=%+v, err=%v", snapshot, err)
	}
	if _, err := db.DB.Exec(`UPDATE cardplatform_cdk_status_cache SET checked_at=? WHERE code_hash=?`,
		time.Now().Add(-2*lookupCacheTTL).UnixMilli(), db.HashCDKCode(code)); err != nil {
		t.Fatal(err)
	}
	single = cdkLookupResult{}
	decode(request("GET", "/cdk?cdk_code="+code, "", 200), &single)
	if single.Status != "used" || !single.Used || single.CanResubmit || upstreamRequests.Load() != 4 {
		t.Fatalf("refreshed result: %+v, upstream requests: %d", single, upstreamRequests.Load())
	}
	request("POST", "/cdk", `{"code":"`+missing+`"}`, 404)
	request("POST", "/cdk", `{"code":"`+failing+`"}`, 503)
	request("POST", "/cdk", `{"code":"`+prefix+`"}`, 404)
	if upstreamRequests.Load() != 6 {
		t.Fatalf("unexpected upstream requests: %d", upstreamRequests.Load())
	}
}
