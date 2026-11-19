package cardplatform

import (
	"errors"
	"net/http"
	"strings"
)

func NewCDKOwner(origin, key string) (*Client, error) {
	if origin != "https://zovocard.com" && origin != "https://sandbox.zovocard.com" {
		return nil, errors.New("invalid owner API origin")
	}
	if strings.TrimSpace(key) == "" {
		return nil, errors.New("missing owner API key")
	}
	c := New(Config{SiteBase: origin, APIKey: strings.TrimSpace(key)})
	c.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c, nil
}
