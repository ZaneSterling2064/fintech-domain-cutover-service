package internal

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

func VerifyWebhookSignature(secret string, body []byte, providedHex string) error {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := mac.Sum(nil)
	provided, err := hex.DecodeString(providedHex)
	if err != nil {
		return err
	}
	if !hmac.Equal(expected, provided) {
		return errors.New("invalid webhook signature")
	}
	return nil
}
