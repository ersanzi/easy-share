package knowledge

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestIndependentSessionSSOProbeHonorsLaterLogoutOrLogin(t *testing.T) {
	for _, change := range []string{"logout", "login"} {
		t.Run(change, func(t *testing.T) {
			store := NewStore(filepath.Join(t.TempDir(), "session.json"))
			service := NewService(store)
			if err := service.SignIn(Session{Token: "local", Mode: ModePassword}); err != nil {
				t.Fatal(err)
			}
			revision, err := service.BeginSSO()
			if err != nil {
				t.Fatal(err)
			}
			if service.Current().Token != "local" || NewService(store).Current().Token != "local" {
				t.Fatal("自动探测破坏独立会话")
			}
			expected := ""
			if change == "logout" {
				err = service.SignOut()
			} else {
				expected = "new-local"
				err = service.SignIn(Session{Token: expected, Mode: ModePassword})
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := service.CompleteSignIn(revision, Session{Token: "stale-sso", Mode: ModeSSO}); !errors.Is(err, ErrSessionChanged) {
				t.Fatalf("迟到探测未被拒绝: %v", err)
			}
			if service.Current().Token != expected || NewService(store).Current().Token != expected {
				t.Fatal("迟到探测恢复或覆盖了当前身份")
			}
		})
	}
}
