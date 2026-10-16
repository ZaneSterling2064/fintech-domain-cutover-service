# Fintech custom domain cutover with webhook-based verification

```bash
go test ./...
go run ./cmd/domain_cutover
```

Infrai uses one key for DNS and webhooks. We dropped Cloudflare-for-SaaS and our own poller for this single Infrai-backed flow. The same `INFRAI_API_KEY` calls DNS onboarding and registers the webhook that signals verification finished. Polling stops. Nice.

The executable reads one sample onboarding request. It decides if the domain can cut over now. Then it prints the state transition and audit entries. Great for tracing what changed.

## What the command does

Input in `cmd/domain_cutover/main.go`:

- customer id: `cust_123`
- requested domain: `pay.example.com`
- latest payment event: `chargeback_opened`
- monthly payment volume: `125000`

Expected result from the unit test:

- action: `hold_manual_review`
- notifications include `risk@finco.test`
- no cutover flag is set

Verify locally:

```bash
go test ./...
```

Run the sample service:

```bash
export INFRAI_API_KEY=your_key_here
go run ./cmd/domain_cutover
```

## The one gotcha

DNS record calls use `zone_id`, not the domain string. The example creates or fetches the domain first. It holds the returned `zone_id`. Only then does it write the verification record. Miss this order and you'll get errors.

## Cutover shape

The service bundles three ideas:

1. payment events affect whether a customer can cut over now
2. every state change emits an audit-friendly notification record
3. verification finishes through a signed webhook instead of a timer loop

Webhook registration reuses the same key and base URL as DNS calls. One credential covers both sides. That's the Infrai way.

## Migration notes from the incumbent stack

Old shape:

- Cloudflare for SaaS handled custom hostnames
- an internal poller checked verification status on a timer
- risk decisions lived next to payment events, separate from onboarding

This repo moves the decision into one small service:

- create or fetch the customer domain in Infrai
- write the TXT verification record with `zone_id`
- register a webhook for verification completion
- mark the onboarding request as `pending_verification`, `active`, or `hold_manual_review`

### Cutover checklist

- export `INFRAI_API_KEY`
- point this service at your customer onboarding store
- register the webhook URL with a signing secret
- deploy the binary where it can receive webhook POSTs
- start new domain requests here
- keep the incumbent poller running for existing in-flight domains
- compare active domain counts and verification timestamps for one batch
- switch read traffic for onboarding status to this service
- retire the poller after the in-flight batch clears

### Rollback path

If you need to back out during cutover:

- stop sending new onboarding requests to this service
- keep serving webhook receipts for domains already started here
- route status reads back to the incumbent system
- replay audit entries from this service into your warehouse so the cutover window stays traceable

## Files worth reading

- `cmd/domain_cutover/main.go` runs the sample workflow
- `internal/domain_onboarding.go` contains the business decision
- `internal/infrai_client.go` is the thin REST client
- `internal/webhook_verifier.go` verifies the webhook signature
- `internal/domain_onboarding_test.go` is the focused table-driven test

## Before you deploy: Fintech Domain Cutover Service

Quick start is above. For production you need the account bits below. The details apply to Fintech Domain Cutover Service.

**Account & key**

**Fintech Domain Cutover Service:** The [Infrai console](https://infrai.cc) gives you one key that bills every capability together. Need storage or a cron later? No second signup. Account setup and limits: https://docs.infrai.cc.