package lid_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/babafem99/vfs/tradeapp/pkg/lid"
)

func TestNewIDsValidate(t *testing.T) {
	order, fund := lid.NewOrder(), lid.NewFunding()
	if !strings.HasPrefix(order, "order-") || len(order) != len("order-")+26 {
		t.Fatalf("order id = %q", order)
	}
	if err := lid.Validate(order, lid.Order); err != nil {
		t.Fatalf("order: %v", err)
	}
	if err := lid.Validate(fund, lid.Funding); err != nil {
		t.Fatalf("funding: %v", err)
	}
}

func TestIDsSortByCreation(t *testing.T) {
	prev := lid.NewOrder()
	for range 1000 {
		next := lid.NewOrder()
		if next <= prev {
			t.Fatalf("%s not after %s", next, prev)
		}
		prev = next
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]error{
		lid.NewFunding():                          lid.ErrInvalidPrefix, // wrong prefix
		"01JC3V8Q9W0000000000000000":              lid.ErrInvalidPrefix, // no prefix
		"order-not-a-ulid":                        lid.ErrInvalidID,
		"order-01JC3V8Q9W":                        lid.ErrInvalidID, // too short
		"order-01jc3v8q9w0000000000000000":        nil,              // lower case is valid Crockford base32
		"0f1e2d3c-4b5a-7968-8776-655443322110":    lid.ErrInvalidPrefix,
		"order-01JC3V8Q9W00000000000000000000000": lid.ErrInvalidID, // too long
	}
	for id, want := range cases {
		err := lid.Validate(id, lid.Order)
		if want == nil && err != nil {
			t.Errorf("%q: %v", id, err)
		}
		if want != nil && !errors.Is(err, want) {
			t.Errorf("%q: got %v, want %v", id, err, want)
		}
	}
}

func TestNormalize(t *testing.T) {
	id := lid.NewOrder()
	for _, in := range []string{id, strings.ToLower(id), strings.ToUpper(id)} {
		got, err := lid.Normalize(in, lid.Order)
		if err != nil || got != id {
			t.Errorf("Normalize(%q) = %q, %v; want %q", in, got, err, id)
		}
	}
	if _, err := lid.Normalize("fund-"+id[len("order-"):], lid.Order); !errors.Is(err, lid.ErrInvalidPrefix) {
		t.Errorf("wrong prefix: %v", err)
	}
	if _, err := lid.Normalize("order-nope", lid.Order); !errors.Is(err, lid.ErrInvalidID) {
		t.Errorf("bad ulid: %v", err)
	}
}
