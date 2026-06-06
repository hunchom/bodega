package index

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// signEnvelope builds a Homebrew-style JWS envelope over payload, signed with
// priv exactly the way VerifyHomebrew expects (PS512, b64:false, signing input
// = protected + "." + payload).
func signEnvelope(t *testing.T, priv *rsa.PrivateKey, kid, alg string, b64 bool, payload string) []byte {
	t.Helper()
	hdr := map[string]any{"alg": alg, "b64": b64, "crit": []string{"b64"}}
	hb, _ := json.Marshal(hdr)
	protected := base64.RawURLEncoding.EncodeToString(hb)
	digest := sha512.Sum512([]byte(protected + "." + payload))
	sig, err := rsa.SignPSS(rand.Reader, priv, crypto.SHA512, digest[:], &rsa.PSSOptions{
		SaltLength: rsa.PSSSaltLengthEqualsHash, Hash: crypto.SHA512,
	})
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]any{
		"payload": payload,
		"signatures": []map[string]any{{
			"protected": protected,
			"header":    map[string]any{"kid": kid},
			"signature": base64.RawURLEncoding.EncodeToString(sig),
		}},
	}
	b, _ := json.Marshal(env)
	return b
}

func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestVerifyRoundtrip(t *testing.T) {
	priv := testKey(t)
	payload := `[{"name":"ripgrep"}]`
	env := signEnvelope(t, priv, "homebrew-1", "PS512", false, payload)
	got, err := verify(env, &priv.PublicKey)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if string(got) != payload {
		t.Fatalf("payload = %q", got)
	}
}

func TestVerifyRejectsTamperedPayload(t *testing.T) {
	priv := testKey(t)
	env := signEnvelope(t, priv, "homebrew-1", "PS512", false, `[{"name":"ripgrep"}]`)
	// Flip the payload after signing — the envelope's payload no longer matches
	// the signature.
	tampered := strings.Replace(string(env), "ripgrep", "evilpkg", 1)
	_, err := verify([]byte(tampered), &priv.PublicKey)
	if !errors.Is(err, ErrUnverified) {
		t.Fatalf("tampered payload accepted, err=%v", err)
	}
}

func TestVerifyRejectsWrongKey(t *testing.T) {
	priv := testKey(t)
	other := testKey(t)
	env := signEnvelope(t, priv, "homebrew-1", "PS512", false, `[]`)
	if _, err := verify(env, &other.PublicKey); !errors.Is(err, ErrUnverified) {
		t.Fatalf("wrong key accepted, err=%v", err)
	}
}

func TestVerifyRejectsMissingKid(t *testing.T) {
	priv := testKey(t)
	env := signEnvelope(t, priv, "attacker-1", "PS512", false, `[]`)
	if _, err := verify(env, &priv.PublicKey); !errors.Is(err, ErrUnverified) {
		t.Fatalf("wrong kid accepted, err=%v", err)
	}
}

func TestVerifyRejectsWrongAlg(t *testing.T) {
	priv := testKey(t)
	env := signEnvelope(t, priv, "homebrew-1", "RS256", false, `[]`)
	if _, err := verify(env, &priv.PublicKey); !errors.Is(err, ErrUnverified) {
		t.Fatalf("wrong alg accepted, err=%v", err)
	}
}

func TestVerifyRejectsEncodedPayloadFlag(t *testing.T) {
	priv := testKey(t)
	// b64:true is a JWS shape we don't accept (Homebrew always uses b64:false).
	env := signEnvelope(t, priv, "homebrew-1", "PS512", true, `[]`)
	if _, err := verify(env, &priv.PublicKey); !errors.Is(err, ErrUnverified) {
		t.Fatalf("b64:true accepted, err=%v", err)
	}
}

func TestVerifyRejectsGarbage(t *testing.T) {
	if _, err := verify([]byte("not json"), &testKey(t).PublicKey); err == nil {
		t.Fatal("garbage envelope accepted")
	}
}

// The embedded Homebrew key must parse at init (init panics otherwise); assert
// it loaded as a sane RSA key.
func TestPinnedKeyLoaded(t *testing.T) {
	if homebrewKey == nil || homebrewKey.N == nil {
		t.Fatal("pinned homebrew key not loaded")
	}
	if sz := homebrewKey.Size(); sz < 256 { // >= 2048-bit
		t.Fatalf("pinned key too small: %d bytes", sz)
	}
}
