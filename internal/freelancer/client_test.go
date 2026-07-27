package freelancer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/igun997/freelancer-mcp/internal/session"
)

func testClient(t *testing.T, handler http.Handler) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	t.Setenv("FREELANCER_SESSION_DIR", t.TempDir())
	store, err := session.NewStore("test")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	cfg := DefaultConfig()
	cfg.APIBase = server.URL + "/api"
	cfg.WebBase = server.URL
	client, err := New(cfg, store)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return client, server
}

func TestLoginStoresSessionAndSendsAuthHeader(t *testing.T) {
	var loginForm url.Values
	var authSeen string

	mux := http.NewServeMux()
	mux.HandleFunc("/auth/device", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(AuthHeader); got != "" {
			t.Errorf("device token request must be anonymous, got %q", got)
		}
		_, _ = w.Write([]byte(`{"status":"success","result":{"token":"device-jwt"}}`))
	})
	mux.HandleFunc("/ajax-api/auth/login.php", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		loginForm = r.PostForm
		_, _ = w.Write([]byte(`{"status":"success","result":{"token":"hash==","user":42,"userRole":"freelancer"}}`))
	})
	mux.HandleFunc("/api/users/0.1/self/", func(w http.ResponseWriter, r *http.Request) {
		authSeen = r.Header.Get(AuthHeader)
		_, _ = w.Write([]byte(`{"status":"success","result":{"id":42,"username":"me","email":"me@example.com","role":"freelancer"}}`))
	})

	client, _ := testClient(t, mux)
	result, err := client.Login(context.Background(), "me@example.com", "secret", LoginOptions{})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if result.UserID != 42 || result.Token != "hash==" {
		t.Fatalf("unexpected login result %+v", result)
	}
	if loginForm.Get("device_token") != "device-jwt" {
		t.Errorf("device token not forwarded: %v", loginForm)
	}
	if loginForm.Get("user") != "me@example.com" || loginForm.Get("password") != "secret" {
		t.Errorf("credentials not forwarded: %v", loginForm)
	}

	self, err := client.Self(context.Background())
	if err != nil {
		t.Fatalf("self: %v", err)
	}
	if self.Username != "me" {
		t.Errorf("username = %q, want me", self.Username)
	}
	if authSeen != "42;hash==" {
		t.Errorf("auth header = %q, want 42;hash==", authSeen)
	}

	// The session must survive a fresh client on the same profile.
	reloaded, err := New(client.Config(), client.Store())
	if err != nil {
		t.Fatalf("reload client: %v", err)
	}
	if !reloaded.HasSession() || reloaded.UserID() != 42 {
		t.Errorf("session not persisted: %+v", reloaded.Session())
	}
}

func TestAPIErrorMapsUnauthorized(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users/0.1/self/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"status":"error","message":"You must be logged in to perform this request","error_code":"RestExceptionCodes.NOT_AUTHENTICATED"}`))
	})

	client, _ := testClient(t, mux)
	client.mu.Lock()
	client.sess.UserID = 7
	client.sess.Token = "stale"
	client.mu.Unlock()

	_, err := client.Self(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("error %v does not unwrap to ErrUnauthorized", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "RestExceptionCodes.NOT_AUTHENTICATED" {
		t.Fatalf("unexpected error shape: %v", err)
	}
}

func TestRetryAfterUnauthorizedUsesStoredCredentials(t *testing.T) {
	calls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/device", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","result":{"token":"device"}}`))
	})
	mux.HandleFunc("/ajax-api/auth/login.php", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","result":{"token":"fresh","user":9,"userRole":"freelancer"}}`))
	})
	mux.HandleFunc("/api/projects/0.1/bids/", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get(AuthHeader) != "9;fresh" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"status":"error","message":"nope"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","result":{"bids":[{"id":1,"project_id":2,"amount":50}]}}`))
	})

	client, _ := testClient(t, mux)
	client.mu.Lock()
	client.sess.UserID = 9
	client.sess.Token = "stale"
	client.mu.Unlock()
	client.SaveCredentials("me", "secret")

	list, err := client.Bids(context.Background(), BidListOptions{})
	if err != nil {
		t.Fatalf("bids: %v", err)
	}
	if len(list.Bids) != 1 || list.Bids[0].ID != 1 {
		t.Fatalf("unexpected bids %+v", list.Bids)
	}
	if calls != 2 {
		t.Errorf("expected one retry, got %d calls", calls)
	}
}

func TestSkillsSendFormArray(t *testing.T) {
	var body string
	var method string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users/0.1/self/jobs/", func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		body = string(buf)
		_, _ = w.Write([]byte(`{"status":"success"}`))
	})

	client, _ := testClient(t, mux)
	client.mu.Lock()
	client.sess.UserID = 1
	client.sess.Token = "t"
	client.mu.Unlock()

	if err := client.SetSkills(context.Background(), []int64{3, 248}); err != nil {
		t.Fatalf("set skills: %v", err)
	}
	if method != http.MethodPut {
		t.Errorf("method = %s, want PUT", method)
	}
	if !strings.Contains(body, "jobs%5B%5D=3") || !strings.Contains(body, "jobs%5B%5D=248") {
		t.Errorf("body %q missing jobs[] entries", body)
	}
	if err := client.SetSkills(context.Background(), nil); err == nil {
		t.Error("expected an error when replacing skills with an empty list")
	}
}

func TestUpdateProfileSendsOnlyChangedFields(t *testing.T) {
	var payload map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/api/users/0.1/self/profile", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&payload)
		_, _ = w.Write([]byte(`{"status":"success"}`))
	})

	client, _ := testClient(t, mux)
	client.mu.Lock()
	client.sess.UserID = 1
	client.sess.Token = "t"
	client.mu.Unlock()

	rate := 25.5
	if err := client.UpdateProfile(context.Background(), ProfileUpdate{HourlyRate: &rate}); err != nil {
		t.Fatalf("update profile: %v", err)
	}
	if len(payload) != 1 || payload["hourly_rate"] != 25.5 {
		t.Fatalf("unexpected payload %v", payload)
	}
	if err := client.UpdateProfile(context.Background(), ProfileUpdate{}); err == nil {
		t.Error("expected an error for an empty update")
	}
}

func TestPlaceBidValidatesInput(t *testing.T) {
	var payload map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/api/projects/0.1/bids/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&payload)
		_, _ = w.Write([]byte(`{"status":"success","result":{"id":77}}`))
	})

	client, _ := testClient(t, mux)
	client.mu.Lock()
	client.sess.UserID = 5
	client.sess.Token = "t"
	client.mu.Unlock()

	if _, err := client.PlaceBid(context.Background(), BidInput{ProjectID: 1, Amount: 0, Period: 3, Description: "x"}); err == nil {
		t.Error("expected an error for a zero amount")
	}
	raw, err := client.PlaceBid(context.Background(), BidInput{ProjectID: 11, Amount: 250, Period: 7, Description: "hello"})
	if err != nil {
		t.Fatalf("place bid: %v", err)
	}
	if string(raw) != `{"id":77}` {
		t.Errorf("result = %s", raw)
	}
	if payload["bidder_id"] != float64(5) {
		t.Errorf("bidder_id = %v, want 5", payload["bidder_id"])
	}
	if payload["milestone_percentage"] != float64(50) {
		t.Errorf("milestone_percentage = %v, want the 50 default", payload["milestone_percentage"])
	}
}

func TestBidActionRejectsUnknownAction(t *testing.T) {
	client, _ := testClient(t, http.NewServeMux())
	client.mu.Lock()
	client.sess.UserID = 1
	client.sess.Token = "t"
	client.mu.Unlock()

	if _, err := client.BidAction(context.Background(), 5, "explode", nil); err == nil {
		t.Fatal("expected an error for an unknown action")
	}
	if !ValidBidAction(BidActionRetract) || ValidBidAction("nope") {
		t.Error("ValidBidAction disagrees with BidActions")
	}
}

func TestSendMessageBuildsMultipart(t *testing.T) {
	var contentType string
	var body []byte
	mux := http.NewServeMux()
	mux.HandleFunc("/api/messages/0.1/threads/12/messages_new/", func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("content-type")
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		body = buf
		_, _ = w.Write([]byte(`{"status":"success","result":{"id":3}}`))
	})

	client, _ := testClient(t, mux)
	client.mu.Lock()
	client.sess.UserID = 1
	client.sess.Token = "t"
	client.mu.Unlock()

	_, err := client.SendMessage(context.Background(), 12, "here you go",
		[]FileUpload{{Name: "build.zip", Data: []byte("payload")}})
	if err != nil {
		t.Fatalf("send message: %v", err)
	}
	if !strings.HasPrefix(contentType, "multipart/form-data") {
		t.Errorf("content type = %q", contentType)
	}
	text := string(body)
	if !strings.Contains(text, `name="files[]"`) || !strings.Contains(text, "build.zip") {
		t.Errorf("multipart body missing the attachment: %q", text)
	}
	if !strings.Contains(text, "here you go") {
		t.Errorf("multipart body missing the message: %q", text)
	}
	if _, err := client.SendMessage(context.Background(), 0, "x", nil); err == nil {
		t.Error("expected an error for a missing thread id")
	}
}

func TestRequireSessionGuardsWrites(t *testing.T) {
	client, _ := testClient(t, http.NewServeMux())
	if _, err := client.Bids(context.Background(), BidListOptions{}); !errors.Is(err, ErrNoSession) {
		t.Fatalf("error = %v, want ErrNoSession", err)
	}
	if err := client.SetChosenRole(context.Background(), "freelancer"); !errors.Is(err, ErrNoSession) {
		t.Fatalf("error = %v, want ErrNoSession", err)
	}
}

func TestErrorEnvelopeOnHTTP200(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/projects/0.1/currencies/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"error","message":"boom","error_code":"X"}`))
	})
	client, _ := testClient(t, mux)
	if _, err := client.Currencies(context.Background()); err == nil {
		t.Fatal("expected an error for an error envelope returned with HTTP 200")
	}
}

func TestParseCVEntryKind(t *testing.T) {
	cases := map[string]CVEntryKind{
		"experience":     CVExperience,
		"educations":     CVEducation,
		"publication":    CVPublication,
		"certifications": CVCertification,
	}
	for input, want := range cases {
		got, err := ParseCVEntryKind(input)
		if err != nil || got != want {
			t.Errorf("ParseCVEntryKind(%q) = %q, %v", input, got, err)
		}
	}
	if _, err := ParseCVEntryKind("hobbies"); err == nil {
		t.Error("expected an error for an unknown section")
	}
	if len(CVExperience.RequiredFields()) == 0 {
		t.Error("experience should document required fields")
	}
}
