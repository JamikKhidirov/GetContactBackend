package platform_test

import (
	"testing"
	"time"

	"github.com/getcontact/backend/internal/domain"
	"github.com/getcontact/backend/internal/platform"
	"github.com/google/uuid"
)

func TestTokenIssueAndParse(t *testing.T) {
	secret := "0123456789abcdef0123456789abcdef"
	tm, err := platform.NewTokenManager(secret, "test-issuer", time.Minute)
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	uid := uuid.New()
	token, jti, exp, err := tm.Issue(uid, domain.RoleAdmin)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if jti == "" || exp.Before(time.Now()) {
		t.Fatal("invalid issue metadata")
	}
	claims, err := tm.Parse(token)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if claims.UserID() != uid || claims.Role != string(domain.RoleAdmin) || claims.ID != jti {
		t.Fatalf("claims mismatch: %+v", claims)
	}
	if claims.SessionID != "" {
		t.Fatalf("legacy Issue must not bind a session, got %q", claims.SessionID)
	}

	sessionID := uuid.New()
	bound, _, _, err := tm.IssueWithSession(uid, domain.RoleUser, sessionID)
	if err != nil {
		t.Fatalf("issue with session: %v", err)
	}
	boundClaims, err := tm.Parse(bound)
	if err != nil {
		t.Fatalf("parse session token: %v", err)
	}
	if boundClaims.SessionID != sessionID.String() {
		t.Fatalf("session id %q, want %q", boundClaims.SessionID, sessionID.String())
	}

	versioned, _, _, err := tm.IssueWithSessionVersion(uid, domain.RoleUser, sessionID, 7)
	if err != nil {
		t.Fatalf("issue with version: %v", err)
	}
	versionedClaims, err := tm.Parse(versioned)
	if err != nil {
		t.Fatalf("parse versioned token: %v", err)
	}
	if versionedClaims.TokenVersion != 7 {
		t.Fatalf("token version %d, want 7", versionedClaims.TokenVersion)
	}

	other, _ := platform.NewTokenManager("ffffffffffffffffffffffffffffffff", "test-issuer", time.Minute)
	if _, err := other.Parse(token); err == nil {
		t.Fatal("token accepted with a different secret")
	}

	expired, _ := platform.NewTokenManager(secret, "test-issuer", -time.Minute)
	expiredToken, _, _, _ := expired.Issue(uid, domain.RoleUser)
	if _, err := tm.Parse(expiredToken); err == nil {
		t.Fatal("expired token accepted")
	}

	if _, err := tm.Parse(token + "x"); err == nil {
		t.Fatal("tampered token accepted")
	}
}
