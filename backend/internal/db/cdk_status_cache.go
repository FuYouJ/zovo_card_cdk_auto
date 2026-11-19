package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type CDKStatusSnapshot struct {
	Status string
	Plan   string
}

func GetCachedCDKStatus(ctx context.Context, code string, freshSince time.Time) (*CDKStatusSnapshot, error) {
	var snapshot CDKStatusSnapshot
	err := DB.QueryRowContext(ctx, `
		SELECT status, plan FROM cardplatform_cdk_status_cache
		WHERE code_hash = ? AND checked_at > ?
	`, HashCDKCode(code), freshSince.UnixMilli()).Scan(&snapshot.Status, &snapshot.Plan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &snapshot, err
}

func SaveCachedCDKStatus(ctx context.Context, code string, snapshot CDKStatusSnapshot, checkedAt time.Time) error {
	_, err := DB.ExecContext(ctx, `
		INSERT INTO cardplatform_cdk_status_cache (code_hash, status, plan, checked_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(code_hash) DO UPDATE SET
			status = excluded.status, plan = excluded.plan, checked_at = excluded.checked_at
		WHERE excluded.checked_at >= cardplatform_cdk_status_cache.checked_at
	`, HashCDKCode(code), snapshot.Status, snapshot.Plan, checkedAt.UnixMilli())
	return err
}
