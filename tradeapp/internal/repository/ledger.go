package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
)

type LedgerRepo struct{}

// CreateJournalEntry inserts an entry header. domain.ErrDuplicate means an
// entry with the same (kind, source_type, source_id) already exists.
func (LedgerRepo) CreateJournalEntry(ctx context.Context, tx pgx.Tx, e domain.Entry) error {
	stmt := `INSERT INTO journal_entries (kind, source_type, source_id)
	         VALUES ($1, $2, $3)
	         ON CONFLICT (kind, source_type, source_id) DO NOTHING`

	tag, err := tx.Exec(ctx, stmt, e.Kind, e.SourceType, e.SourceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrDuplicate
	}
	return nil
}

func (LedgerRepo) GetJournalEntryID(ctx context.Context, tx pgx.Tx, kind, sourceType, sourceID string) (int64, error) {
	stmt := `SELECT id FROM journal_entries
	         WHERE kind = $1 AND source_type = $2 AND source_id = $3`

	var id int64
	err := tx.QueryRow(ctx, stmt, kind, sourceType, sourceID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, domain.ErrNotFound
	}
	return id, err
}

func (LedgerRepo) CreatePosting(ctx context.Context, tx pgx.Tx, entryID, accountID, amount int64) error {
	stmt := `INSERT INTO postings (entry_id, account_id, amount) VALUES ($1, $2, $3)`

	_, err := tx.Exec(ctx, stmt, entryID, accountID, amount)
	return err
}

// GetAccountsForUpdate reads the accounts with these codes and locks them,
// in id order. Codes that don't exist are simply missing from the result.
func (LedgerRepo) GetAccountsForUpdate(ctx context.Context, tx pgx.Tx, codes []string) ([]domain.LedgerAccount, error) {
	stmt := `
		SELECT id, code, owner_id, balance
		FROM accounts
		WHERE code = ANY($1)
		ORDER BY id
		FOR UPDATE`

	rows, err := tx.Query(ctx, stmt, codes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	accounts := []domain.LedgerAccount{}
	for rows.Next() {
		var a domain.LedgerAccount
		if err := rows.Scan(&a.ID, &a.Code, &a.OwnerID, &a.Balance); err != nil {
			return nil, err
		}
		accounts = append(accounts, a)
	}
	return accounts, rows.Err()
}

func (LedgerRepo) UpdateAccountBalance(ctx context.Context, tx pgx.Tx, accountID, balance int64) error {
	stmt := `UPDATE accounts SET balance = $2 WHERE id = $1`

	tag, err := tx.Exec(ctx, stmt, accountID, balance)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (LedgerRepo) GetAccountBalance(ctx context.Context, tx pgx.Tx, code string) (int64, error) {
	stmt := `SELECT balance FROM accounts WHERE code = $1`

	var balance int64
	err := tx.QueryRow(ctx, stmt, code).Scan(&balance)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, domain.ErrNotFound
	}
	return balance, err
}

func (LedgerRepo) CountJournalEntries(ctx context.Context, tx pgx.Tx, kind string) (int, error) {
	stmt := `SELECT count(*) FROM journal_entries WHERE kind = $1`

	var n int
	err := tx.QueryRow(ctx, stmt, kind).Scan(&n)
	return n, err
}
