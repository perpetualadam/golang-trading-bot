package exchange

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// HTTPDoer is satisfied by *http.Client.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

func defaultClient() *http.Client {
	return &http.Client{Timeout: 25 * time.Second}
}

func jsonGet(ctx context.Context, c HTTPDoer, u string, headers map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("http %d: %s", resp.StatusCode, truncate(string(b), 512))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(b, out)
}

func jsonPost(ctx context.Context, c HTTPDoer, u string, headers map[string]string, body string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(body))
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if body != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("http %d: %s", resp.StatusCode, truncate(string(b), 512))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(b, out)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func hmacSHA256Hex(secret, payload string) string {
	m := hmac.New(sha256.New, []byte(secret))
	_, _ = m.Write([]byte(payload))
	return hex.EncodeToString(m.Sum(nil))
}

func hmacSHA256B64(secret, payload string) string {
	m := hmac.New(sha256.New, []byte(secret))
	_, _ = m.Write([]byte(payload))
	return base64.StdEncoding.EncodeToString(m.Sum(nil))
}

// KrakenSign: signature = HMAC-SHA512( path + SHA256(nonce + postData) , base64_decode(secret) )
func KrakenSign(urlpath, nonce, postData string, apiSecretB64 string) (string, error) {
	h := sha256.Sum256([]byte(nonce + postData))
	msg := append([]byte(urlpath), h[:]...)
	key, err := base64.StdEncoding.DecodeString(apiSecretB64)
	if err != nil {
		return "", fmt.Errorf("kraken secret base64: %w", err)
	}
	mac := hmac.New(sha512.New, key)
	_, _ = mac.Write(msg)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil)), nil
}

func mustParseFloat(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func fmtQty(q float64) string {
	return strconv.FormatFloat(q, 'f', -1, 64)
}
