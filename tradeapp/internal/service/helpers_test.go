package service

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/babafem99/vfs/tradeapp/internal/dbtest"
	"github.com/babafem99/vfs/tradeapp/internal/domain"
	"github.com/babafem99/vfs/tradeapp/internal/provider"
	"github.com/babafem99/vfs/tradeapp/internal/providertest"
	"github.com/babafem99/vfs/tradeapp/internal/repository"
)

const user = 1

var ctx = context.Background()

func setup(t *testing.T) *Service {
	t.Helper()
	s, _ := setupWithBroker(t)
	return s
}

// setupWithBroker wires the service to a fake broker. Retries back off from
// 1s; tests move the clock forward with advance instead of sleeping.
func setupWithBroker(t *testing.T) (*Service, *providertest.Broker) {
	t.Helper()
	broker := providertest.New(t)
	cfg := DefaultConfig()
	cfg.JobMaxAttempts = 3
	s := New(
		repository.New(dbtest.New(t)),
		provider.New(broker.URL, providertest.APIKey, 2*time.Second, nil),
		cfg,
	)
	return s, broker
}

// advance moves the service's clock forward by d.
func (s *Service) advance(d time.Duration) {
	current := s.now()
	s.now = func() time.Time { return current.Add(d) }
}

// inTx runs fn in a transaction and fails the test on error.
func (s *Service) inTx(t *testing.T, fn func(tx pgx.Tx) error) {
	t.Helper()
	if err := s.repo.RunInTx(ctx, fn); err != nil {
		t.Fatal(err)
	}
}

func (s *Service) mustFund(t *testing.T, amount int64) {
	t.Helper()
	if _, err := s.FundAccount(ctx, user, amount); err != nil {
		t.Fatal(err)
	}
}

func (s *Service) cashNow(t *testing.T) domain.Cash {
	t.Helper()
	var c domain.Cash
	s.inTx(t, func(tx pgx.Tx) error {
		var err error
		c, err = s.getCash(ctx, tx, user)
		return err
	})
	return c
}

func (s *Service) balanceNow(t *testing.T, code string) int64 {
	t.Helper()
	var b int64
	s.inTx(t, func(tx pgx.Tx) error {
		var err error
		b, err = s.ledgerRepo.GetAccountBalance(ctx, tx, code)
		return err
	})
	return b
}

func (s *Service) positionNow(t *testing.T, symbol string) domain.Position {
	t.Helper()
	var p domain.Position
	s.inTx(t, func(tx pgx.Tx) error {
		var err error
		p, err = s.positionRepo.GetPosition(ctx, tx, user, symbol)
		return err
	})
	return p
}

func (s *Service) assertInvariants(t *testing.T) {
	t.Helper()
	dbtest.AssertInvariants(t, s.CheckInvariants)
}

// setNow freezes the service's clock at t.
func (s *Service) setNow(t time.Time) {
	s.now = func() time.Time { return t }
}
