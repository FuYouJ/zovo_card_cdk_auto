package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tuzi/cdk-recharge-system/internal/cardplatform"
	"github.com/tuzi/cdk-recharge-system/internal/db"
)

const restrictedCDKRemarkEnv = "GPT_REDEEM_CARD_REMARK"

type ownerCDKPreview struct {
	Token   string    `json:"redemption_token"`
	Product string    `json:"product"`
	Plan    string    `json:"plan"`
	Expires time.Time `json:"expires_at"`
}

func ownerDigest(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

func ownerProduct(product, plan string) string {
	switch strings.ToLower(strings.TrimSpace(product)) {
	case "gpt", "chatgpt":
		return "gpt"
	case "grok", "x", "claude":
		return strings.ToLower(strings.TrimSpace(product))
	case "":
		plan = strings.ToLower(strings.TrimSpace(plan))
		for _, prefix := range []string{"grok", "x", "claude"} {
			if strings.HasPrefix(plan, prefix+"_") {
				return prefix
			}
		}
		// Legacy ChatGPT previews can omit the product. Ambiguous plans must
		// take the restricted path so a missing field never bypasses the rule.
		return "gpt"
	default:
		return ""
	}
}

func rememberOwnerPreview(ctx context.Context, raw json.RawMessage, device string) error {
	var outer struct {
		Code *int            `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &outer); err != nil {
		return err
	}
	if outer.Code != nil && *outer.Code != 0 {
		return nil
	}
	payload := raw
	if len(outer.Data) != 0 {
		payload = outer.Data
	}
	var preview ownerCDKPreview
	if err := json.Unmarshal(payload, &preview); err != nil {
		return err
	}
	product := ownerProduct(preview.Product, preview.Plan)
	if preview.Token == "" || preview.Plan == "" || product == "" {
		return errors.New("preview missing owner card selection metadata")
	}
	now := time.Now()
	expires := now.Add(15 * time.Minute)
	if !preview.Expires.IsZero() && preview.Expires.Before(expires) {
		expires = preview.Expires
	}
	if !expires.After(now) {
		return errors.New("preview already expired")
	}
	return db.SaveCDKOwnerPreview(ctx, ownerDigest(preview.Token), ownerDigest(device), product, preview.Plan, expires.Unix(), now.Unix())
}

func redeemByGPTRemark(c *gin.Context, body map[string]any) bool {
	remark := strings.TrimSpace(os.Getenv(restrictedCDKRemarkEnv))
	if remark == "" {
		return false
	}
	c.Header("Cache-Control", "no-store")
	token := str(body["redemption_token"])
	device := deviceFrom(c)
	tokenHash, deviceHash := ownerDigest(token), ownerDigest(device)
	ctx := c.Request.Context()
	session, err := db.LoadCDKOwnerSession(ctx, tokenHash, deviceHash, time.Now().Unix())
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "请重新验证卡密，再提交兑换。"})
		return true
	}
	if session.Product != "gpt" {
		return false
	}
	preflight := str(body["preflight_token"])
	if preflight == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请先完成 ChatGPT 账号预检。"})
		return true
	}
	owner, err := configuredCDKOwner()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "ChatGPT 兑换暂不可用，请稍后重试。"})
		return true
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	allowed, err := owner.ActiveCardsWithRemarkPrefix(ctx, remark)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "暂时无法核对 GPT 用卡名单，请稍后重试。"})
		return true
	}
	blocked, err := db.ListActiveBlockedCardIDs()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "暂时无法核对 GPT 用卡名单，请稍后重试。"})
		return true
	}
	for _, id := range blocked {
		delete(allowed, id)
	}
	preflightHash := ownerDigest(preflight)
	if session.CardID == 0 && len(allowed) > 0 {
		card, err := owner.PickExistingGPTCard(ctx, session.Plan, allowed)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "卡网选卡预览暂不可用，请稍后重试。"})
			return true
		}
		if card > 0 {
			session, err = db.LockCDKOwnerCard(ctx, tokenHash, deviceHash, preflightHash, card, time.Now().Unix())
			if err != nil {
				c.JSON(http.StatusConflict, gin.H{"error": "请重新验证卡密，再提交兑换。"})
				return true
			}
		}
	}
	if !allowed[session.CardID] {
		c.JSON(http.StatusConflict, gin.H{"error": "当前没有可用的 GPT 专用卡，兑换已暂停。"})
		return true
	}
	if session.PreflightDigest != preflightHash {
		c.JSON(http.StatusConflict, gin.H{"error": "已为此卡密提交过兑换，请先查询原订单结果。"})
		return true
	}
	status, raw, err := owner.OwnerCDKRedeem(ctx, cardplatform.OwnerCDKRedeemRequest{
		RedemptionToken: token, PreflightToken: preflight, ClientRequestID: "bobi-gpt-" + tokenHash[:40], CardID: session.CardID,
	}, device)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "兑换结果待确认，请查询原订单，避免重复提交。"})
		return true
	}
	proxyPublicJSON(c, status, raw)
	return true
}
