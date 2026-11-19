package handler

import (
	"errors"
	"os"
	"strings"

	"github.com/tuzi/cdk-recharge-system/internal/cardplatform"
)

func configuredCDKOwner() (*cardplatform.Client, error) {
	origin := strings.TrimSpace(os.Getenv("GPT_REDEEM_API_ORIGIN"))
	if origin != cardplatform.LoadConfig().SiteBase {
		return nil, errors.New("owner and preview API origins differ")
	}
	key, err := os.ReadFile(os.Getenv("GPT_REDEEM_API_KEY_FILE"))
	if err != nil {
		return nil, err
	}
	return cardplatform.NewCDKOwner(origin, string(key))
}

func authenticatedCardPlatformClient() (*cardplatform.Client, error) {
	cfg := cardplatform.LoadConfig()
	if cfg.APIKey != "" {
		return cardplatform.New(cfg), nil
	}
	return configuredCDKOwner()
}
