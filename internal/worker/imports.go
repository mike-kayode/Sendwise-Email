package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/khayode/email-platform/internal/contacts"
)

const (
	contactImportBatchSize = 250
	contactImportMaxRows   = 100000
	contactImportMaxErrors = 100
	contactImportMaxTries  = 5
	contactImportLease     = 5 * time.Minute
)

var errContactImportLeaseLost = errors.New("contact import lease lost")

type contactImportJob struct {
	ID            string
	Organization  string
	LeaseToken    string
	SourceData    []byte
	Mapping       []byte
	Attempts      int
	TotalRows     int
	ProcessedRows int
	Imported      int
	Skipped       int
	Invalid       int
	Errors        []byte
}

type contactImportRowError struct {
	Row     int    `json:"row"`
	Message string `json:"message"`
}

func (w *Worker) contactImportLoop(ctx context.Context) {
	ticker := time.NewTicker(w.cfg.SchedulerInterval)
	defer ticker.Stop()

	for {
		if err := w.processPendingContactImports(ctx); err != nil && ctx.Err() == nil {
			w.logger.Error("process contact imports", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *Worker) processPendingContactImports(ctx context.Context) error {
	for range 4 {
		job, err := w.claimContactImport(ctx)
		if err != nil {
			return err
		}
		if job == nil {
			return nil
		}
		if err := w.processContactImport(ctx, job); err != nil {
			if errors.Is(err, errContactImportLeaseLost) {
				continue
			}
			w.logger.Warn("contact import deferred", "import_id", job.ID, "error", err)
			if releaseErr := w.releaseContactImport(ctx, job, err); releaseErr != nil {
				return releaseErr
			}
		}
	}
	return nil
}

func (w *Worker) claimContactImport(ctx context.Context) (*contactImportJob, error) {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin import claim: %w", err)
	}
	defer tx.Rollback(ctx)

	job := &contactImportJob{}
	err = tx.QueryRow(ctx, `
		SELECT id, organization_id, source_data, mapping, attempts,
		       total_rows, processed_rows, imported_count, skipped_count,
		       invalid_count, errors
		FROM contact_imports
		WHERE (status = 'queued' AND next_attempt_at <= now())
		   OR (status = 'processing' AND lease_expires_at < now())
		ORDER BY created_at
		LIMIT 1
		FOR UPDATE SKIP LOCKED
	`).Scan(
		&job.ID, &job.Organization, &job.SourceData, &job.Mapping, &job.Attempts,
		&job.TotalRows, &job.ProcessedRows, &job.Imported, &job.Skipped,
		&job.Invalid, &job.Errors,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load pending import: %w", err)
	}

	if err := tx.QueryRow(ctx, `
		UPDATE contact_imports
		SET status = 'processing', attempts = attempts + 1,
		    lease_expires_at = now() + $2::interval,
		    lease_token = gen_random_uuid(),
		    started_at = COALESCE(started_at, now()), last_error = NULL,
		    updated_at = now()
		WHERE id = $1
		RETURNING lease_token
	`, job.ID, fmt.Sprintf("%d seconds", int(contactImportLease.Seconds()))).Scan(&job.LeaseToken); err != nil {
		return nil, fmt.Errorf("lease contact import: %w", err)
	}
	job.Attempts++
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit import claim: %w", err)
	}
	return job, nil
}

func (w *Worker) processContactImport(ctx context.Context, job *contactImportJob) error {
	var mapping contacts.CSVColumnMapping
	if err := json.Unmarshal(job.Mapping, &mapping); err != nil {
		return w.failContactImport(ctx, job, "The saved column mapping is invalid.")
	}
	totalRows, err := countContactImportRows(job.SourceData, &mapping)
	if err != nil {
		return w.failContactImport(ctx, job, "The CSV contains too many rows or has an invalid header.")
	}
	if totalRows > contacts.MaxCSVImportRows {
		return w.failContactImport(ctx, job, fmt.Sprintf("CSV imports are limited to %d rows.", contacts.MaxCSVImportRows))
	}
	job.TotalRows = totalRows
	reader, _, err := contacts.NewCSVReader(bytes.NewReader(job.SourceData), &mapping)
	if err != nil {
		return w.failContactImport(ctx, job, "The saved CSV or column mapping is invalid.")
	}

	rowNumber := 0
	for rowNumber < job.ProcessedRows {
		if _, err := reader.Read(); err != nil && !errors.Is(err, io.EOF) {
			rowNumber++
			continue
		} else if errors.Is(err, io.EOF) {
			return w.failContactImport(ctx, job, "The saved import progress exceeds the CSV row count.")
		}
		rowNumber++
	}

	var rowErrors []contactImportRowError
	if len(job.Errors) > 0 {
		if err := json.Unmarshal(job.Errors, &rowErrors); err != nil {
			rowErrors = nil
		}
	}

	batch := make([]contacts.CSVContactRow, 0, contactImportBatchSize)
	rowsInCheckpoint := 0
	for {
		row, readErr := reader.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		rowNumber++
		rowsInCheckpoint++
		if readErr != nil {
			job.Invalid++
			appendImportError(&rowErrors, rowNumber, "Invalid CSV row.")
		} else {
			batch = append(batch, row)
		}

		if rowsInCheckpoint >= contactImportBatchSize {
			if err := w.commitContactImportBatch(ctx, job, batch, totalRows, rowNumber, rowErrors); err != nil {
				return err
			}
			batch = batch[:0]
			rowsInCheckpoint = 0
		}
	}

	if rowsInCheckpoint > 0 || job.ProcessedRows == 0 {
		if err := w.commitContactImportBatch(ctx, job, batch, totalRows, rowNumber, rowErrors); err != nil {
			return err
		}
	}
	return w.completeContactImport(ctx, job, rowNumber, rowErrors)
}

func countContactImportRows(source []byte, mapping *contacts.CSVColumnMapping) (int, error) {
	reader, _, err := contacts.NewCSVReader(bytes.NewReader(source), mapping)
	if err != nil {
		return 0, err
	}
	rows := 0
	for {
		_, err := reader.Read()
		if errors.Is(err, io.EOF) {
			return rows, nil
		}
		rows++
		if rows > contacts.MaxCSVImportRows {
			return rows, nil
		}
	}
}

func (w *Worker) commitContactImportBatch(ctx context.Context, job *contactImportJob, rows []contacts.CSVContactRow, totalRows, cursor int, rowErrors []contactImportRowError) error {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin import batch: %w", err)
	}
	defer tx.Rollback(ctx)

	var leaseID string
	err = tx.QueryRow(ctx, `
		SELECT id FROM contact_imports
		WHERE id = $1 AND status = 'processing'
		  AND lease_token = $2 AND lease_expires_at > now()
		FOR UPDATE
	`, job.ID, job.LeaseToken).Scan(&leaseID)
	if errors.Is(err, pgx.ErrNoRows) {
		return errContactImportLeaseLost
	}
	if err != nil {
		return fmt.Errorf("lock contact import checkpoint: %w", err)
	}

	if len(rows) > 0 {
		batch := &pgx.Batch{}
		for _, row := range rows {
			batch.Queue(`
				INSERT INTO contacts (organization_id, email, first_name, last_name, status, custom_fields, source)
				VALUES ($1, $2, $3, $4, 'pending', $5::jsonb, 'csv_import')
				ON CONFLICT (organization_id, lower(email)) DO NOTHING
			`, job.Organization, row.Email, optionalImportText(row.FirstName), optionalImportText(row.LastName), row.CustomFields)
		}
		results := tx.SendBatch(ctx, batch)
		inserted := 0
		for range rows {
			tag, err := results.Exec()
			if err != nil {
				_ = results.Close()
				return fmt.Errorf("insert imported contacts: %w", err)
			}
			inserted += int(tag.RowsAffected())
		}
		if err := results.Close(); err != nil {
			return fmt.Errorf("close import batch: %w", err)
		}
		job.Imported += inserted
		job.Skipped += len(rows) - inserted
	}

	job.ProcessedRows = cursor
	job.TotalRows = totalRows
	errorJSON, err := json.Marshal(rowErrors)
	if err != nil {
		return fmt.Errorf("encode import errors: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE contact_imports
		SET total_rows = $2, processed_rows = $3,
		    imported_count = $4, skipped_count = $5, invalid_count = $6,
		    errors = $7::jsonb, lease_expires_at = now() + $8::interval,
		    updated_at = now()
		WHERE id = $1 AND status = 'processing' AND lease_token = $9
	`, job.ID, job.TotalRows, job.ProcessedRows, job.Imported, job.Skipped, job.Invalid, errorJSON,
		fmt.Sprintf("%d seconds", int(contactImportLease.Seconds())), job.LeaseToken); err != nil {
		return fmt.Errorf("save import progress: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit import batch: %w", err)
	}
	return nil
}

func (w *Worker) completeContactImport(ctx context.Context, job *contactImportJob, totalRows int, rowErrors []contactImportRowError) error {
	errorJSON, err := json.Marshal(rowErrors)
	if err != nil {
		return fmt.Errorf("encode final import errors: %w", err)
	}
	_, err = w.pool.Exec(ctx, `
		UPDATE contact_imports
		SET status = 'completed', total_rows = $2, processed_rows = $2,
		    imported_count = $3, skipped_count = $4, invalid_count = $5,
		    errors = $6::jsonb, source_data = ''::bytea,
		    lease_expires_at = NULL, lease_token = NULL, completed_at = now(), updated_at = now()
		WHERE id = $1 AND status = 'processing' AND lease_token = $7
	`, job.ID, totalRows, job.Imported, job.Skipped, job.Invalid, errorJSON, job.LeaseToken)
	if err != nil {
		return fmt.Errorf("complete contact import: %w", err)
	}
	return nil
}

func (w *Worker) failContactImport(ctx context.Context, job *contactImportJob, message string) error {
	_, err := w.pool.Exec(ctx, `
		UPDATE contact_imports
		SET status = 'failed', last_error = $2, source_data = ''::bytea,
		    lease_expires_at = NULL, lease_token = NULL, completed_at = now(), updated_at = now()
		WHERE id = $1 AND status = 'processing' AND lease_token = $3
	`, job.ID, truncateError(errors.New(message)), job.LeaseToken)
	if err != nil {
		return fmt.Errorf("fail contact import: %w", err)
	}
	return nil
}

func (w *Worker) releaseContactImport(ctx context.Context, job *contactImportJob, cause error) error {
	if job.Attempts >= contactImportMaxTries {
		return w.failContactImport(ctx, job, "The import could not be completed after several attempts.")
	}
	delay := time.Duration(1<<min(job.Attempts-1, 8)) * time.Minute
	_, err := w.pool.Exec(ctx, `
		UPDATE contact_imports
		SET status = 'queued', next_attempt_at = now() + $2::interval,
		    lease_expires_at = NULL, last_error = $3, updated_at = now(),
		    lease_token = NULL
		WHERE id = $1 AND status = 'processing' AND lease_token = $4
	`, job.ID, fmt.Sprintf("%d seconds", int(delay.Seconds())), truncateError(cause), job.LeaseToken)
	if err != nil {
		return fmt.Errorf("release contact import: %w", err)
	}
	return nil
}

func appendImportError(errorsList *[]contactImportRowError, row int, message string) {
	if len(*errorsList) >= contactImportMaxErrors {
		return
	}
	*errorsList = append(*errorsList, contactImportRowError{Row: row, Message: message})
}

func optionalImportText(value string) any {
	if value == "" {
		return nil
	}
	return value
}
