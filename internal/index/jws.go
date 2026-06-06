package index

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha512"
	"crypto/x509"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
)

// homebrewPubKeyPEM is Homebrew's pinned `homebrew-1` RSA public key — byte for
// byte the key brew bundles at Library/Homebrew/api/homebrew-1.pem. The index
// JWS is signed by Homebrew's offline CI key; verifying against this pinned key
// is what protects us from a tampered index served by a compromised CDN/origin
// (the index drives bottle code-execution, so its integrity is security
// critical). A public key is not a secret; embedding it is correct.
//
//go:embed keys/homebrew-1.pem
var homebrewPubKeyPEM []byte

// homebrewKey is the parsed pinned key, resolved once at package init.
var homebrewKey *rsa.PublicKey

func init() {
	k, err := parseRSAPublicKeyPEM(homebrewPubKeyPEM)
	if err != nil {
		panic("index: embedded homebrew-1.pem is unparseable: " + err.Error())
	}
	homebrewKey = k
}

// ErrUnverified is returned when an index payload fails signature verification.
// Callers MUST refuse to build/install from an unverified payload.
var ErrUnverified = errors.New("index payload signature verification failed")

// jwsEnvelope is Homebrew's general-JWS-serialization shape:
//
//	{"payload":"<raw json string>","signatures":[{"protected","header","signature"}]}
type jwsEnvelope struct {
	Payload    string         `json:"payload"`
	Signatures []jwsSignature `json:"signatures"`
}

type jwsSignature struct {
	Protected string `json:"protected"` // base64url(JSON header)
	Header    struct {
		Kid string `json:"kid"`
	} `json:"header"`
	Signature string `json:"signature"` // base64url(PS512 sig)
}

type jwsProtected struct {
	Alg string `json:"alg"`
	B64 *bool  `json:"b64"` // pointer: absent means true per RFC 7797
}

// VerifyHomebrew verifies a Homebrew API JWS envelope against the pinned key and
// returns the raw payload bytes (the formula/cask JSON array). It mirrors
// Homebrew's api.rb verify_and_parse_jws exactly: PS512, RFC-7797 unencoded
// payload (b64:false), signing input = protected + "." + payload, PSS salt =
// digest length, MGF1 = SHA-512.
func VerifyHomebrew(envelope []byte) ([]byte, error) {
	return verify(envelope, homebrewKey)
}

// verify is VerifyHomebrew with an injectable key — tests sign fixtures with
// their own keypair and pass the matching public key here.
func verify(envelope []byte, pub *rsa.PublicKey) ([]byte, error) {
	var env jwsEnvelope
	if err := json.Unmarshal(envelope, &env); err != nil {
		return nil, fmt.Errorf("jws envelope: %w", err)
	}
	sig := pickSignature(env.Signatures, "homebrew-1")
	if sig == nil {
		return nil, fmt.Errorf("%w: no homebrew-1 signature", ErrUnverified)
	}

	hdrBytes, err := b64urlDecode(sig.Protected)
	if err != nil {
		return nil, fmt.Errorf("%w: protected header: %v", ErrUnverified, err)
	}
	var hdr jwsProtected
	if err := json.Unmarshal(hdrBytes, &hdr); err != nil {
		return nil, fmt.Errorf("%w: protected header json: %v", ErrUnverified, err)
	}
	// alg must be PS512 and b64 must be explicitly false (RFC 7797). nil means
	// "true" per spec — Homebrew always sets it false, so a missing/true b64 is
	// a format we don't accept.
	if hdr.Alg != "PS512" || hdr.B64 == nil || *hdr.B64 {
		return nil, fmt.Errorf("%w: unexpected alg/b64 (%q, %v)", ErrUnverified, hdr.Alg, hdr.B64)
	}

	rawSig, err := b64urlDecode(sig.Signature)
	if err != nil {
		return nil, fmt.Errorf("%w: signature decode: %v", ErrUnverified, err)
	}

	// RFC 7797 detached/unencoded payload: sign over the literal bytes of
	// `protected . payload`, NOT base64url(payload).
	signingInput := sig.Protected + "." + env.Payload
	digest := sha512.Sum512([]byte(signingInput))
	if err := rsa.VerifyPSS(pub, crypto.SHA512, digest[:], rawSig, &rsa.PSSOptions{
		SaltLength: rsa.PSSSaltLengthEqualsHash,
		Hash:       crypto.SHA512,
	}); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnverified, err)
	}
	return []byte(env.Payload), nil
}

func pickSignature(sigs []jwsSignature, kid string) *jwsSignature {
	for i := range sigs {
		if sigs[i].Header.Kid == kid {
			return &sigs[i]
		}
	}
	return nil
}

// b64urlDecode tolerates both padded and unpadded base64url, matching Ruby's
// Base64.urlsafe_decode64 leniency.
func b64urlDecode(s string) ([]byte, error) {
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.URLEncoding.DecodeString(s)
}

func parseRSAPublicKeyPEM(pemBytes []byte) (*rsa.PublicKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("no PEM block")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rk, ok := key.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("not an RSA public key: %T", key)
	}
	return rk, nil
}
