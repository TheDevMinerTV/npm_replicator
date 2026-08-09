package npm

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

// Exports is the package.json "exports" field, normalized to its full subpath
// form: a map of subpath ("." or "./something") to condition target. npm
// accepts several sugared spellings that all mean the same thing —
//
//	"exports": "./index.js"
//	"exports": ["./index.js", "./fallback.js"]
//	"exports": { "import": "./index.mjs", "require": "./index.cjs" }
//
// all describe the "." subpath — so they are lifted into that form on decode
// and consumers only ever see one shape.
//
// Targets stay as raw JSON because a target is legally a string, an array of
// targets, a nested condition object or null (a blocked subpath). Their
// contents are still normalized: see normalizeExportTarget.
type Exports map[string]json.RawMessage

func (e *Exports) UnmarshalJSON(data []byte) error {
	if isJSONNull(data) {
		return nil
	}

	{
		// try decoding as an object, which is either a subpath map already or
		// the condition-only sugar for "."
		var t map[string]json.RawMessage
		if err := json.Unmarshal(data, &t); err != nil {
			var jsonErr *json.UnmarshalTypeError
			if !errors.As(err, &jsonErr) {
				return err
			}
		} else {
			if len(t) == 0 {
				// "exports": {} blocks every subpath, same as having none
				return nil
			}

			if !hasSubpathKey(t) {
				*e = Exports{".": json.RawMessage(bytes.Clone(data))}
				return nil
			}

			normalized := make(Exports, len(t))
			for subpath, target := range t {
				normalized[normalizeSubpath(subpath)] = target
			}

			*e = normalized

			return nil
		}
	}

	// a bare string or a fallback array: sugar for the "." subpath
	*e = Exports{".": json.RawMessage(bytes.Clone(data))}

	return nil
}

// Normalize rewrites every target into a canonical, compact form: paths get a
// leading "./", object keys end up in a deterministic order and insignificant
// whitespace is dropped. Two packages that declare the same exports therefore
// store byte-identical documents.
func (e Exports) Normalize() {
	for subpath, target := range e {
		e[subpath] = normalizeExportTarget(target)
	}
}

// hasSubpathKey reports whether the object is keyed by subpaths rather than by
// conditions. The spec requires all keys to be one or the other; when a broken
// package mixes them we favour reading it as subpaths, which keeps more of the
// document intact.
func hasSubpathKey(obj map[string]json.RawMessage) bool {
	for key := range obj {
		if strings.HasPrefix(strings.TrimSpace(key), ".") {
			return true
		}
	}

	return false
}

// normalizeSubpath canonicalizes an exports key. Unlike a target path, "." is
// itself valid here and must not grow a prefix.
func normalizeSubpath(subpath string) string {
	subpath = strings.ReplaceAll(strings.TrimSpace(subpath), "\\", "/")
	if subpath == "" || subpath == "." || strings.HasPrefix(subpath, "./") {
		return subpath
	}

	return normalizePath(subpath)
}

// normalizeExportTarget walks a target and normalizes every path string in it.
// Re-marshalling also sorts object keys and strips whitespace. A target that
// cannot be parsed is passed through untouched rather than dropped.
func normalizeExportTarget(target json.RawMessage) json.RawMessage {
	if len(target) == 0 {
		return target
	}

	decoder := json.NewDecoder(bytes.NewReader(target))
	decoder.UseNumber()

	var node any
	if err := decoder.Decode(&node); err != nil {
		return target
	}

	normalized, err := json.Marshal(normalizeExportNode(node))
	if err != nil {
		return target
	}

	return normalized
}

func normalizeExportNode(node any) any {
	switch n := node.(type) {
	case string:
		return normalizePath(n)

	case map[string]any:
		for key, value := range n {
			n[key] = normalizeExportNode(value)
		}

		return n

	case []any:
		for i, value := range n {
			n[i] = normalizeExportNode(value)
		}

		return n

	default:
		// null, numbers, booleans: nothing to normalize, keep as-is
		return node
	}
}

func isJSONNull(data []byte) bool {
	trimmed := bytes.TrimSpace(data)

	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}
