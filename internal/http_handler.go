package internal

import (
	"encoding/json"
	"net/http"
)

type VerificationWebhook struct {
	Domain string `json:"domain"`
	Status string `json:"status"`
}

func VerificationWebhookHandler(secret string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)

		signature := r.Header.Get("X-Infrai-Signature")
		if err := VerifyWebhookSignature(secret, body, signature); err != nil {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}

		var payload VerificationWebhook
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(w, "bad payload", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"domain": payload.Domain,
			"status": payload.Status,
		})
	})
}
