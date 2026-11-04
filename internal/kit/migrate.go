package kit

import (
	"context"

	"github.com/khayode/email-platform/internal/db"
)

// Migrate applies the service's own SQL migrations in lexical order against
// its private database. Each service owns its migration history; there is no
// shared migration chain across services.
func Migrate(ctx context.Context, databaseURL, dir string) error {
	return db.Migrate(ctx, databaseURL, dir)
}
