package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khayode/email-platform/services/identity"
)

func main() {
	email := flag.String("email", "", "email address of an existing verified account")
	role := flag.String("role", identity.PlatformRoleSuperAdmin, "platform role: support_admin or super_admin")
	reason := flag.String("reason", "", "required operator reason for the promotion")
	flag.Parse()

	normalizedEmail := strings.ToLower(strings.TrimSpace(*email))
	normalizedRole := strings.TrimSpace(*role)
	if normalizedEmail == "" || strings.TrimSpace(*reason) == "" {
		fmt.Fprintln(os.Stderr, "usage: admin-bootstrap -email user@example.com -role super_admin -reason 'operator reason'")
		os.Exit(2)
	}
	if normalizedRole != identity.PlatformRoleSupportAdmin && normalizedRole != identity.PlatformRoleSuperAdmin {
		fmt.Fprintln(os.Stderr, "role must be support_admin or super_admin")
		os.Exit(2)
	}
	databaseURL := strings.TrimSpace(os.Getenv("IDENTITY_DATABASE_URL"))
	if databaseURL == "" {
		databaseURL = strings.TrimSpace(os.Getenv("DATABASE_URL"))
	}
	if databaseURL == "" {
		fmt.Fprintln(os.Stderr, "IDENTITY_DATABASE_URL or DATABASE_URL is required")
		os.Exit(2)
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "connect identity database:", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "ping identity database:", err)
		os.Exit(1)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "begin platform role update:", err)
		os.Exit(1)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('sendwise_platform_role_changes'))`); err != nil {
		fmt.Fprintln(os.Stderr, "serialize platform role update:", err)
		os.Exit(1)
	}

	var userID, oldRole string
	err = tx.QueryRow(ctx, `
		SELECT id::text, platform_role
		FROM users
		WHERE lower(email) = $1 AND status = 'active' AND email_verified_at IS NOT NULL
		FOR UPDATE
	`, normalizedEmail).Scan(&userID, &oldRole)
	if err != nil {
		fmt.Fprintln(os.Stderr, "find active verified account; no role was changed:", err)
		os.Exit(1)
	}
	if oldRole == normalizedRole {
		fmt.Fprintln(os.Stderr, "account already has the requested platform role; no change made")
		os.Exit(0)
	}

	before, _ := json.Marshal(map[string]string{"platform_role": oldRole})
	after, _ := json.Marshal(map[string]string{"platform_role": normalizedRole})
	if _, err := tx.Exec(ctx, `
		UPDATE users SET platform_role = $2, updated_at = now() WHERE id = $1
	`, userID, normalizedRole); err != nil {
		fmt.Fprintln(os.Stderr, "update platform role:", err)
		os.Exit(1)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO admin_audit (
			actor_user_id, actor_role, action, target_type, target_id,
			before_state, after_state, reason, user_agent
		) VALUES (NULL, 'operator', 'platform_role_bootstrapped', 'user', $1, $2, $3, $4, 'cmd/admin-bootstrap')
	`, userID, before, after, strings.TrimSpace(*reason)); err != nil {
		fmt.Fprintln(os.Stderr, "write promotion audit entry:", err)
		os.Exit(1)
	}
	if err := tx.Commit(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "commit platform role update:", err)
		os.Exit(1)
	}
	fmt.Println("platform role updated and audited")
}
