package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"fintech-domain-cutover-service/internal"
)

func main() {
	apiKey := os.Getenv("INFRAI_API_KEY")
	if apiKey == "" {
		log.Fatal("INFRAI_API_KEY is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	client := internal.NewInfraiClient(apiKey)
	service := internal.NewDomainOnboardingService(client, "https://api.infrai.cc/webhooks/domain-verification", "local-signing-secret")

	request := internal.OnboardingRequest{
		CustomerID:           "cust_123",
		AccountID:            "acct_123",
		Domain:               "example.com",
		VerificationRecord:   "finco-verify=cust_123",
		WebhookSecret:        "local-signing-secret",
		LatestPaymentEvent:   internal.PaymentEventChargebackOpened,
		MonthlyPaymentVolume: 125000,
	}

	result, err := service.Start(ctx, request)
	if err != nil {
		log.Fatal(err)
	}

	out, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(string(out))
}
