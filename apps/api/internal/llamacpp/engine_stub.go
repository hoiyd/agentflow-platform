//go:build !llamacpp || !cgo

package llamacpp

import (
	"context"
	"fmt"
)

type Engine struct{}

func Available() bool {
	return false
}

func New(Config) (*Engine, error) {
	return nil, ErrNotBuilt
}

func (*Engine) ModelDescription() string {
	return ""
}

func (*Engine) Generate(context.Context, string, int, TokenHandler) (Stats, error) {
	return Stats{}, ErrNotBuilt
}

func (*Engine) Close() error {
	return fmt.Errorf("close libllama engine: %w", ErrNotBuilt)
}
