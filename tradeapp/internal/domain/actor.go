package domain

import "context"

// Actor is who a request acts for. The transport puts it in the context
// (from auth; in this app, always the demo user); services that need it, like
// idempotency, read it from there.
type Actor struct {
	UserID int64
}

type actorKey struct{}

func WithActor(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, a)
}

func ActorFrom(ctx context.Context) (Actor, bool) {
	a, ok := ctx.Value(actorKey{}).(Actor)
	return a, ok
}
