package e2e

import (
	"net"
	"net/http"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

type testListener struct {
	addr  string
	serve func(*http.Server)
}

func newLocalListener(t *testing.T) *testListener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	return &testListener{
		addr: ln.Addr().String(),
		serve: func(srv *http.Server) {
			_ = srv.Serve(ln)
		},
	}
}

func bcryptHash(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	return string(b), err
}
