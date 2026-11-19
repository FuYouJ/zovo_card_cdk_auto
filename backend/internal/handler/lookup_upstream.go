package handler

import (
	"context"
	"errors"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/tuzi/cdk-recharge-system/internal/cardplatform"
	"github.com/tuzi/cdk-recharge-system/internal/db"
)

const lookupCacheTTL = 5 * time.Minute

var lookupCodePattern = regexp.MustCompile(`^[A-Z0-9]+(?:-[A-Z0-9]+)+$`)
var lookupUpstreamSlots = make(chan struct{}, lookupBatchWorkers)

func resolveUpstreamCDKStatus(ctx context.Context, code string, resp *cdkLookupResult) bool {
	code = strings.ToUpper(strings.TrimSpace(code))
	separator := strings.LastIndexByte(code, '-')
	if len(code) > 128 || separator < 8 || len(code)-separator <= 4 || !lookupCodePattern.MatchString(code) {
		return false
	}
	// Only send a search prefix. The returned complete code must match exactly.
	prefix := code[:min(separator, 14)]
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	select {
	case lookupUpstreamSlots <- struct{}{}:
		defer func() { <-lookupUpstreamSlots }()
	case <-ctx.Done():
		resp.Status, resp.Message = "unavailable", lookupUnavailableMessage
		return false
	}

	checkedAt := time.Now()
	snapshot, err := fetchUpstreamCDKStatus(ctx, code, prefix)
	if err != nil || (snapshot != nil && !applyUpstreamCDKStatus(resp, *snapshot)) {
		// Provider errors can contain request or account data.
		log.Print("[lookup-cdk] upstream status lookup failed")
		resp.Status, resp.Message = "unavailable", lookupUnavailableMessage
		return false
	}
	if snapshot == nil {
		return false
	}
	if err := db.SaveCachedCDKStatus(ctx, code, *snapshot, checkedAt); err != nil {
		log.Print("[lookup-cdk] status cache write failed")
	}
	return true
}

func fetchUpstreamCDKStatus(ctx context.Context, code, prefix string) (*db.CDKStatusSnapshot, error) {
	client, err := authenticatedCardPlatformClient()
	if err != nil {
		return nil, err
	}
	const pageSize, maxPages = 20, 3
	for page := 1; page <= maxPages; page++ {
		result, err := client.ListCDKsQuery(ctx, cardplatform.CDKListQuery{
			Page: page, PageSize: pageSize, Query: prefix,
		})
		if err != nil {
			return nil, err
		}
		for _, item := range result.List {
			if item.ID > 0 && (strings.EqualFold(strings.TrimSpace(item.Code), code) ||
				strings.EqualFold(strings.TrimSpace(item.FullCode), code)) {
				return &db.CDKStatusSnapshot{Status: item.Status, Plan: item.Plan}, nil
			}
		}
		if result.List == nil || (len(result.List) == 0 && result.Total > (page-1)*pageSize) {
			break
		}
		if result.Total <= (page-1)*pageSize+len(result.List) {
			return nil, nil
		}
	}
	return nil, errors.New("incomplete CDK status search")
}

func applyUpstreamCDKStatus(resp *cdkLookupResult, snapshot db.CDKStatusSnapshot) bool {
	switch strings.ToLower(strings.TrimSpace(snapshot.Status)) {
	case "unused", "active":
		resp.Status, resp.Message = "unused", "卡密未使用"
	case "consumed", "redeemed", "used":
		resp.Status, resp.Message = "used", "卡密使用成功"
	case "reserved", "processing":
		resp.Status, resp.Message = "processing", "卡密兑换处理中"
	case "review":
		resp.Status, resp.Message = "processing", "卡密待核验"
	case "disabled":
		resp.Status, resp.Message = "disabled", "卡密已禁用"
	case "frozen":
		resp.Status, resp.Message = "disabled", "卡密暂时冻结"
	case "expired":
		resp.Status, resp.Message = "expired", "卡密已过期"
	default:
		return false
	}
	resp.Plan = snapshot.Plan
	return true
}
