package app

import (
	"context"
	"errors"
	"testing"
)

func TestGroupFirstErrorCancelsOthers(t *testing.T) {
	g, _ := NewGroup(context.Background())
	boom := errors.New("boom")
	g.Go(func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })
	g.Go(func(ctx context.Context) error { return boom })
	if err := g.Wait(); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}

func TestGroupParentCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	g, _ := NewGroup(ctx)
	g.Go(func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })
	cancel()
	if err := g.Wait(); err != nil {
		t.Fatalf("err = %v", err)
	}
}
