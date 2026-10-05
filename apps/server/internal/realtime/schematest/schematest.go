// Package schematest checks WebSocket messages against the protocol
// contract in api/ws/*.schema.json, the source of truth of the realtime
// protocol (docs/00 §8). It is imported by tests only.
//
// It implements the subset of JSON Schema 2020-12 the contract uses and
// fails on any other keyword, so the schema cannot silently rely on a rule
// that is not checked. A full validator would be a new dependency
// (docs/00 §16); this is the ~200-line alternative.
package schematest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// Schema files of api/ws.
const (
	ServerMessage = "server-message.schema.json"
	ClientMessage = "client-message.schema.json"
	// WidgetMessage is what the widget's connection sends (W-01).
	WidgetMessage = "widget-message.schema.json"
)

// Schema is a loaded schema document.
type Schema struct {
	root map[string]any
}

// Load reads api/ws/<name>, found by walking up from the working directory.
func Load(t testing.TB, name string) *Schema {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		path := filepath.Join(dir, "api", "ws", name)
		if b, err := os.ReadFile(path); err == nil { //nolint:gosec // G304: a fixed name under the repository
			var root map[string]any
			if err := decode(b, &root); err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			return &Schema{root: root}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("api/ws/%s not found above the working directory", name)
		}
		dir = parent
	}
}

// Validate checks one JSON message and returns the first violation.
func (s *Schema) Validate(msg []byte) error {
	var v any
	if err := decode(msg, &v); err != nil {
		return fmt.Errorf("not JSON: %w", err)
	}
	return s.check(s.root, v, "$")
}

// Def returns a definition from $defs, for tests that compare enums.
func (s *Schema) Def(name string) map[string]any {
	defs, _ := s.root["$defs"].(map[string]any)
	d, _ := defs[name].(map[string]any)
	return d
}

func decode(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	return d.Decode(v)
}

// annotations carry no validation rule.
var annotations = map[string]bool{
	"$schema": true, "$id": true, "$defs": true, "$comment": true, "title": true, "description": true,
}

func (s *Schema) check(sch map[string]any, v any, path string) error {
	for _, k := range sortedKeys(sch) {
		if annotations[k] {
			continue
		}
		if err := s.keyword(k, sch[k], v, path); err != nil {
			return err
		}
	}
	return nil
}

func (s *Schema) keyword(k string, arg any, v any, path string) error {
	switch k {
	case "$ref":
		return s.check(s.resolve(arg.(string)), v, path)
	case "type":
		if !hasType(v, arg.(string)) {
			return fmt.Errorf("%s: want %s, got %T", path, arg, v)
		}
	case "const":
		if !equal(v, arg) {
			return fmt.Errorf("%s: want %v, got %v", path, arg, v)
		}
	case "enum":
		if !slices.ContainsFunc(arg.([]any), func(e any) bool { return equal(v, e) }) {
			return fmt.Errorf("%s: %v is not one of %v", path, v, arg)
		}
	case "required":
		obj, ok := v.(map[string]any)
		for _, name := range arg.([]any) {
			if _, has := obj[name.(string)]; ok && !has {
				return fmt.Errorf("%s: missing %q", path, name)
			}
		}
	case "properties":
		obj, _ := v.(map[string]any)
		props := arg.(map[string]any)
		for _, name := range sortedKeys(props) {
			if pv, ok := obj[name]; ok {
				if err := s.check(props[name].(map[string]any), pv, path+"."+name); err != nil {
					return err
				}
			}
		}
	case "allOf", "anyOf", "oneOf":
		return s.combine(k, arg.([]any), v, path)
	case "minimum":
		n, ok := v.(json.Number)
		if ok && compare(n, arg.(json.Number)) < 0 {
			return fmt.Errorf("%s: %v is below %v", path, n, arg)
		}
	case "minLength", "maxLength":
		str, ok := v.(string)
		limit, _ := arg.(json.Number).Int64()
		n := int64(len([]rune(str)))
		if ok && ((k == "minLength" && n < limit) || (k == "maxLength" && n > limit)) {
			return fmt.Errorf("%s: length %d violates %s %d", path, n, k, limit)
		}
	case "pattern":
		if str, ok := v.(string); ok && !regexp.MustCompile(arg.(string)).MatchString(str) {
			return fmt.Errorf("%s: %q does not match %s", path, str, arg)
		}
	case "format":
		return checkFormat(arg.(string), v, path)
	case "items":
		arr, _ := v.([]any)
		for i, item := range arr {
			if err := s.check(arg.(map[string]any), item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case "uniqueItems":
		arr, _ := v.([]any)
		for i := range arr {
			for j := range i {
				if arg.(bool) && equal(arr[i], arr[j]) {
					return fmt.Errorf("%s: items %d and %d are equal", path, j, i)
				}
			}
		}
	default:
		return fmt.Errorf("schematest: keyword %q is not supported; extend the validator or the schema", k)
	}
	return nil
}

func (s *Schema) combine(k string, subs []any, v any, path string) error {
	matched := 0
	var first error
	for _, sub := range subs {
		err := s.check(sub.(map[string]any), v, path)
		if err == nil {
			matched++
			continue
		}
		if k == "allOf" {
			return err
		}
		if first == nil {
			first = err
		}
	}
	switch {
	case k == "anyOf" && matched == 0:
		return fmt.Errorf("%s: matches none of anyOf: %w", path, first)
	case k == "oneOf" && matched != 1:
		return fmt.Errorf("%s: matches %d of oneOf, want exactly 1 (first mismatch: %w)", path, matched, first)
	}
	return nil
}

func (s *Schema) resolve(ref string) map[string]any {
	name, ok := strings.CutPrefix(ref, "#/$defs/")
	def := s.Def(name)
	if !ok || def == nil {
		panic(fmt.Sprintf("schematest: unresolvable $ref %q", ref))
	}
	return def
}

func hasType(v any, typ string) bool {
	switch typ {
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "null":
		return v == nil
	case "number":
		_, ok := v.(json.Number)
		return ok
	case "integer":
		n, ok := v.(json.Number)
		if !ok {
			return false
		}
		_, isInt := new(big.Int).SetString(n.String(), 10)
		return isInt
	}
	panic("schematest: unknown type " + typ)
}

func checkFormat(format string, v any, path string) error {
	str, ok := v.(string)
	if !ok {
		return nil
	}
	switch format {
	case "date-time":
		if _, err := time.Parse(time.RFC3339Nano, str); err != nil {
			return fmt.Errorf("%s: %q is not an RFC 3339 date-time", path, str)
		}
	case "uuid":
		// Checked by the pattern next to it.
	default:
		return fmt.Errorf("schematest: format %q is not supported", format)
	}
	return nil
}

func equal(a, b any) bool {
	an, aok := a.(json.Number)
	bn, bok := b.(json.Number)
	if aok && bok {
		return compare(an, bn) == 0
	}
	return a == b
}

func compare(a, b json.Number) int {
	x, _ := new(big.Float).SetString(a.String())
	y, _ := new(big.Float).SetString(b.String())
	return x.Cmp(y)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
