package domainwarmup

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type Reputation struct {
	BounceRate    float64
	ComplaintRate float64
}

func RevokeIfUnsafe(ctx context.Context, tx pgx.Tx, domainID string) (bool, Reputation, error) {
	var organizationID, domain string
	var bounced, successful int64
	if err := tx.QueryRow(ctx, `
		SELECT sd.organization_id::text, sd.domain,
		       COALESCE(sum(stats.bounce_count), 0),
		       COALESCE(sum(stats.successful_count), 0)
		FROM sending_domains sd
		LEFT JOIN sending_domain_daily_stats stats
		  ON stats.organization_id = sd.organization_id
		 AND lower(stats.domain) = lower(sd.domain)
		 AND stats.sending_day >= CURRENT_DATE - 6
		WHERE sd.id = $1 AND sd.warmup_graduated_at IS NOT NULL
		GROUP BY sd.organization_id, sd.domain
	`, domainID).Scan(&organizationID, &domain, &bounced, &successful); err != nil {
		if err == pgx.ErrNoRows {
			return false, Reputation{}, nil
		}
		return false, Reputation{}, fmt.Errorf("load graduated-domain bounce rate: %w", err)
	}
	var complained, submitted int64
	if err := tx.QueryRow(ctx, `
		SELECT count(DISTINCT me.delivery_job_id) FILTER (WHERE me.event_type = 'complained'),
		       count(DISTINCT me.delivery_job_id) FILTER (WHERE me.event_type IN ('submitted', 'accepted'))
		FROM message_events me
		JOIN campaigns c ON c.id = me.campaign_id
		WHERE c.organization_id = $1
		  AND lower(split_part(c.sender_email, '@', 2)) = lower($2)
		  AND me.occurred_at >= now() - interval '7 days'
		  AND me.event_type IN ('complained', 'submitted', 'accepted')
	`, organizationID, domain).Scan(&complained, &submitted); err != nil {
		return false, Reputation{}, fmt.Errorf("load graduated-domain complaint rate: %w", err)
	}
	reputation := Reputation{}
	if total := bounced + successful; total > 0 {
		reputation.BounceRate = float64(bounced) / float64(total)
	}
	if total := complained + submitted; total > 0 {
		reputation.ComplaintRate = float64(complained) / float64(total)
	}
	if reputation.BounceRate <= 0.02 && reputation.ComplaintRate <= 0.0005 {
		return false, reputation, nil
	}
	tag, err := tx.Exec(ctx, `
		UPDATE sending_domains
		SET warmup_graduated_at = NULL,
		    warmup_graduated_by = NULL,
		    warmup_graduated_reason = '',
		    updated_at = now()
		WHERE id = $1 AND warmup_graduated_at IS NOT NULL
	`, domainID)
	if err != nil {
		return false, reputation, fmt.Errorf("revoke unsafe warm-up graduation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return false, reputation, nil
	}
	message := fmt.Sprintf(
		"Warm-up graduation automatically revoked: 7-day bounce rate %.3f%%, complaint rate %.4f%%.",
		100*reputation.BounceRate, 100*reputation.ComplaintRate,
	)
	if _, err := tx.Exec(ctx, `
		INSERT INTO sending_domain_alerts (organization_id, domain_id, alert_type, message)
		VALUES ($1, $2, 'warmup_graduation_revoked', $3)
	`, organizationID, domainID, message); err != nil {
		return false, reputation, fmt.Errorf("create warm-up graduation alert: %w", err)
	}
	return true, reputation, nil
}
