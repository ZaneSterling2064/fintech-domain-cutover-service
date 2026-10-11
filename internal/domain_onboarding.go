package internal

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type PaymentEvent string

const (
	PaymentEventSettled          PaymentEvent = "settled"
	PaymentEventChargebackOpened PaymentEvent = "chargeback_opened"
	PaymentEventPayoutPaused     PaymentEvent = "payout_paused"
)

type OnboardingRequest struct {
	CustomerID           string
	AccountID            string
	Domain               string
	VerificationRecord   string
	WebhookSecret        string
	LatestPaymentEvent   PaymentEvent
	MonthlyPaymentVolume int64
}

type AuditNotification struct {
	At      time.Time `json:"at"`
	Channel string    `json:"channel"`
	Target  string    `json:"target"`
	Message string    `json:"message"`
}

type OnboardingResult struct {
	CustomerID         string              `json:"customer_id"`
	Domain             string              `json:"domain"`
	ZoneID             string              `json:"zone_id"`
	Action             string              `json:"action"`
	Status             string              `json:"status"`
	CutoverReady       bool                `json:"cutover_ready"`
	WebhookID          string              `json:"webhook_id"`
	Notifications      []AuditNotification `json:"notifications"`
	VerificationRecord string              `json:"verification_record"`
}

type DomainAPI interface {
	DNSDomainAdd(ctx context.Context, req DomainAddRequest) (DomainData, error)
	DNSDomainGet(ctx context.Context, domain string) (DomainData, error)
	DNSDomainVerify(ctx context.Context, req DomainKeyRequest) error
	DNSRecordUpsert(ctx context.Context, req RecordUpsertRequest) error
	AccountWebhooksRegister(ctx context.Context, req WebhookRegisterRequest) (WebhookRegisterData, error)
}

type DomainOnboardingService struct {
	api        DomainAPI
	webhookURL string
	secret     string
	now        func() time.Time
}

func NewDomainOnboardingService(api DomainAPI, webhookURL, secret string) *DomainOnboardingService {
	return &DomainOnboardingService{
		api:        api,
		webhookURL: webhookURL,
		secret:     secret,
		now:        time.Now,
	}
}

func (s *DomainOnboardingService) Start(ctx context.Context, req OnboardingRequest) (OnboardingResult, error) {
	decision := decideCutover(req)

	domainData, err := s.ensureDomain(ctx, req)
	if err != nil {
		return OnboardingResult{}, err
	}

	if err := s.api.DNSRecordUpsert(ctx, RecordUpsertRequest{
		ZoneID:     domainData.ZoneID,
		RecordType: "TXT",
		Name:       domainData.Domain,
		Content:    req.VerificationRecord,
		TTL:        300,
		Metadata: map[string]interface{}{
			"customer_id": req.CustomerID,
			"purpose":     "domain_verification",
		},
	}); err != nil {
		return OnboardingResult{}, err
	}

	if err := s.api.DNSDomainVerify(ctx, DomainKeyRequest{Domain: req.Domain}); err != nil {
		return OnboardingResult{}, err
	}

	wh, err := s.api.AccountWebhooksRegister(ctx, WebhookRegisterRequest{
		URL:         s.webhookURL,
		Events:      []string{"*"},
		Description: "custom domain verification completion",
		Secret:      req.WebhookSecret,
		Headers: map[string]string{
			"X-Service": "domain-cutover",
		},
	})
	if err != nil {
		return OnboardingResult{}, err
	}

	result := OnboardingResult{
		CustomerID:         req.CustomerID,
		Domain:             req.Domain,
		ZoneID:             domainData.ZoneID,
		Action:             decision.Action,
		Status:             decision.Status,
		CutoverReady:       decision.CutoverReady,
		WebhookID:          wh.ID,
		VerificationRecord: req.VerificationRecord,
		Notifications: []AuditNotification{
			{
				At:      s.now().UTC(),
				Channel: "email",
				Target:  decision.Notify,
				Message: fmt.Sprintf("customer=%s domain=%s action=%s status=%s", req.CustomerID, req.Domain, decision.Action, decision.Status),
			},
		},
	}
	return result, nil
}

func (s *DomainOnboardingService) ensureDomain(ctx context.Context, req OnboardingRequest) (DomainData, error) {
	got, err := s.api.DNSDomainGet(ctx, req.Domain)
	if err == nil && got.ZoneID != "" {
		return got, nil
	}
	return s.api.DNSDomainAdd(ctx, DomainAddRequest{
		Domain:    req.Domain,
		Vendor:    "fintech-platform",
		AccountID: req.AccountID,
		Metadata: map[string]interface{}{
			"customer_id": req.CustomerID,
		},
	})
}

type cutoverDecision struct {
	Action       string
	Status       string
	CutoverReady bool
	Notify       string
}

func decideCutover(req OnboardingRequest) cutoverDecision {
	if req.LatestPaymentEvent == PaymentEventChargebackOpened || req.LatestPaymentEvent == PaymentEventPayoutPaused {
		return cutoverDecision{
			Action:       "hold_manual_review",
			Status:       "risk_hold",
			CutoverReady: false,
			Notify:       "risk@finco.test",
		}
	}
	if req.MonthlyPaymentVolume >= 100000 && strings.HasPrefix(req.Domain, "pay.") {
		return cutoverDecision{
			Action:       "activate_when_verified",
			Status:       "pending_verification",
			CutoverReady: false,
			Notify:       "ops@finco.test",
		}
	}
	return cutoverDecision{
		Action:       "activate_when_verified",
		Status:       "pending_verification",
		CutoverReady: false,
		Notify:       "onboarding@finco.test",
	}
}
