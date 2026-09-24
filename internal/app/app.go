// Package app provides the common process lifecycle for CMS services:
// signal handling, running concurrent components and orderly shutdown.
package app

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// SignalContext returns a context cancelled on SIGINT or SIGTERM.
func SignalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// Group runs functions concurrently; the first error (or the parent
// context ending) cancels the rest. Wait returns the first non-nil error,
// ignoring context.Canceled.
type Group struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	once   sync.Once
	err    error
}

// NewGroup creates a group bound to ctx.
func NewGroup(ctx context.Context) (*Group, context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	return &Group{ctx: ctx, cancel: cancel}, ctx
}

// Go starts fn in a goroutine.
func (g *Group) Go(fn func(ctx context.Context) error) {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		if err := fn(g.ctx); err != nil && !errors.Is(err, context.Canceled) {
			g.once.Do(func() { g.err = err })
			g.cancel()
		}
	}()
}

// Wait blocks until every function returned.
func (g *Group) Wait() error {
	g.wg.Wait()
	g.cancel()
	return g.err
}
