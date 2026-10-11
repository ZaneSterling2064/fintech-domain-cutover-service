package internal

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeDomainAPI struct {
	zoneID      string
	webhookID   string
	upsertedReq RecordUpsertRequest
}

func (f *fakeDomainAPI) DNSDomainAdd(ctx context.Context, req DomainAddRequest) (DomainData, error) {
	return DomainData{ZoneID: f.zoneID, Domain: req.Domain}, nil
}

func (f *fakeDomainAPI) DNSDomainGet(ctx context.Context, domain string) (DomainData, error) {
	return DomainData{}, &InfraiError{Message: "not found", StatusCode: 404}
}

func (f *fakeDomainAPI) DNSDomainVerify(ctx context.Context, req DomainKeyRequest) error {
	return nil
}

func (f *fakeDomainAPI) DNSRecordUpsert(ctx context.Context, req RecordUpsertRequest) error {
	f.upsertedReq = req
	return nil
}

func (f *fakeDomainAPI) AccountWebhooksRegister(ctx context.Context, req WebhookRegisterRequest) (WebhookRegisterData, error) {
	return WebhookRegisterData{ID: f.webhookID}, nil
}

func TestDomainOnboardingDecisionTable(t *testing.T) {
	tests := []struct {
		name           string
		input          OnboardingRequest
		expectedAction string
		expectedTarget string
	}{
		{
			name: "chargeback holds cutover",
			input: OnboardingRequest{
				CustomerID:           "cust_123",
				AccountID:            "acct_123",
				Domain:               "pay.example.com",
				VerificationRecord:   "finco-verify=cust_123",
				WebhookSecret:        "secret",
				LatestPaymentEvent:   PaymentEventChargebackOpened,
				MonthlyPaymentVolume: 125000,
			},
			expectedAction: "hold_manual_review",
			expectedTarget: "risk@finco.test",
		},
		{
			name: "healthy account waits for verification",
			input: OnboardingRequest{
				CustomerID:           "cust_234",
				AccountID:            "acct_234",
				Domain:               "pay.example.org",
				VerificationRecord:   "finco-verify=cust_234",
				WebhookSecret:        "secret",
				LatestPaymentEvent:   PaymentEventSettled,
				MonthlyPaymentVolume: 125000,
			},
			expectedAction: "activate_when_verified",
			expectedTarget: "ops@finco.test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeDomainAPI{zoneID: "zone_123", webhookID: "wh_123"}
			svc := NewDomainOnboardingService(fake, "https://onboarding.finco.test/webhooks/domain-verification", tt.input.WebhookSecret)
			svc.now = func() time.Time { return time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC) }

			result, err := svc.Start(context.Background(), tt.input)
			if err != nil {
				t.Fatalf("Start() error = %v", err)
			}
			if result.Action != tt.expectedAction {
				t.Fatalf("Action = %s, want %s", result.Action, tt.expectedAction)
			}
			if result.Notifications[0].Target != tt.expectedTarget {
				t.Fatalf("notification target = %s, want %s", result.Notifications[0].Target, tt.expectedTarget)
			}
			if result.CutoverReady {
				t.Fatalf("CutoverReady = true, want false")
			}
			if fake.upsertedReq.ZoneID != "zone_123" {
				t.Fatalf("ZoneID = %s, want zone_123", fake.upsertedReq.ZoneID)
			}
		})
	}
}

func TestVerificationWebhookHandlerAcceptsSignedPayload(t *testing.T) {
	payload := VerificationWebhook{Domain: "pay.example.com", Status: "verified"}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	mac := hmac.New(sha256.New, []byte("secret"))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(http.MethodPost, "/webhooks/domain-verification", nil)
	req.Body = httptest.NewRecorder().Result().Body
	req.ContentLength = int64(len(body))
	req.Body = nopCloser{Reader: body}
	req.Header.Set("X-Infrai-Signature", sig)

	rr := httptest.NewRecorder()
	VerificationWebhookHandler("secret").ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

type nopCloser struct {
	Reader []byte
	read   bool
}

func (n nopCloser) Read(p []byte) (int, error) {
	if n.read {
		return 0, context.Canceled
	}
	copy(p, n.Reader)
	return len(n.Reader), nil
}

func (n nopCloser) Close() error { return nil }
