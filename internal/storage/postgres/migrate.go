package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/migrations"
)

// Migrate is an explicit operator action; Open and doctor never invoke it.
func Migrate(ctx context.Context, dsn string) error {
	cfg, err := connectionConfig(dsn)
	if err != nil {
		return err
	}
	conn, err := pgx.ConnectConfig(ctx, cfg.ConnConfig)
	if err != nil {
		return classify(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = conn.Close(cleanup)
	}()
	if err = migrations.Apply(ctx, conn); err != nil {
		return domain.ErrMigration
	}
	return nil
}
