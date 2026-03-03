// Copyright 2016 fatedier, fatedier@gmail.com
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package log

import (
	"io"
	"os"
	"strings"

	"github.com/fatedier/golib/log"
)

var Logger *log.Logger

func init() {
	Logger = log.New(
		log.WithCaller(true),
		log.AddCallerSkip(1),
		log.WithLevel(log.InfoLevel),
	)
}

// yamuxLogWriter implements io.Writer and forwards yamux logs to FRP's logger with [yamux] prefix.
type yamuxLogWriter struct{}

func (w *yamuxLogWriter) Write(p []byte) (n int, err error) {
	if len(p) > 0 {
		Debugf("[yamux] %s", strings.TrimSpace(string(p)))
	}
	return len(p), nil
}

// YamuxLogWriter returns an io.Writer that writes yamux logs to FRP's logger.
// Yamux logs are written at Debug level with [yamux] prefix. Set log.level = "debug" to see them.
func YamuxLogWriter() io.Writer {
	return &yamuxLogWriter{}
}

func InitLogger(logPath string, levelStr string, maxDays int, disableLogColor bool) {
	options := []log.Option{}
	if logPath == "console" {
		if !disableLogColor {
			options = append(options,
				log.WithOutput(log.NewConsoleWriter(log.ConsoleConfig{
					Colorful: true,
				}, os.Stdout)),
			)
		}
	} else {
		writer := log.NewRotateFileWriter(log.RotateFileConfig{
			FileName: logPath,
			Mode:     log.RotateFileModeDaily,
			MaxDays:  maxDays,
		})
		writer.Init()
		options = append(options, log.WithOutput(writer))
	}

	level, err := log.ParseLevel(levelStr)
	if err != nil {
		level = log.InfoLevel
	}
	options = append(options, log.WithLevel(level))
	Logger = Logger.WithOptions(options...)
}

func Errorf(format string, v ...interface{}) {
	Logger.Errorf(format, v...)
}

func Warnf(format string, v ...interface{}) {
	Logger.Warnf(format, v...)
}

func Infof(format string, v ...interface{}) {
	Logger.Infof(format, v...)
}

func Debugf(format string, v ...interface{}) {
	Logger.Debugf(format, v...)
}

func Tracef(format string, v ...interface{}) {
	Logger.Tracef(format, v...)
}
