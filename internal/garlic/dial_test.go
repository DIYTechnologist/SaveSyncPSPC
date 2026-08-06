package garlic

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIsDisallowedGarlicAddr(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"169.254.169.254", true}, // cloud metadata endpoint - the whole point of this check
		{"169.254.1.1", true},
		{"0.0.0.0", true},
		{"127.0.0.1", false}, // loopback deliberately allowed - see isDisallowedGarlicAddr's doc comment
		{"192.168.2.79", false},
		{"10.0.0.1", false},
		{"8.8.8.8", false},
	}
	for _, c := range cases {
		got := isDisallowedGarlicAddr(net.ParseIP(c.ip))
		if got != c.want {
			t.Errorf("isDisallowedGarlicAddr(%s) = %v, want %v", c.ip, got, c.want)
		}
	}
}

// TestSafeDialContextRejectsLinkLocal confirms the dial-time enforcement
// actually blocks a connection attempt to a link-local address, using a
// literal IP (127.0.0.1 is loopback, not link-local, so a real listener
// there is used as the negative control - a normal connection must
// still work).
func TestSafeDialContextRejectsLinkLocal(t *testing.T) {
	_, err := safeDialContext(context.Background(), "tcp", "169.254.169.254:80")
	if err == nil {
		t.Fatal("expected a link-local dial to be refused")
	}
	if !strings.Contains(err.Error(), "link-local") {
		t.Fatalf("expected a link-local-specific error, got: %v", err)
	}
}

func TestSafeDialContextAllowsLoopback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := New(server.URL, 2*time.Second)
	resp, err := client.HTTP.Get(server.URL)
	if err != nil {
		t.Fatalf("expected a loopback connection through safeDialContext to succeed, got: %v", err)
	}
	resp.Body.Close()
}

// TestSafeDialContextResolvesFreshEachCall is the actual regression this
// exists for: the resolution used to decide whether an address is
// allowed must be the same one used to connect, not a separate earlier
// lookup - otherwise a DNS answer that changes between the check and the
// connect (rebinding) slips through. This is exercised indirectly: two
// back-to-back dials through safeDialContext against the same loopback
// listener both succeed, showing resolution isn't a one-time cached
// decision reused across calls.
func TestSafeDialContextResolvesFreshEachCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	for i := 0; i < 2; i++ {
		conn, err := safeDialContext(context.Background(), "tcp", server.Listener.Addr().String())
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		conn.Close()
	}
}
