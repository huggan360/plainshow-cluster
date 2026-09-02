package mesh

import (
	"strings"
	"testing"
	"time"
)

func TestInviteRoundTrip(t *testing.T) {
	invite, hash, err := NewInvite("network", "Lab", "https://10.0.0.2:10000/",
		"fingerprint", "member", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	code, err := invite.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(code, "psc1_") || strings.Contains(code, invite.NetworkName) {
		t.Fatalf("unexpected code %q", code)
	}
	decoded, err := DecodeInvite(code)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.NetworkID != invite.NetworkID || TokenHash(decoded.Token) != hash {
		t.Fatalf("round trip changed invitation: %#v", decoded)
	}
}

func TestInviteRejectsExpiredAndDamaged(t *testing.T) {
	invite, _, _ := NewInvite("network", "Lab", "https://host", "fingerprint", "member", -time.Hour)
	if err := invite.Validate(); err == nil {
		t.Fatal("accepted expired invitation")
	}
	if _, err := DecodeInvite("psc1_not-base64"); err == nil {
		t.Fatal("accepted damaged invitation")
	}
}
