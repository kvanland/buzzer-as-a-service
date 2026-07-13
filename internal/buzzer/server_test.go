package buzzer

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

//go:embed testdata/web/*
var testAssets embed.FS

func TestHTTPCreateJoinBuzzResetFlow(t *testing.T) {
	store := NewMemoryStore(time.Hour)
	server := testServer(t, store)

	create := postJSON(t, server, "/api/groups", `{"hostName":"Host","color":"#ff4d6d"}`, http.StatusCreated)
	var created CreateResult
	mustDecode(t, create, &created)

	join := postJSON(t, server, "/api/groups/"+created.Code+"/join", `{"name":"Player","color":"#2ec4b6"}`, http.StatusOK)
	var joined JoinResult
	mustDecode(t, join, &joined)

	buzzBody := `{"playerId":"` + joined.PlayerID + `","playerToken":"` + joined.PlayerToken + `"}`
	buzz := postJSON(t, server, "/api/groups/"+created.Code+"/buzz", buzzBody, http.StatusOK)
	var buzzed BuzzResult
	mustDecode(t, buzz, &buzzed)
	if !buzzed.Accepted || buzzed.Snapshot.FirstBuzz.PlayerID != joined.PlayerID {
		t.Fatalf("bad buzz result: %+v", buzzed)
	}

	profileBody := `{"playerId":"` + joined.PlayerID + `","playerToken":"` + joined.PlayerToken + `","name":"Renamed","color":"#f9c74f"}`
	profile := postJSON(t, server, "/api/groups/"+created.Code+"/profile", profileBody, http.StatusOK)
	var profiled JoinResult
	mustDecode(t, profile, &profiled)
	if profiled.Snapshot.FirstBuzz.PlayerName != "Renamed" {
		t.Fatalf("profile did not update buzz entry: %+v", profiled.Snapshot.FirstBuzz)
	}

	resetBody := `{"hostToken":"` + created.HostToken + `"}`
	reset := postJSON(t, server, "/api/groups/"+created.Code+"/reset", resetBody, http.StatusOK)
	var snapshot Snapshot
	mustDecode(t, reset, &snapshot)
	if snapshot.FirstBuzz != nil || len(snapshot.Buzzes) != 0 || snapshot.Round != 2 {
		t.Fatalf("reset did not clear buzzes and advance round: %+v", snapshot)
	}

	roundReset := postJSON(t, server, "/api/groups/"+created.Code+"/reset-round-count", resetBody, http.StatusOK)
	mustDecode(t, roundReset, &snapshot)
	if snapshot.Round != 1 {
		t.Fatalf("round count reset returned round %d, want 1", snapshot.Round)
	}

	remove := postJSON(t, server, "/api/groups/"+created.Code+"/players/"+joined.PlayerID+"/remove", resetBody, http.StatusOK)
	mustDecode(t, remove, &snapshot)
	if len(snapshot.Players) != 1 {
		t.Fatalf("remove player left players: %+v", snapshot.Players)
	}
}

func TestHTTPCreateGroupLimitReturnsTooManyRequests(t *testing.T) {
	store := NewMemoryStore(time.Hour)
	server := testServer(t, store)
	for i := 0; i < maxActiveGroups; i++ {
		postJSONFromIP(t, server, "/api/groups", `{"hostName":"Host"}`, fmt.Sprintf("198.51.100.%d", i), http.StatusCreated)
	}
	postJSONFromIP(t, server, "/api/groups", `{"hostName":"Host"}`, "203.0.113.200", http.StatusTooManyRequests)
}

func TestHTTPCreateRateLimitReturnsTooManyRequests(t *testing.T) {
	store := NewMemoryStore(time.Hour)
	server := testServer(t, store)
	for i := 0; i < maxCreateRequestsPerWindow; i++ {
		postJSONFromIP(t, server, "/api/groups", `{"hostName":"Host"}`, "203.0.113.10", http.StatusCreated)
	}
	postJSONFromIP(t, server, "/api/groups", `{"hostName":"Host"}`, "203.0.113.10", http.StatusTooManyRequests)
}

func TestRateLimiterEventStreamLimit(t *testing.T) {
	limiter := newRateLimiter()
	releases := make([]func(), 0, maxEventStreamsPerIP)
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	for i := 0; i < maxEventStreamsPerIP; i++ {
		release, ok := limiter.acquireEvent("203.0.113.11")
		if !ok {
			t.Fatalf("event stream %d rejected", i)
		}
		releases = append(releases, release)
	}
	if _, ok := limiter.acquireEvent("203.0.113.11"); ok {
		t.Fatal("event stream over limit was accepted")
	}
}

func TestHTTPRejectsOversizedJSONBody(t *testing.T) {
	store := NewMemoryStore(time.Hour)
	server := testServer(t, store)
	body := `{"hostName":"` + strings.Repeat("x", int(maxJSONBodyBytes)) + `"}`
	postJSON(t, server, "/api/groups", body, http.StatusRequestEntityTooLarge)
}

func TestHTTPRejectsMultipleJSONValues(t *testing.T) {
	store := NewMemoryStore(time.Hour)
	server := testServer(t, store)
	postJSON(t, server, "/api/groups", `{"hostName":"Host"}{}`, http.StatusBadRequest)
}

func TestHTTPSupportsPrefixedMount(t *testing.T) {
	store := NewMemoryStore(time.Hour)
	server := testServer(t, store)
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/buzzer-as-a-service/api/groups", strings.NewReader(`{"hostName":"Host"}`))
	req.Header.Set("Content-Type", "application/json")
	server.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", res.Code, res.Body.String())
	}
}

func testServer(t *testing.T, store *Store) http.Handler {
	t.Helper()
	assets, err := fs.Sub(testAssets, "testdata/web")
	if err != nil {
		t.Fatal(err)
	}
	return NewServer(store, assets)
}

func postJSON(t *testing.T, handler http.Handler, path, body string, want int) string {
	t.Helper()
	return postJSONFromIP(t, handler, path, body, "192.0.2.1", want)
}

func postJSONFromIP(t *testing.T, handler http.Handler, path, body, ip string, want int) string {
	t.Helper()
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cf-Connecting-Ip", ip)
	handler.ServeHTTP(res, req)
	if res.Code != want {
		t.Fatalf("POST %s status = %d body=%s", path, res.Code, res.Body.String())
	}
	return res.Body.String()
}

func mustDecode(t *testing.T, body string, target any) {
	t.Helper()
	if err := json.Unmarshal([]byte(body), target); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
}
