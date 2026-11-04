package repository

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
)

// InvariantRepo runs the "books add up" queries. Each method returns one text
// line per violation; an empty list means the invariant holds.
type InvariantRepo struct{}

// UnbalancedEntries: every journal entry must sum to zero.
func (InvariantRepo) UnbalancedEntries(ctx context.Context, tx pgx.Tx) ([]string, error) {
	stmt := `
		SELECT format('entry %s sums to %s', entry_id, SUM(amount))
		FROM postings
		GROUP BY entry_id
		HAVING SUM(amount) <> 0`

	return queryLines(ctx, tx, stmt)
}

// NonZeroBalanceSum: all account balances together must sum to zero.
func (InvariantRepo) NonZeroBalanceSum(ctx context.Context, tx pgx.Tx) ([]string, error) {
	stmt := `
		SELECT format('all balances sum to %s', SUM(balance))
		FROM accounts
		HAVING COALESCE(SUM(balance), 0) <> 0`

	return queryLines(ctx, tx, stmt)
}

// StaleBalances: each cached balance must equal the sum of its postings.
func (InvariantRepo) StaleBalances(ctx context.Context, tx pgx.Tx) ([]string, error) {
	stmt := `
		SELECT format('%s cached %s, postings %s', a.code, a.balance, COALESCE(SUM(p.amount), 0))
		FROM accounts a
		LEFT JOIN postings p ON p.account_id = a.id
		GROUP BY a.id
		HAVING a.balance <> COALESCE(SUM(p.amount), 0)`

	return queryLines(ctx, tx, stmt)
}

// NegativeUserBalances: user accounts must never be negative.
func (InvariantRepo) NegativeUserBalances(ctx context.Context, tx pgx.Tx) ([]string, error) {
	stmt := `
		SELECT format('%s is %s', code, balance)
		FROM accounts
		WHERE owner_id IS NOT NULL AND balance < 0`

	return queryLines(ctx, tx, stmt)
}

// CashHeldMismatches: a user's cash_held must equal the holds of their open
// (non-final) BUY orders.
func (InvariantRepo) CashHeldMismatches(ctx context.Context, tx pgx.Tx) ([]string, error) {
	stmt := `
		SELECT format('user %s cash_held %s, open BUY holds %s', u.id, COALESCE(a.balance, 0), COALESCE(o.held, 0))
		FROM users u
		LEFT JOIN accounts a ON a.code = 'user:' || u.id || ':cash_held'
		LEFT JOIN (
			SELECT user_id, SUM(hold_remaining) AS held
			FROM orders
			WHERE side = 'BUY' AND status <> ALL($1)
			GROUP BY user_id
		) o ON o.user_id = u.id
		WHERE COALESCE(a.balance, 0) <> COALESCE(o.held, 0)`

	return queryLines(ctx, tx, stmt, domain.FinalStatuses)
}

// HeldQtyMismatches: a position's held_qty must equal the holds of open
// (non-final) SELL orders for that symbol.
func (InvariantRepo) HeldQtyMismatches(ctx context.Context, tx pgx.Tx) ([]string, error) {
	stmt := `
		SELECT format('user %s %s held_qty %s, open SELL holds %s', user_id, symbol, COALESCE(p.held_qty, 0), COALESCE(o.held, 0))
		FROM positions p
		FULL JOIN (
			SELECT user_id, symbol, SUM(hold_remaining) AS held
			FROM orders
			WHERE side = 'SELL' AND status <> ALL($1)
			GROUP BY user_id, symbol
		) o USING (user_id, symbol)
		WHERE COALESCE(p.held_qty, 0) <> COALESCE(o.held, 0)`

	return queryLines(ctx, tx, stmt, domain.FinalStatuses)
}

// NegativePositions: quantities must never be negative, and held <= qty.
func (InvariantRepo) NegativePositions(ctx context.Context, tx pgx.Tx) ([]string, error) {
	stmt := `
		SELECT format('user %s %s qty %s held %s', user_id, symbol, qty, held_qty)
		FROM positions
		WHERE qty < 0 OR held_qty < 0 OR held_qty > qty`

	return queryLines(ctx, tx, stmt)
}

// PositionMovementMismatches: a position's qty must equal the sum of its movements.
func (InvariantRepo) PositionMovementMismatches(ctx context.Context, tx pgx.Tx) ([]string, error) {
	stmt := `
		SELECT format('user %s %s qty %s, movements %s', user_id, symbol, COALESCE(p.qty, 0), COALESCE(m.total, 0))
		FROM positions p
		FULL JOIN (
			SELECT user_id, symbol, SUM(qty_delta) AS total
			FROM position_movements
			GROUP BY user_id, symbol
		) m USING (user_id, symbol)
		WHERE COALESCE(p.qty, 0) <> COALESCE(m.total, 0)`

	return queryLines(ctx, tx, stmt)
}

// queryLines runs a query that returns one text column and collects it.
func queryLines(ctx context.Context, tx pgx.Tx, stmt string, args ...any) ([]string, error) {
	rows, err := tx.Query(ctx, stmt, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	lines := []string{}
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return nil, err
		}
		lines = append(lines, line)
	}
	return lines, rows.Err()
}
