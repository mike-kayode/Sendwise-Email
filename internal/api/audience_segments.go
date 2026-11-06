package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/khayode/email-platform/internal/contacts"
	"github.com/khayode/email-platform/internal/httpx"
)

type segmentPreviewInput struct {
	Rules json.RawMessage `json:"rules"`
}

type segmentPreview struct {
	Count int       `json:"count"`
	Data  []contact `json:"data"`
}

func (s *Server) countSegmentContacts(ctx context.Context, organizationID string, rules json.RawMessage) (int, error) {
	predicate, args, err := contacts.CompileSegmentRules(rules, 2)
	if err != nil {
		return 0, err
	}
	queryArgs := append([]any{organizationID}, args...)
	var count int
	err = s.pool.QueryRow(ctx, `
		SELECT count(*) FROM contacts c
		WHERE c.organization_id = $1 AND `+predicate, queryArgs...).Scan(&count)
	return count, err
}

func (s *Server) previewSegment(w http.ResponseWriter, r *http.Request) {
	var input segmentPreviewInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "The request body is invalid.")
		return
	}
	predicate, args, err := contacts.CompileSegmentRules(input.Rules, 2)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_rules", err.Error())
		return
	}
	result, err := evaluateSegment(r.Context(), s, s.organizationID(r), predicate, args)
	if err != nil {
		s.logger.Error("preview segment", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not preview the segment.")
		return
	}
	httpx.JSON(w, http.StatusOK, result)
}

func (s *Server) listSegmentContacts(w http.ResponseWriter, r *http.Request) {
	orgID := s.organizationID(r)
	var rules []byte
	err := s.pool.QueryRow(r.Context(), `
		SELECT rules FROM segments WHERE id = $1 AND organization_id = $2
	`, r.PathValue("id"), orgID).Scan(&rules)
	if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
		httpx.Error(w, http.StatusNotFound, "segment_not_found", "The segment could not be found.")
		return
	}
	if err != nil {
		s.logger.Error("load segment contacts", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load segment contacts.")
		return
	}
	limit, offset := pagination(r)
	predicate, args, err := contacts.CompileSegmentRules(rules, 2)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "invalid_segment", "The saved segment rules are invalid.")
		return
	}
	queryArgs := append([]any{orgID}, args...)
	var total int
	if err := s.pool.QueryRow(r.Context(), `
		SELECT count(*) FROM contacts c
		WHERE c.organization_id = $1 AND `+predicate, queryArgs...).Scan(&total); err != nil {
		s.logger.Error("count segment contacts", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load segment contacts.")
		return
	}
	queryArgs = append(queryArgs, limit, offset)
	rows, err := s.pool.Query(r.Context(), `
		SELECT `+listMemberContactColumns+`
		FROM contacts c
		WHERE c.organization_id = $1 AND `+predicate+`
		ORDER BY c.created_at DESC, c.id
		LIMIT $`+strconv.Itoa(len(queryArgs)-1)+` OFFSET $`+strconv.Itoa(len(queryArgs))+`
	`, queryArgs...)
	if err != nil {
		s.logger.Error("list segment contacts", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load segment contacts.")
		return
	}
	defer rows.Close()
	items := make([]contact, 0, limit)
	for rows.Next() {
		item, err := scanContact(rows)
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load segment contacts.")
			return
		}
		items = append(items, *item)
	}
	if err := rows.Err(); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load segment contacts.")
		return
	}
	httpx.JSON(w, http.StatusOK, listResponse[contact]{
		Data: items, Pagination: paginationResponse{Total: total, Limit: limit, Offset: offset},
	})
}

func evaluateSegment(ctx context.Context, s *Server, orgID, predicate string, args []any) (segmentPreview, error) {
	queryArgs := append([]any{orgID}, args...)
	var result segmentPreview
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM contacts c WHERE c.organization_id = $1 AND `+predicate,
		queryArgs...).Scan(&result.Count); err != nil {
		return segmentPreview{}, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+listMemberContactColumns+`
		FROM contacts c
		WHERE c.organization_id = $1 AND `+predicate+`
		ORDER BY c.created_at DESC, c.id
		LIMIT 10
	`, queryArgs...)
	if err != nil {
		return segmentPreview{}, err
	}
	defer rows.Close()
	result.Data = make([]contact, 0, 10)
	for rows.Next() {
		item, err := scanContact(rows)
		if err != nil {
			return segmentPreview{}, err
		}
		result.Data = append(result.Data, *item)
	}
	return result, rows.Err()
}
