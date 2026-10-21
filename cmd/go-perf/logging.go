package main

import (
	"fmt"
	"log/slog"
	"os"
)

// logger writes diagnostic logs to stderr so stdout stays usable for
// machine-readable subcommand output.
var logger = printfLogger{log: slog.New(slog.NewTextHandler(os.Stderr, nil))}

type printfLogger struct {
	log *slog.Logger
}

func (l printfLogger) Debugf(format string, args ...any) { l.log.Debug(fmt.Sprintf(format, args...)) }
func (l printfLogger) Infof(format string, args ...any)  { l.log.Info(fmt.Sprintf(format, args...)) }
func (l printfLogger) Warnf(format string, args ...any)  { l.log.Warn(fmt.Sprintf(format, args...)) }
func (l printfLogger) Errorf(format string, args ...any) { l.log.Error(fmt.Sprintf(format, args...)) }
