package db

import "context"

type CDKOwnerSession struct {
	Product, Plan, PreflightDigest string
	CardID                         int64
}

func SaveCDKOwnerPreview(ctx context.Context, token, device, product, plan string, expires, now int64) error {
	tx, err := DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM cdk_owner_redeem_sessions WHERE expires_at<=?", now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO cdk_owner_redeem_sessions
		(token_digest,device_digest,product,plan,expires_at) VALUES(?,?,?,?,?)`, token, device, product, plan, expires); err != nil {
		return err
	}
	return tx.Commit()
}

func LoadCDKOwnerSession(ctx context.Context, token, device string, now int64) (CDKOwnerSession, error) {
	var session CDKOwnerSession
	err := DB.QueryRowContext(ctx, `SELECT product,plan,preflight_digest,card_id FROM cdk_owner_redeem_sessions
		WHERE token_digest=? AND device_digest=? AND expires_at>?`, token, device, now).
		Scan(&session.Product, &session.Plan, &session.PreflightDigest, &session.CardID)
	return session, err
}

func LockCDKOwnerCard(ctx context.Context, token, device, preflight string, card int64, now int64) (CDKOwnerSession, error) {
	if _, err := DB.ExecContext(ctx, `UPDATE cdk_owner_redeem_sessions SET card_id=?,preflight_digest=?
		WHERE token_digest=? AND device_digest=? AND expires_at>? AND card_id=0 AND product='gpt'`,
		card, preflight, token, device, now); err != nil {
		return CDKOwnerSession{}, err
	}
	return LoadCDKOwnerSession(ctx, token, device, now)
}
