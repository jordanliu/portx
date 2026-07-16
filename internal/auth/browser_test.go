package auth

import (
	"encoding/json"
	"encoding/pem"
	"testing"
)

func TestDecodeOriginCert(t *testing.T) {
	t.Parallel()
	payload, _ := json.Marshal(map[string]string{
		"zoneID":    "zone-1",
		"accountID": "acct-1",
		"apiToken":  "tok-secret",
	})
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "ARGO TUNNEL TOKEN", Bytes: payload})
	res, err := decodeOriginCert(pemBytes)
	if err != nil {
		t.Fatal(err)
	}
	if res.APIToken != "tok-secret" || res.AccountID != "acct-1" || res.ZoneID != "zone-1" {
		t.Fatalf("%+v", res)
	}
}
