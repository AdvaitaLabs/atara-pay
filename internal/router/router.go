// Package router selects which Adapter handles a given request.
//
// The MVP implementation is "explicit + default": callers pass a Rail in the
// request (or as a ?rail= query param); if absent, the configured DefaultRail
// is used. Smart routing (cheapest/fastest quote) is added once a second rail
// lands and we have real cost data.
package router

import (
	"fmt"

	"github.com/atara-xyz/atara-pay/internal/adapters"
	paygwerr "github.com/atara-xyz/atara-pay/internal/errors"
	"github.com/atara-xyz/atara-pay/internal/types"
)

type Router struct {
	adapters    map[types.Rail]adapters.Adapter
	defaultRail types.Rail
}

func New(defaultRail types.Rail, list ...adapters.Adapter) *Router {
	m := make(map[types.Rail]adapters.Adapter, len(list))
	for _, a := range list {
		m[a.Rail()] = a
	}
	return &Router{adapters: m, defaultRail: defaultRail}
}

// Pick returns the adapter for the given rail. Empty rail falls back to the
// configured default.
func (r *Router) Pick(rail types.Rail) (adapters.Adapter, error) {
	if rail == "" {
		rail = r.defaultRail
	}
	a, ok := r.adapters[rail]
	if !ok {
		return nil, fmt.Errorf("%w: rail %q is not configured", paygwerr.ErrBadRequest, rail)
	}
	return a, nil
}

// Registered lists every rail this router knows about.
func (r *Router) Registered() []types.Rail {
	out := make([]types.Rail, 0, len(r.adapters))
	for k := range r.adapters {
		out = append(out, k)
	}
	return out
}
