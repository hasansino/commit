package cmdutil

import (
	"context"

	"github.com/spf13/pflag"
)

type Factory struct {
	ctx     context.Context
	options *Options
}

func NewFactory(ctx context.Context) *Factory {
	f := &Factory{
		ctx:     ctx,
		options: new(Options),
	}
	return f
}

func (f *Factory) Context() context.Context {
	return f.ctx
}

func (f *Factory) Options() *Options {
	return f.options
}

func (f *Factory) BindFlags(flags *pflag.FlagSet) {
	f.options.BindFlags(flags)
}
