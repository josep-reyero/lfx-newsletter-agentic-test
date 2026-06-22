// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package handler

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/MicahParks/jwkset"
	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

const testKeyID = "heimdall-test-key"

func TestAuthValidatorValidateAcceptsPS256Token(t *testing.T) {
	key := testRSAKey(t)
	validator := testAuthValidator(t, key, "newsletter-api")

	token := signToken(t, jwt.SigningMethodPS256, key, jwt.MapClaims{
		"aud":       "newsletter-api",
		"principal": "user-123",
		"exp":       time.Now().Add(time.Hour).Unix(),
	})

	user, err := validator.validate(token)
	if err != nil {
		t.Fatalf("validate PS256 token: %v", err)
	}
	if user != "user-123" {
		t.Fatalf("validate PS256 token user = %q, want user-123", user)
	}
}

func TestAuthValidatorValidateRejectsWrongAudience(t *testing.T) {
	key := testRSAKey(t)
	validator := testAuthValidator(t, key, "newsletter-api")

	token := signToken(t, jwt.SigningMethodPS256, key, jwt.MapClaims{
		"aud":       "other-service",
		"principal": "user-123",
		"exp":       time.Now().Add(time.Hour).Unix(),
	})

	_, err := validator.validate(token)
	if err == nil {
		t.Fatal("validate token with wrong audience: got nil error")
	}
	if err.Error() != "token audience mismatch" {
		t.Fatalf("validate token with wrong audience error = %v, want audience mismatch", err)
	}
}

func TestAuthValidatorValidateRejectsUnsupportedSigningMethod(t *testing.T) {
	key := testRSAKey(t)
	validator := testAuthValidator(t, key, "newsletter-api")

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"aud":       "newsletter-api",
		"principal": "user-123",
		"exp":       time.Now().Add(time.Hour).Unix(),
	})
	token.Header[jwkset.HeaderKID] = testKeyID
	signed, err := token.SignedString([]byte("not-a-valid-heimdall-key"))
	if err != nil {
		t.Fatalf("sign HS256 token: %v", err)
	}

	if _, err := validator.validate(signed); err == nil {
		t.Fatal("validate HS256 token: got nil error")
	}
}

func testRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return key
}

func testAuthValidator(t *testing.T, key *rsa.PrivateKey, audience string) *AuthValidator {
	t.Helper()

	jwk, err := jwkset.NewJWKFromKey(&key.PublicKey, jwkset.JWKOptions{
		Metadata: jwkset.JWKMetadataOptions{
			KID: testKeyID,
			ALG: jwkset.AlgPS256,
			USE: jwkset.UseSig,
		},
	})
	if err != nil {
		t.Fatalf("create JWK: %v", err)
	}
	storage := jwkset.NewMemoryStorage()
	if err := storage.KeyWrite(context.Background(), jwk); err != nil {
		t.Fatalf("write JWK: %v", err)
	}
	jwks, err := keyfunc.New(keyfunc.Options{
		Ctx:          context.Background(),
		Storage:      storage,
		UseWhitelist: []jwkset.USE{jwkset.UseSig},
	})
	if err != nil {
		t.Fatalf("create keyfunc: %v", err)
	}
	return &AuthValidator{
		jwks:             jwks,
		expectedAudience: audience,
	}
}

func signToken(t *testing.T, method jwt.SigningMethod, key *rsa.PrivateKey, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(method, claims)
	token.Header[jwkset.HeaderKID] = testKeyID
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}
