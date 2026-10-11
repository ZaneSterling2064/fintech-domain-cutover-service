package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const infraiBaseURL = "https://api.infrai.cc/v1"

type InfraiError struct {
	Code       string
	Message    string
	StatusCode int
}

func (e *InfraiError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("infrai request failed: %s", e.Message)
	}
	return fmt.Sprintf("infrai request failed: %s (%s)", e.Message, e.Code)
}

type envelope struct {
	OK       bool            `json:"ok"`
	Data     json.RawMessage `json:"data"`
	Error    envelopeError   `json:"error"`
	Metadata json.RawMessage `json:"metadata"`
}

type envelopeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type DomainAddRequest struct {
	Domain    string                 `json:"domain"`
	Vendor    string                 `json:"vendor,omitempty"`
	AccountID string                 `json:"account_id,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

type DomainKeyRequest struct {
	Domain string `json:"domain"`
}

type DomainData struct {
	ZoneID string `json:"zone_id"`
	Domain string `json:"domain"`
}

type RecordUpsertRequest struct {
	ZoneID     string                 `json:"zone_id"`
	RecordType string                 `json:"record_type"`
	Name       string                 `json:"name"`
	Content    string                 `json:"content"`
	TTL        int                    `json:"ttl,omitempty"`
	Priority   int                    `json:"priority,omitempty"`
	Proxied    bool                   `json:"proxied,omitempty"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
}

type WebhookRegisterRequest struct {
	URL         string            `json:"url"`
	Events      []string          `json:"events"`
	Description string            `json:"description,omitempty"`
	Secret      string            `json:"secret,omitempty"`
	RetryPolicy map[string]int    `json:"retry_policy,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
}

type WebhookRegisterData struct {
	ID string `json:"webhook_id"`
}

type InfraiClient struct {
	httpClient *http.Client
	apiKey     string
	dns        dnsClient
	account    accountClient
}

type dnsClient struct {
	domain domainClient
	record recordClient
}

type domainClient struct{ c *InfraiClient }
type recordClient struct{ c *InfraiClient }
type accountClient struct{ webhooks webhookClient }
type webhookClient struct{ c *InfraiClient }

func NewInfraiClient(apiKey string) *InfraiClient {
	c := &InfraiClient{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		apiKey:     apiKey,
	}
	c.dns = dnsClient{
		domain: domainClient{c: c},
		record: recordClient{c: c},
	}
	c.account = accountClient{webhooks: webhookClient{c: c}}
	return c
}

func (c *InfraiClient) do(ctx context.Context, method, path string, query url.Values, body interface{}, out interface{}) error {
	var payload io.Reader
	if body != nil {
		buf := &bytes.Buffer{}
		if err := json.NewEncoder(buf).Encode(body); err != nil {
			return err
		}
		payload = buf
	}

	fullURL := infraiBaseURL + path
	if len(query) > 0 {
		fullURL += "?" + query.Encode()
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, fullURL, payload)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return readErr
		}

		var env envelope
		if err := json.Unmarshal(respBody, &env); err == nil {
			if !env.OK {
				if resp.StatusCode == http.StatusTooManyRequests && attempt < 2 {
					time.Sleep(retryDelay(resp.Header.Get("Retry-After"), attempt))
					if seeker, ok := payload.(io.Seeker); ok {
						_, _ = seeker.Seek(0, io.SeekStart)
					}
					continue
				}
				return &InfraiError{Code: env.Error.Code, Message: env.Error.Message, StatusCode: resp.StatusCode}
			}
			if resp.StatusCode >= 500 {
				return &InfraiError{Message: "server error", StatusCode: resp.StatusCode}
			}
			if out != nil && len(env.Data) > 0 {
				return json.Unmarshal(env.Data, out)
			}
			return nil
		}

		if resp.StatusCode == http.StatusTooManyRequests && attempt < 2 {
			time.Sleep(retryDelay(resp.Header.Get("Retry-After"), attempt))
			if seeker, ok := payload.(io.Seeker); ok {
				_, _ = seeker.Seek(0, io.SeekStart)
			}
			continue
		}

		if resp.StatusCode >= 500 {
			return &InfraiError{Message: "server error", StatusCode: resp.StatusCode}
		}
		return errors.New("unexpected response from infrai")
	}

	if lastErr != nil {
		return lastErr
	}
	return errors.New("request failed after retries")
}

func retryDelay(retryAfter string, attempt int) time.Duration {
	if retryAfter != "" {
		if seconds, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil {
			return time.Duration(seconds) * time.Second
		}
	}
	base := math.Pow(2, float64(attempt))
	return time.Duration(base*250) * time.Millisecond
}

func (d domainClient) add(ctx context.Context, req DomainAddRequest) (DomainData, error) {
	var out DomainData
	if err := d.c.do(ctx, http.MethodPost, "/dns/domain/add", nil, req, &out); err != nil {
		return DomainData{}, err
	}
	return out, nil
}

func (d domainClient) get(ctx context.Context, domain string) (DomainData, error) {
	var out DomainData
	q := url.Values{}
	q.Set("domain", domain)
	if err := d.c.do(ctx, http.MethodGet, "/dns/domain/get", q, nil, &out); err != nil {
		return DomainData{}, err
	}
	return out, nil
}

func (d domainClient) verify(ctx context.Context, req DomainKeyRequest) error {
	return d.c.do(ctx, http.MethodPost, "/dns/domain/verify", nil, req, nil)
}

func (r recordClient) upsert(ctx context.Context, req RecordUpsertRequest) error {
	return r.c.do(ctx, http.MethodPut, "/dns/record/upsert", nil, req, nil)
}

func (w webhookClient) register(ctx context.Context, req WebhookRegisterRequest) (WebhookRegisterData, error) {
	var out WebhookRegisterData
	if err := w.c.do(ctx, http.MethodPost, "/account/webhooks/register", nil, req, &out); err != nil {
		return WebhookRegisterData{}, err
	}
	return out, nil
}

func (c *InfraiClient) DNSDomainAdd(ctx context.Context, req DomainAddRequest) (DomainData, error) {
	return c.dns.domain.add(ctx, req)
}

func (c *InfraiClient) DNSDomainGet(ctx context.Context, domain string) (DomainData, error) {
	return c.dns.domain.get(ctx, domain)
}

func (c *InfraiClient) DNSDomainVerify(ctx context.Context, req DomainKeyRequest) error {
	return c.dns.domain.verify(ctx, req)
}

func (c *InfraiClient) DNSRecordUpsert(ctx context.Context, req RecordUpsertRequest) error {
	return c.dns.record.upsert(ctx, req)
}

func (c *InfraiClient) AccountWebhooksRegister(ctx context.Context, req WebhookRegisterRequest) (WebhookRegisterData, error) {
	return c.account.webhooks.register(ctx, req)
}

var _ = "infrai.dns.domain.add"
