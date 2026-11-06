package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/khayode/email-platform/internal/auth"
	"github.com/khayode/email-platform/internal/config"
	"github.com/khayode/email-platform/internal/testutil"
)

func TestP3AudienceEndpointsAndTenantIsolationIntegration(t *testing.T) {
	pool := testutil.NewPostgres(t)
	ctx := context.Background()
	userID, orgID := createImportTestPrincipal(t, ctx, pool, "p3-owner@example.test", "p3-workspace")
	otherUser, otherOrg := createImportTestPrincipal(t, ctx, pool, "p3-other@example.test", "p3-other-workspace")
	tokens := auth.NewManager("p3-integration-secret-value", "integration-test", time.Hour)
	token, err := tokens.IssueAccess(userID, orgID, auth.RoleOwner)
	if err != nil {
		t.Fatal(err)
	}
	otherToken, err := tokens.IssueAccess(otherUser, otherOrg, auth.RoleOwner)
	if err != nil {
		t.Fatal(err)
	}
	server := New(
		config.Config{}, pool, tokens, nil,
		auth.NewAPIKeyService(pool, slog.New(slog.NewTextHandler(io.Discard, nil))),
		nil, slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	handler := server.Handler()
	request := func(method, path, body, authToken string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+authToken)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}

	var contacts []string
	for _, email := range []string{"alice@example.test", "bob@example.test", "carol@example.test"} {
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO contacts (organization_id, email, status, custom_fields, created_at)
			VALUES ($1, $2, $3, $4::jsonb, $5)
			RETURNING id
		`, orgID, email, map[string]string{
			"alice@example.test": "subscribed",
			"bob@example.test":   "subscribed",
			"carol@example.test": "unsubscribed",
		}[email], map[string]string{
			"alice@example.test": `{"city":"Lagos"}`,
			"bob@example.test":   `{"city":"Nairobi"}`,
			"carol@example.test": `{"city":"Lagos"}`,
		}[email], map[string]time.Time{
			"alice@example.test": time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC),
			"bob@example.test":   time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC),
			"carol@example.test": time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		}[email]).Scan(&id); err != nil {
			t.Fatal(err)
		}
		contacts = append(contacts, id)
	}

	createdList := request(http.MethodPost, "/api/v1/lists", `{"name":"Newsletter","description":"weekly"}`, token)
	if createdList.Code != http.StatusCreated {
		t.Fatalf("create list = %d: %s", createdList.Code, createdList.Body.String())
	}
	var list audienceList
	if err := json.Unmarshal(createdList.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if got := request(http.MethodGet, "/api/v1/lists?limit=1&offset=0", "", token); got.Code != http.StatusOK ||
		!strings.Contains(got.Body.String(), `"pagination":{"total":1,"limit":1,"offset":0}`) {
		t.Fatalf("paginated lists = %d: %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPatch, "/api/v1/lists/"+list.ID, `{"name":"Weekly","description":"edited"}`, token); got.Code != http.StatusNoContent {
		t.Fatalf("rename list = %d: %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, "/api/v1/lists/"+list.ID+"/contacts", `{"contact_ids":["`+contacts[0]+`","`+contacts[1]+`"]}`, token); got.Code != http.StatusNoContent {
		t.Fatalf("add list contacts = %d: %s", got.Code, got.Body.String())
	}
	var listCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM contact_list_members WHERE organization_id=$1 AND list_id=$2`, orgID, list.ID).Scan(&listCount); err != nil || listCount != 2 {
		t.Fatalf("list membership count = %d, err=%v", listCount, err)
	}
	members := request(http.MethodGet, "/api/v1/lists/"+list.ID+"/contacts?limit=1&offset=0", "", token)
	if members.Code != http.StatusOK || !strings.Contains(members.Body.String(), `"total":2`) || !strings.Contains(members.Body.String(), `"limit":1`) {
		t.Fatalf("list member page = %d: %s", members.Code, members.Body.String())
	}
	export := request(http.MethodGet, "/api/v1/lists/"+list.ID+"/contacts/export", "", token)
	if export.Code != http.StatusOK {
		t.Fatalf("export list = %d: %s", export.Code, export.Body.String())
	}
	records, err := csv.NewReader(strings.NewReader(export.Body.String())).ReadAll()
	if err != nil || len(records) != 3 || records[0][0] != "email" {
		t.Fatalf("list CSV rows = %#v, err=%v", records, err)
	}
	detail := request(http.MethodGet, "/api/v1/lists/"+list.ID, "", token)
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), `"contact_count":2`) {
		t.Fatalf("list detail count = %d: %s", detail.Code, detail.Body.String())
	}
	if got := request(http.MethodDelete, "/api/v1/lists/"+list.ID+"/contacts/"+contacts[1], "", token); got.Code != http.StatusNoContent {
		t.Fatalf("remove list contact = %d: %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodGet, "/api/v1/lists/"+list.ID, "", token); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"contact_count":1`) {
		t.Fatalf("list count after removal = %d: %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodGet, "/api/v1/lists/"+list.ID, "", otherToken); got.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant list detail = %d: %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodGet, "/api/v1/lists/"+list.ID+"/contacts", "", otherToken); got.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant list members = %d: %s", got.Code, got.Body.String())
	}

	createdTag := request(http.MethodPost, "/api/v1/tags", `{"name":"VIP","color":"#4f46e5"}`, token)
	if createdTag.Code != http.StatusCreated {
		t.Fatalf("create tag = %d: %s", createdTag.Code, createdTag.Body.String())
	}
	var createdTagValue tag
	if err := json.Unmarshal(createdTag.Body.Bytes(), &createdTagValue); err != nil {
		t.Fatal(err)
	}
	if got := request(http.MethodPost, "/api/v1/contacts/"+contacts[0]+"/tags/"+createdTagValue.ID, "", token); got.Code != http.StatusNoContent {
		t.Fatalf("assign contact tag = %d: %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodGet, "/api/v1/contacts/"+contacts[0]+"/tags", "", token); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"name":"VIP"`) {
		t.Fatalf("get contact tags = %d: %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, "/api/v1/contacts/tags", `{"tag_id":"`+createdTagValue.ID+`","contact_ids":["`+contacts[2]+`"]}`, token); got.Code != http.StatusNoContent {
		t.Fatalf("bulk assign tag = %d: %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodGet, "/api/v1/tags?limit=10", "", token); got.Code != http.StatusOK ||
		!strings.Contains(got.Body.String(), `"contact_count":2`) ||
		!strings.Contains(got.Body.String(), `"pagination":{"total":1,"limit":10,"offset":0}`) {
		t.Fatalf("tag count after assignments = %d: %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodGet, "/api/v1/contacts/"+contacts[0]+"/tags?limit=1", "", token); got.Code != http.StatusOK ||
		!strings.Contains(got.Body.String(), `"pagination":{"total":1,"limit":1,"offset":0}`) {
		t.Fatalf("paginated contact tags = %d: %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodDelete, "/api/v1/contacts/"+contacts[2]+"/tags/"+createdTagValue.ID, "", token); got.Code != http.StatusNoContent {
		t.Fatalf("unassign tag = %d: %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodGet, "/api/v1/tags", "", otherToken); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"data":[]`) {
		t.Fatalf("cross-tenant tags = %d: %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodDelete, "/api/v1/tags/"+createdTagValue.ID, "", otherToken); got.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant tag delete = %d: %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, "/api/v1/contacts/"+contacts[0]+"/tags/"+createdTagValue.ID, "", otherToken); got.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant tag assignment = %d: %s", got.Code, got.Body.String())
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO contact_list_members (organization_id, contact_id, list_id)
		VALUES ($1,$2,$3) ON CONFLICT DO NOTHING
	`, orgID, contacts[1], list.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO contact_tags (organization_id, contact_id, tag_id)
		VALUES ($1,$2,$3) ON CONFLICT DO NOTHING
	`, orgID, contacts[0], createdTagValue.ID); err != nil {
		t.Fatal(err)
	}
	shapes := []struct {
		name  string
		rules string
		want  []string
	}{
		{"email", `{"groups":[{"match":"AND","conditions":[{"field":"email","operator":"contains","value":"alice"}]}]}`, []string{"alice@example.test"}},
		{"status", `{"groups":[{"match":"AND","conditions":[{"field":"status","operator":"equals","value":"subscribed"}]}]}`, []string{"alice@example.test", "bob@example.test"}},
		{"tag", `{"groups":[{"match":"AND","conditions":[{"field":"tag","operator":"equals","value":"VIP"}]}]}`, []string{"alice@example.test"}},
		{"list", `{"groups":[{"match":"AND","conditions":[{"field":"list","operator":"equals","value":"Weekly"}]}]}`, []string{"alice@example.test", "bob@example.test"}},
		{"created_at", `{"groups":[{"match":"AND","conditions":[{"field":"created_at","operator":"after","value":"2026-01-02T00:00:00Z"}]}]}`, []string{"alice@example.test", "bob@example.test"}},
		{"custom field", `{"groups":[{"match":"AND","conditions":[{"field":"custom_fields.city","operator":"equals","value":"Lagos"}]}]}`, []string{"alice@example.test", "carol@example.test"}},
	}
	for _, shape := range shapes {
		t.Run("segment "+shape.name, func(t *testing.T) {
			response := request(http.MethodPost, "/api/v1/segments/preview", `{"rules":`+shape.rules+`}`, token)
			if response.Code != http.StatusOK {
				t.Fatalf("preview = %d: %s", response.Code, response.Body.String())
			}
			var preview struct {
				Count int       `json:"count"`
				Data  []contact `json:"data"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil {
				t.Fatal(err)
			}
			got := make([]string, len(preview.Data))
			for i := range preview.Data {
				got[i] = preview.Data[i].Email
			}
			if preview.Count != len(shape.want) || !reflect.DeepEqual(got, shape.want) {
				t.Fatalf("preview contacts=%v count=%d, want %v", got, preview.Count, shape.want)
			}
		})
	}
	orRules := `{"groups":[{"match":"OR","conditions":[{"field":"email","operator":"equals","value":"alice@example.test"},{"field":"status","operator":"equals","value":"unsubscribed"}]}]}`
	orPreview := request(http.MethodPost, "/api/v1/segments/preview", `{"rules":`+orRules+`}`, token)
	if orPreview.Code != http.StatusOK ||
		!strings.Contains(orPreview.Body.String(), `"count":2`) ||
		!strings.Contains(orPreview.Body.String(), `"alice@example.test"`) ||
		!strings.Contains(orPreview.Body.String(), `"carol@example.test"`) {
		t.Fatalf("OR rule preview = %d: %s", orPreview.Code, orPreview.Body.String())
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO contacts (organization_id, email, status)
		SELECT $1, 'p3-preview-' || value || '@example.test', 'bounced'
		FROM generate_series(1, 11) AS valueset(value)
	`, orgID); err != nil {
		t.Fatal(err)
	}
	samplePreview := request(http.MethodPost, "/api/v1/segments/preview", `{"rules":{"groups":[{"match":"AND","conditions":[{"field":"status","operator":"equals","value":"bounced"}]}]}}`, token)
	var sample struct {
		Count int       `json:"count"`
		Data  []contact `json:"data"`
	}
	if samplePreview.Code != http.StatusOK || json.Unmarshal(samplePreview.Body.Bytes(), &sample) != nil || sample.Count != 11 || len(sample.Data) != 10 {
		t.Fatalf("ten-contact sample = %d: %s", samplePreview.Code, samplePreview.Body.String())
	}
	combinedRules := `{"groups":[{"match":"AND","conditions":[{"field":"tag","operator":"equals","value":"VIP"},{"field":"list","operator":"equals","value":"Weekly"}]}]}`
	createdSegment := request(http.MethodPost, "/api/v1/segments", `{"name":"VIP newsletter","rules":`+combinedRules+`}`, token)
	if createdSegment.Code != http.StatusCreated {
		t.Fatalf("create segment = %d: %s", createdSegment.Code, createdSegment.Body.String())
	}
	var segmentValue segment
	if err := json.Unmarshal(createdSegment.Body.Bytes(), &segmentValue); err != nil {
		t.Fatal(err)
	}
	segmentMembers := request(http.MethodGet, "/api/v1/segments/"+segmentValue.ID+"/contacts?limit=10", "", token)
	if segmentMembers.Code != http.StatusOK || !strings.Contains(segmentMembers.Body.String(), `"total":1`) || !strings.Contains(segmentMembers.Body.String(), `"alice@example.test"`) {
		t.Fatalf("segment members = %d: %s", segmentMembers.Code, segmentMembers.Body.String())
	}
	if got := request(http.MethodGet, "/api/v1/segments?limit=5", "", token); got.Code != http.StatusOK ||
		!strings.Contains(got.Body.String(), `"contact_count":1`) ||
		!strings.Contains(got.Body.String(), `"pagination":{"total":1,"limit":5,"offset":0}`) {
		t.Fatalf("segment list = %d: %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodGet, "/api/v1/segments/"+segmentValue.ID+"/contacts?limit=1", "", token); got.Code != http.StatusOK ||
		!strings.Contains(got.Body.String(), `"pagination":{"total":1,"limit":1,"offset":0}`) {
		t.Fatalf("paginated segment contacts = %d: %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, "/api/v1/segments/preview", `{"rules":{"groups":[{"match":"AND","conditions":[{"field":"unknown","operator":"equals","value":"x"}]}]}}`, token); got.Code != http.StatusBadRequest {
		t.Fatalf("malformed rules = %d: %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, "/api/v1/segments", `{"name":"Invalid segment","rules":{"groups":[]}}`, token); got.Code != http.StatusBadRequest {
		t.Fatalf("malformed segment creation = %d: %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodGet, "/api/v1/segments/"+segmentValue.ID+"/contacts", "", otherToken); got.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant segment = %d: %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodDelete, "/api/v1/lists/"+list.ID, "", token); got.Code != http.StatusNoContent {
		t.Fatalf("delete list = %d: %s", got.Code, got.Body.String())
	}
}
