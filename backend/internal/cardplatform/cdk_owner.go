package cardplatform

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func (c *Client) ActiveCardsWithRemarkPrefix(ctx context.Context, prefix string) (map[int64]bool, error) {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return nil, errors.New("owner card remark prefix is empty")
	}
	lowerPrefix := strings.ToLower(prefix)
	const pageSize = 100
	cards := make(map[int64]bool)
	seen := make(map[int64]bool)
	for page := 1; page <= 100; page++ {
		query := url.Values{
			"status": {"ACTIVE"}, "archived": {"0"}, "remark": {prefix},
			"page": {strconv.Itoa(page)}, "page_size": {strconv.Itoa(pageSize)},
		}
		raw, err := c.doOpenAPI(ctx, http.MethodGet, "/cards?"+query.Encode(), nil, "")
		if err != nil {
			return nil, err
		}
		var data struct {
			Total *int `json:"total"`
			List  []struct {
				ID     int64  `json:"id"`
				Remark string `json:"remark"`
				Status string `json:"status"`
			} `json:"list"`
		}
		if json.Unmarshal(raw, &data) != nil || data.Total == nil || *data.Total < 0 {
			return nil, errors.New("invalid owner card inventory")
		}
		for _, item := range data.List {
			if item.ID <= 0 || seen[item.ID] {
				return nil, errors.New("inconsistent owner card inventory")
			}
			seen[item.ID] = true
			if item.Status == "ACTIVE" && strings.HasPrefix(strings.ToLower(strings.TrimSpace(item.Remark)), lowerPrefix) {
				cards[item.ID] = true
			}
		}
		if len(seen) == *data.Total {
			return cards, nil
		}
		if len(data.List) == 0 || len(seen) > *data.Total {
			break
		}
	}
	return nil, errors.New("owner card inventory incomplete")
}

func (c *Client) PickExistingGPTCard(ctx context.Context, plan string, allowed map[int64]bool) (int64, error) {
	query := url.Values{"product": {"gpt"}, "plan": {plan}, "card_mode": {"auto_existing"}}
	raw, err := c.doOpenAPI(ctx, http.MethodGet, "/gpt-direct/card-pool/schedule?"+query.Encode(), nil, "")
	if err != nil {
		return 0, err
	}
	var data struct {
		Candidates []struct {
			ID   int64 `json:"card_id"`
			Rank int   `json:"rank"`
			Skip *bool `json:"skip"`
		} `json:"candidates"`
	}
	if json.Unmarshal(raw, &data) != nil || data.Candidates == nil {
		return 0, errors.New("invalid owner card schedule")
	}
	var id int64
	var rank int
	for _, card := range data.Candidates {
		if allowed[card.ID] && card.Skip != nil && !*card.Skip && card.Rank > 0 && (id == 0 || card.Rank < rank) {
			id, rank = card.ID, card.Rank
		}
	}
	return id, nil
}

type OwnerCDKRedeemRequest struct {
	RedemptionToken string `json:"redemption_token"`
	PreflightToken  string `json:"preflight_token"`
	ClientRequestID string `json:"client_request_id"`
	CardID          int64  `json:"card_id"`
}

func (c *Client) OwnerCDKRedeem(ctx context.Context, body OwnerCDKRedeemRequest, device string) (int, json.RawMessage, error) {
	if body.CardID <= 0 || c.cfg.APIKey == "" {
		return 0, nil, errors.New("owner redemption needs an explicit card")
	}
	return c.doCDK(ctx, http.MethodPost, c.cfg.OpenAPIBase()+"/gpt-direct/cdks/redeem", body, device, c.cfg.APIKey)
}
