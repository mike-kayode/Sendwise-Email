package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestStalwartAdminClientHostsDomainWithAppManagedDKIM(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != "admin" || password != "synthetic-admin-password" {
			t.Errorf("unexpected management authentication")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/jmap/session":
			httpxJSON(t, w, map[string]any{
				"primaryAccounts": map[string]string{"urn:stalwart:jmap": "configuration-account"},
			})
		case "/jmap/":
			var request struct {
				MethodCalls [][]json.RawMessage `json:"methodCalls"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode JMAP request: %v", err)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			if len(request.MethodCalls) != 1 || len(request.MethodCalls[0]) < 2 {
				t.Errorf("unexpected method calls: %+v", request.MethodCalls)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			var method string
			_ = json.Unmarshal(request.MethodCalls[0][0], &method)
			methods = append(methods, method)
			switch method {
			case "x:Domain/get":
				httpxJSON(t, w, map[string]any{
					"methodResponses": [][]any{{method, map[string]any{"list": []any{}}, "sendwise"}},
				})
			case "x:Domain/set":
				var args map[string]any
				if err := json.Unmarshal(request.MethodCalls[0][1], &args); err != nil {
					t.Errorf("decode Domain/set arguments: %v", err)
					http.Error(w, "bad request", http.StatusBadRequest)
					return
				}
				create := args["create"].(map[string]any)["#sendwise-domain"].(map[string]any)
				if create["name"] != "campaign.example.test" {
					t.Errorf("created domain = %v", create["name"])
				}
				if create["dkimManagement"].(map[string]any)["@type"] != "Manual" {
					t.Errorf("Stalwart DKIM management = %v, want Manual", create["dkimManagement"])
				}
				httpxJSON(t, w, map[string]any{
					"methodResponses": [][]any{{method, map[string]any{
						"created": map[string]any{"#sendwise-domain": map[string]string{"id": "tenant-domain"}},
					}, "sendwise"}},
				})
			default:
				t.Errorf("unexpected JMAP method %q", method)
				http.Error(w, "not found", http.StatusNotFound)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := stalwartAdminClient{
		baseURL: server.URL, username: "admin", password: "synthetic-admin-password",
		client: server.Client(),
	}
	if err := client.EnsureDomain(context.Background(), "campaign.example.test"); err != nil {
		t.Fatalf("ensure hosted domain: %v", err)
	}
	if !reflect.DeepEqual(methods, []string{"x:Domain/get", "x:Domain/set"}) {
		t.Fatalf("Stalwart methods = %v", methods)
	}
}

func TestStalwartAdminClientEnforcesAppManagedDKIMForExistingDomain(t *testing.T) {
	updateCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/jmap/session" {
			httpxJSON(t, w, map[string]any{
				"primaryAccounts": map[string]string{"urn:stalwart:jmap": "configuration-account"},
			})
			return
		}
		if r.URL.Path != "/jmap/" {
			http.NotFound(w, r)
			return
		}
		var request struct {
			MethodCalls [][]json.RawMessage `json:"methodCalls"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode JMAP request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		var method string
		_ = json.Unmarshal(request.MethodCalls[0][0], &method)
		switch method {
		case "x:Domain/get":
			httpxJSON(t, w, map[string]any{
				"methodResponses": [][]any{{method, map[string]any{"list": []any{
					map[string]string{"id": "tenant-domain", "name": "campaign.example.test"},
				}}, "sendwise"}},
			})
		case "x:Domain/set":
			var args map[string]any
			if err := json.Unmarshal(request.MethodCalls[0][1], &args); err != nil {
				t.Errorf("decode Domain/set arguments: %v", err)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			update := args["update"].(map[string]any)["tenant-domain"].(map[string]any)
			if update["dkimManagement"].(map[string]any)["@type"] != "Manual" {
				t.Errorf("existing domain DKIM management = %v, want Manual", update["dkimManagement"])
			}
			updateCalled = true
			httpxJSON(t, w, map[string]any{
				"methodResponses": [][]any{{method, map[string]any{
					"updated": map[string]any{"tenant-domain": map[string]any{}},
				}, "sendwise"}},
			})
		default:
			http.Error(w, "unexpected update", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	client := stalwartAdminClient{
		baseURL: server.URL, client: server.Client(),
	}
	if err := client.EnsureDomain(context.Background(), "campaign.example.test"); err != nil {
		t.Fatalf("ensure existing hosted domain: %v", err)
	}
	if !updateCalled {
		t.Fatal("Stalwart Domain/set did not enforce app-managed DKIM for the existing domain")
	}
}

func httpxJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode test response: %v", err)
	}
}
