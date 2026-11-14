package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/khayode/email-platform/internal/testutil"
)

func TestCheckTenantQuotaRejectsSuspendedWorkspace(t *testing.T) {
	pool := testutil.NewPostgres(t)
	ctx := context.Background()
	var organizationID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (name, slug, status)
		VALUES ('Suspended campaign test', 'suspended-campaign-test', 'suspended')
		RETURNING id::text
	`).Scan(&organizationID); err != nil {
		t.Fatalf("create suspended test organization: %v", err)
	}

	worker := &Worker{pool: pool}
	err := worker.checkTenantQuota(ctx, organizationID)
	if !errors.Is(err, errWorkspaceSuspended) {
		t.Fatalf("checkTenantQuota() error = %v, want suspended workspace rejection", err)
	}
}
