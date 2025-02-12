// Copyright 2020 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package main

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"text/template"

	"github.com/cockroachdb/cockroach/pkg/cli/exit"
	"github.com/cockroachdb/errors"
	"github.com/cockroachdb/gostdlib/go/format"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		exit.WithCode(exit.UnspecifiedError())
	}
}

func run() error {
	if len(os.Args) < 3 {
		return errors.Newf("usage: %s <proto> <template>\n", os.Args[0])
	}

	// Which template are we running?
	tmplName := os.Args[2]
	tmplSrc, ok := templates[tmplName]
	if !ok {
		return errors.Newf("unknown template: %q", tmplName)
	}
	tmpl, err := template.New(tmplName).Parse(tmplSrc)
	if err != nil {
		return errors.Wrapf(err, "%s", tmplName)
	}

	// Read the input .proto file.
	chans, sevs, err := readInput(os.Args[1])
	if err != nil {
		return err
	}

	// Render the template.
	var src bytes.Buffer
	if err := tmpl.Execute(&src, struct {
		Severities []info
		Channels   []info
	}{sevs, chans}); err != nil {
		return err
	}

	// If we are generating a .go file, do a pass of gofmt.
	newBytes := src.Bytes()
	if strings.HasSuffix(tmplName, ".go") {
		newBytes, err = format.Source(newBytes)
		if err != nil {
			return errors.Wrap(err, "gofmt")
		}
	}

	// Write the output file.
	w := os.Stdout
	if len(os.Args) > 3 {
		f, err := os.OpenFile(os.Args[3], os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0666)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		w = f
	}
	if _, err := w.Write(newBytes); err != nil {
		return err
	}

	return nil
}

type info struct {
	RawComment string
	Comment    string
	PComment   string
	Name       string
	NAME       string
	NameLower  string
}

func readInput(protoName string) (chans []info, sevs []info, err error) {
	protoData, err := os.ReadFile(protoName)
	if err != nil {
		return nil, nil, err
	}
	inSevs := false
	inChans := false
	rawComment := ""
	for _, line := range strings.Split(string(protoData), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		switch {
		case !inSevs && !inChans && strings.HasPrefix(line, "enum Severity"):
			inSevs = true
			continue
		case inSevs && !inChans && strings.HasPrefix(line, "}"):
			inSevs = false
			continue
		case !inSevs && !inChans && strings.HasPrefix(line, "enum Channel"):
			inChans = true
			continue
		case !inSevs && inChans && strings.HasPrefix(line, "}"):
			inChans = false
			continue
		}
		if !inSevs && !inChans {
			continue
		}

		if strings.HasPrefix(line, "//") {
			rawComment += line + "\n"
			continue
		}
		if strings.HasPrefix(line, "reserved") {
			rawComment = ""
			continue
		}
		key := strings.Split(line, " ")[0]
		title := strings.ReplaceAll(cases.Title(language.English, cases.NoLower).String(
			strings.ReplaceAll(strings.ToLower(key), "_", " ")), " ", "")
		if inSevs {
			comment := "// The `" + key + "` severity" + strings.TrimPrefix(rawComment, "// "+key)
			sevs = append(sevs, info{
				RawComment: rawComment,
				Comment:    comment,
				PComment:   strings.ReplaceAll(strings.ReplaceAll(comment, "// ", ""), "//", ""),
				Name:       title,
				NAME:       strings.ToUpper(key),
				NameLower:  strings.ToLower(key),
			})
		}
		if inChans && key != "CHANNEL_MAX" {
			comment := "// The `" + key + "` channel" + strings.TrimPrefix(rawComment, "// "+key)
			chans = append(chans, info{
				RawComment: rawComment,
				Comment:    comment,
				PComment:   strings.ReplaceAll(strings.ReplaceAll(comment, "// ", ""), "//", ""),
				Name:       title,
				NAME:       strings.ToUpper(key),
				NameLower:  strings.ToLower(key),
			})
		}
		rawComment = ""
	}

	return chans, sevs, nil
}

var templates = map[string]string{
	"logging.md": `## Logging levels (severities)
{{range .Severities}}{{if eq .NAME "NONE" "UNKNOWN" "DEFAULT"|not}}
### {{.NAME}}

{{.PComment}}
{{- end}}{{- end}}

## Logging channels
{{range .Channels}}
### ` + "`" + `{{.NAME}}` + "`" + `

{{.PComment}}
{{- end}}
`,

	"severity.go": `// Code generated by gen/main.go. DO NOT EDIT.

package severity

import "github.com/cockroachdb/cockroach/pkg/util/log/logpb"
{{range .Severities}}

{{ .RawComment -}}
const {{.NAME}} = logpb.Severity_{{.NAME}}
{{end}}
`,

	"channel.go": `// Code generated by gen/main.go. DO NOT EDIT.

package channel

import "github.com/cockroachdb/cockroach/pkg/util/log/logpb"

{{range .Channels}}

{{ .RawComment -}}
const {{.NAME}} = logpb.Channel_{{.NAME}}
{{end}}
`,

	"log_channels.go": `// Code generated by gen/main.go. DO NOT EDIT.

package log

import (
  "context"

  "github.com/cockroachdb/cockroach/pkg/util/log/channel"
  "github.com/cockroachdb/cockroach/pkg/util/log/severity"
)

// ChannelLogger is a helper interface to ease the run-time selection
// of channels. We do not force use of ChannelLogger when
// instantiating the logger objects below (e.g. by giving them the
// interface type), to ensure the calls remain inlinable in the common
// case.
//
// Note that casting a channel logger to the interface
// type yields a heap allocation: it may be useful for performance to
// pre-allocate interface references in the global scope.
type ChannelLogger interface {
  {{range .Severities}}{{if eq .NAME "NONE" "UNKNOWN" "DEFAULT"|not -}}
  // {{.Name}}f logs to the channel with severity {{.NAME}}.
  // It extracts log tags from the context and logs them along with the given
  // message. Arguments are handled in the manner of fmt.Printf.
  {{.Name}}f(ctx context.Context, format string, args ...interface{})

  // V{{.Name}}f logs to the channel with severity {{.NAME}},
  // if logging has been enabled for the source file where the call is
  // performed at the provided verbosity level, via the vmodule setting.
  // It extracts log tags from the context and logs them along with the given
  // message. Arguments are handled in the manner of fmt.Printf.
  V{{.Name}}f(ctx context.Context, level Level, format string, args ...interface{})

  // {{.Name}} logs to the channel with severity {{.NAME}}.
  // It extracts log tags from the context and logs them along with the given
  // message.
  {{.Name}}(ctx context.Context, msg string)

  // {{.Name}}fDepth logs to the channel with severity {{.NAME}},
  // offsetting the caller's stack frame by 'depth'.
  // It extracts log tags from the context and logs them along with the given
  // message. Arguments are handled in the manner of fmt.Printf.
  {{.Name}}fDepth(ctx context.Context, depth int, format string, args ...interface{})

  {{end}}{{end}}{{- /* end range severities */ -}}

  // Shout logs to the channel, and also to the real stderr if logging
  // is currently redirected to a file.
  Shout(ctx context.Context, sev Severity, msg string)

  // Shoutf logs to the channel, and also to the real stderr if
  // logging is currently redirected to a file. Arguments are handled in
  // the manner of fmt.Printf.
  Shoutf(ctx context.Context, sev Severity, format string, args ...interface{})
}

{{$sevs := .Severities}}
{{range $unused, $chan := .Channels}}
// logger{{.Name}} is the logger type for the {{.NAME}} channel.
type logger{{.Name}} struct{}

// {{.Name}} is a logger that logs to the {{.NAME}} channel.
//
{{.Comment -}}
var {{.Name}} logger{{.Name}}

// {{.Name}} and logger{{.Name}} implement ChannelLogger.
//
// We do not force use of ChannelLogger when instantiating the logger
// object above (e.g. by giving it the interface type), to ensure
// the calls to the API methods remain inlinable in the common case.
var _ ChannelLogger = {{.Name}}

{{range $sevi, $sev := $sevs}}{{if eq .NAME "NONE" "UNKNOWN" "DEFAULT"|not}}{{with $chan}}
// {{with $sev}}{{.Name}}{{end}}f logs to the {{.NAME}} channel with severity {{with $sev}}{{.NAME}}{{end}}.
// It extracts log tags from the context and logs them along with the given
// message. Arguments are handled in the manner of fmt.Printf.
//
{{.Comment -}}
//
{{with $sev}}{{.Comment}}{{end -}}
func (logger{{.Name}}) {{with $sev}}{{.Name}}{{end}}f(ctx context.Context, format string, args ...interface{}) {
  logfDepth(ctx, 1, severity.{{with $sev}}{{.NAME}}{{end}}, channel.{{.NAME}}, format, args...)
}

// V{{with $sev}}{{.Name}}{{end}}f logs to the {{.NAME}} channel with severity {{with $sev}}{{.NAME}}{{end}},
// if logging has been enabled for the source file where the call is
// performed at the provided verbosity level, via the vmodule setting.
// It extracts log tags from the context and logs them along with the given
// message. Arguments are handled in the manner of fmt.Printf.
//
{{.Comment -}}
//
{{with $sev}}{{.Comment}}{{end -}}
func (logger{{.Name}}) V{{with $sev}}{{.Name}}{{end}}f(ctx context.Context, level Level, format string, args ...interface{}) {
  if VDepth(level, 1) {
    logfDepth(ctx, 1, severity.{{with $sev}}{{.NAME}}{{end}}, channel.{{.NAME}}, format, args...)
  }
}

// {{with $sev}}{{.Name}}{{end}} logs to the {{.NAME}} channel with severity {{with $sev}}{{.NAME}}{{end}}.
// It extracts log tags from the context and logs them along with the given
// message.
//
{{.Comment -}}
//
{{with $sev}}{{.Comment}}{{end -}}
func (logger{{.Name}}) {{with $sev}}{{.Name}}{{end}}(ctx context.Context, msg string) {
  logfDepth(ctx, 1, severity.{{with $sev}}{{.NAME}}{{end}}, channel.{{.NAME}}, msg)
}

// {{with $sev}}{{.Name}}{{end}}fDepth logs to the {{.NAME}} channel with severity {{with $sev}}{{.NAME}}{{end}},
// offsetting the caller's stack frame by 'depth'.
// It extracts log tags from the context and logs them along with the given
// message. Arguments are handled in the manner of fmt.Printf.
//
{{.Comment -}}
//
{{with $sev}}{{.Comment}}{{end -}}
func (logger{{.Name}}) {{with $sev}}{{.Name}}{{end}}fDepth(ctx context.Context, depth int, format string, args ...interface{}) {
  logfDepth(ctx, depth+1, severity.{{with $sev}}{{.NAME}}{{end}}, channel.{{.NAME}}, format, args...)
}

{{if .NAME|eq "DEV"}}
// {{with $sev}}{{.Name}}{{end}}f logs to the {{.NAME}} channel with severity {{with $sev}}{{.NAME}}{{end}},
// if logging has been enabled for the source file where the call is
// performed at the provided verbosity level, via the vmodule setting.
// It extracts log tags from the context and logs them along with the given
// message. Arguments are handled in the manner of fmt.Printf.
//
{{.Comment -}}
//
{{with $sev}}{{.Comment}}{{end -}}
func {{with $sev}}{{.Name}}{{end}}f(ctx context.Context, format string, args ...interface{}) {
  logfDepth(ctx, 1, severity.{{with $sev}}{{.NAME}}{{end}}, channel.{{.NAME}}, format, args...)
}

// V{{with $sev}}{{.Name}}{{end}}f logs to the {{.NAME}} channel with severity {{with $sev}}{{.NAME}}{{end}}.
// It extracts log tags from the context and logs them along with the given
// message. Arguments are handled in the manner of fmt.Printf.
//
{{.Comment -}}
//
{{with $sev}}{{.Comment}}{{end -}}
func V{{with $sev}}{{.Name}}{{end}}f(ctx context.Context, level Level, format string, args ...interface{}) {
  if VDepth(level, 1) {
    logfDepth(ctx, 1, severity.{{with $sev}}{{.NAME}}{{end}}, channel.{{.NAME}}, format, args...)
  }
}

// {{with $sev}}{{.Name}}{{end}} logs to the {{.NAME}} channel with severity {{with $sev}}{{.NAME}}{{end}}.
// It extracts log tags from the context and logs them along with the given
// message.
//
{{.Comment -}}
//
{{with $sev}}{{.Comment}}{{end -}}
func {{with $sev}}{{.Name}}{{end}}(ctx context.Context, msg string) {
  logfDepth(ctx, 1, severity.{{with $sev}}{{.NAME}}{{end}}, channel.{{.NAME}}, msg)
}

// {{with $sev}}{{.Name}}{{end}}fDepth logs to the {{.NAME}} channel with severity {{with $sev}}{{.NAME}}{{end}},
// offsetting the caller's stack frame by 'depth'.
// It extracts log tags from the context and logs them along with the given
// message. Arguments are handled in the manner of fmt.Printf.
//
{{.Comment -}}
//
{{with $sev}}{{.Comment}}{{end -}}
func {{with $sev}}{{.Name}}{{end}}fDepth(ctx context.Context, depth int, format string, args ...interface{}) {
  logfDepth(ctx, depth+1, severity.{{with $sev}}{{.NAME}}{{end}}, channel.{{.NAME}}, format, args...)
}
{{end}}{{- /* end channel name = DEV */ -}}

{{end}}{{end}}{{end}}{{- /* end range severities */ -}}

// Shout logs to channel {{.NAME}}, and also to the real stderr if logging
// is currently redirected to a file.
//
{{.Comment -}}
func (logger{{.Name}}) Shout(ctx context.Context, sev Severity, msg string) {
  shoutfDepth(ctx, 1, sev, channel.{{.NAME}}, msg)
}

// Shoutf logs to channel {{.NAME}}, and also to the real stderr if
// logging is currently redirected to a file. Arguments are handled in
// the manner of fmt.Printf.
//
{{.Comment -}}
func (logger{{.Name}}) Shoutf(ctx context.Context, sev Severity, format string, args ...interface{}) {
  shoutfDepth(ctx, 1, sev, channel.{{.NAME}}, format, args...)
}

{{if .NAME|eq "DEV"}}

// Shout logs to channel {{.NAME}}, and also to the real stderr if logging
// is currently redirected to a file.
//
{{.Comment -}}
func Shout(ctx context.Context, sev Severity, msg string) {
  shoutfDepth(ctx, 1, sev, channel.{{.NAME}}, msg)
}

// Shoutf logs to channel {{.NAME}}, and also to the real stderr if
// logging is currently redirected to a file. Arguments are handled in
// the manner of fmt.Printf.
//
{{.Comment -}}
func Shoutf(ctx context.Context, sev Severity, format string, args ...interface{}) {
  shoutfDepth(ctx, 1, sev, channel.{{.NAME}}, format, args...)
}

{{end}}{{- /* end channel name = DEV */ -}}

{{end}}{{- /* end range channels */ -}}
`,
}
