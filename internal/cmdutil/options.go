package cmdutil

import "github.com/spf13/pflag"

type Options struct {
	LogLevel string
}

func (o *Options) BindFlags(f *pflag.FlagSet) {
	f.StringVar(&o.LogLevel, "log-level", "info", "Logging level (debug, info, warn, error)")
}
