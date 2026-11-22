// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dao

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/globlit/k9s/internal/config"
)

// RenderOpts controls presentation without changing the retained source logs.
type RenderOpts struct {
	ShowTime, JSON bool
	Fields         []string
	Palette        config.JSONLog
}

// parsedJSON returns an immutable object, or nil for a non-JSON log item.
func (l *LogItem) parsedJSON() map[string]any {
	l.jsonOnce.Do(func() {
		payload := bytes.TrimSpace(l.Bytes)
		if len(payload) == 0 || l.IsError {
			return
		}
		if payload[0] != '{' {
			if i := bytes.IndexByte(payload, ' '); i > 0 {
				payload = bytes.TrimSpace(payload[i+1:])
			}
		}
		if len(payload) == 0 || payload[0] != '{' {
			return
		}
		dec := json.NewDecoder(bytes.NewReader(payload))
		dec.UseNumber()
		var fields map[string]any
		if dec.Decode(&fields) != nil {
			return
		}
		var extra any
		if dec.Decode(&extra) != io.EOF {
			return
		}
		l.jsonFields = fields
		for k := range fields {
			l.jsonKeys = append(l.jsonKeys, k)
		}
		slices.SortFunc(l.jsonKeys, func(a, b string) int {
			if x, y := logFieldRank(a), logFieldRank(b); x != y {
				return x - y
			}
			return strings.Compare(a, b)
		})
	})
	return l.jsonFields
}

func logFieldRank(key string) int {
	switch key {
	case "ts":
		return 0
	case "time":
		return 1
	case "@timestamp":
		return 2
	case "level":
		return 3
	case "lvl":
		return 4
	case "severity":
		return 5
	case "msg":
		return 6
	case "message":
		return 7
	default:
		return 8
	}
}

func (l *LogItem) renderJSON(opts RenderOpts, out *bytes.Buffer) bool {
	fields := l.parsedJSON()
	if fields == nil {
		return false
	}
	palette := opts.Palette
	if palette == (config.JSONLog{}) {
		palette = config.NewJSONLog()
	}
	shown := 0
	for _, key := range l.jsonKeys {
		if len(opts.Fields) > 0 && !slices.Contains(opts.Fields, key) {
			continue
		}
		if shown > 0 {
			out.WriteByte(' ')
		}
		shown++
		// Quote controls, but omit the surrounding quotes on field names.
		name := strconv.Quote(key)
		out.WriteString("[" + string(palette.Key) + "::]" + escapeLogText(name[1:len(name)-1]) + "=[-::-]")
		value := fields[key]
		color := palette.String
		switch value.(type) {
		case json.Number:
			color = palette.Number
		case bool:
			color = palette.Bool
		case nil:
			color = palette.Null
		}
		if key == "level" || key == "lvl" || key == "severity" {
			if level, ok := value.(string); ok {
				switch strings.ToLower(level) {
				case "error", "fatal", "panic":
					color = palette.LevelError
				case "warn", "warning":
					color = palette.LevelWarn
				case "info", "information":
					color = palette.LevelInfo
				case "debug", "trace":
					color = palette.LevelDebug
				}
			}
		}
		encoded, _ := json.Marshal(value)
		out.WriteString("[" + string(color) + "::]" + escapeLogText(string(encoded)) + "[-::-]")
	}
	if hidden := len(fields) - shown; hidden > 0 {
		if shown > 0 {
			out.WriteByte(' ')
		}
		fmt.Fprintf(out, "[%s::d]…+%d[-::-]", palette.Key, hidden)
	} else if len(fields) == 0 {
		out.WriteString("{}")
	}
	if bytes.HasSuffix(l.Bytes, []byte{'\n'}) {
		out.WriteByte('\n')
	}
	return true
}

type jsonPredicate struct {
	key, value string
	rx         *regexp.Regexp
}

// parseJSONSelector uses CSV quoting, so a predicate with commas can be written
// as "message~timeout, retry". Predicates are ANDed and keys are top-level keys.
func parseJSONSelector(query string) ([]jsonPredicate, error) {
	reader := csv.NewReader(strings.NewReader(query))
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = -1
	fields, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("JSON filter: expected key=value or key~regex: %w", err)
	}
	if _, err := reader.Read(); err != io.EOF {
		return nil, fmt.Errorf("JSON filter: expected one selector line")
	}
	predicates := make([]jsonPredicate, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		i := strings.IndexAny(field, "=~")
		if i <= 0 {
			return nil, fmt.Errorf("JSON filter: expected key=value or key~regex in %q", field)
		}
		p := jsonPredicate{key: strings.TrimSpace(field[:i]), value: field[i+1:]}
		if p.key == "" {
			return nil, fmt.Errorf("JSON filter: empty key")
		}
		if field[i] == '~' {
			p.rx, err = regexp.Compile(p.value)
			if err != nil {
				return nil, fmt.Errorf("JSON filter: %w", err)
			}
		}
		predicates = append(predicates, p)
	}
	return predicates, nil
}

func matchesJSON(fields map[string]any, predicates []jsonPredicate) bool {
	for _, p := range predicates {
		value, exists := fields[p.key]
		if !exists {
			return false
		}
		text, ok := value.(string)
		if !ok {
			encoded, _ := json.Marshal(value)
			text = string(encoded)
		}
		if p.rx != nil {
			if !p.rx.MatchString(text) {
				return false
			}
		} else if text != p.value {
			return false
		}
	}
	return true
}
