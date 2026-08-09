package npm

import (
	"encoding/json"
	"testing"
)

func TestDetectPackageType(t *testing.T) {
	tests := []struct {
		name string
		json string
		want PackageType
	}{
		{
			name: "no version at all",
			json: `{}`,
			want: PackageTypeUnknown,
		},
		{
			name: "plain commonjs",
			json: `{"version":"1.0.0","main":"./index.js"}`,
			want: PackageTypeCJS,
		},
		{
			name: "type module without exports",
			json: `{"version":"1.0.0","type":"module"}`,
			want: PackageTypeESM,
		},
		{
			name: "type module with string exports",
			json: `{"version":"6.0.0","type":"module","exports":"./source/index.js"}`,
			want: PackageTypeESM,
		},
		{
			name: "type module with types and default conditions only",
			json: `{"version":"6.0.0","type":"module","exports":{"types":"./index.d.ts","default":"./index.js"}}`,
			want: PackageTypeESM,
		},
		{
			name: "esm first dual",
			json: `{"version":"1.0.0","type":"module","exports":{".":{"import":"./index.mjs","require":"./index.cjs"}}}`,
			want: PackageTypeDual,
		},
		{
			name: "commonjs first dual via exports",
			json: `{"version":"1.0.0","type":"commonjs","main":"./index.js","exports":{".":{"require":"./index.js","import":"./index.mjs"}}}`,
			want: PackageTypeDual,
		},
		{
			name: "commonjs first dual via legacy module field",
			json: `{"version":"1.0.0","main":"./index.js","module":"./index.esm.js"}`,
			want: PackageTypeDual,
		},
		{
			name: "condition nested under platform key",
			json: `{"version":"1.0.0","exports":{".":{"node":{"import":"./node.mjs","require":"./node.cjs"}}}}`,
			want: PackageTypeDual,
		},
		{
			name: "condition inside fallback array",
			json: `{"version":"1.0.0","exports":{".":[{"import":"./a.mjs"},"./b.js"]}}`,
			want: PackageTypeDual,
		},
		{
			name: "exports without any module condition",
			json: `{"version":"1.0.0","exports":{".":"./index.js","./pkg":"./pkg.js"}}`,
			want: PackageTypeCJS,
		},
		{
			name: "null exports",
			json: `{"version":"1.0.0","exports":null}`,
			want: PackageTypeCJS,
		},
		{
			name: "subpath named like a condition is not a condition",
			json: `{"version":"1.0.0","exports":{".":"./index.js","./require":"./require.js","./import":"./import.js"}}`,
			want: PackageTypeCJS,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var v Version
			if err := json.Unmarshal([]byte(test.json), &v); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}

			if got := v.DetectPackageType(); got != test.want {
				t.Errorf("DetectPackageType() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestLooseString(t *testing.T) {
	tests := []struct {
		name       string
		json       string
		wantType   LooseString
		wantModule LooseString
	}{
		{
			name:     "plain strings",
			json:     `{"version":"1.0.0","type":"module","module":"./index.mjs"}`,
			wantType: "module", wantModule: "./index.mjs",
		},
		{
			name:       "array module keeps first entry",
			json:       `{"version":"1.0.0","module":["./index.mjs","./other.mjs"]}`,
			wantModule: "./index.mjs",
		},
		{
			name: "empty array module",
			json: `{"version":"1.0.0","module":[]}`,
		},
		{
			name: "null module",
			json: `{"version":"1.0.0","module":null}`,
		},
		{
			name: "numeric module is dropped, not an error",
			json: `{"version":"1.0.0","module":0}`,
		},
		{
			name: "object type is dropped, not an error",
			json: `{"version":"1.0.0","type":{"nope":true}}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var v Version
			if err := json.Unmarshal([]byte(test.json), &v); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}

			if v.Type != test.wantType {
				t.Errorf("Type = %q, want %q", v.Type, test.wantType)
			}
			if v.Module != test.wantModule {
				t.Errorf("Module = %q, want %q", v.Module, test.wantModule)
			}
		})
	}
}

func TestPaths(t *testing.T) {
	tests := []struct {
		name string
		json string
		want Paths
	}{
		{
			name: "plain string becomes a one-entry slice",
			json: `{"version":"1.0.0","main":"index.js"}`,
			want: Paths{"./index.js"},
		},
		{
			name: "array keeps every entry",
			json: `{"version":"1.0.0","main":["index.js","./other.js","lib\\third.js"]}`,
			want: Paths{"./index.js", "./other.js", "./lib/third.js"},
		},
		{
			name: "entries that normalize onto each other are deduped",
			json: `{"version":"1.0.0","main":["index.js","./index.js",".\\index.js"]}`,
			want: Paths{"./index.js"},
		},
		{
			name: "non-string entries are skipped, the rest survives",
			json: `{"version":"1.0.0","main":["index.js",null,7,{"a":1},"other.js"]}`,
			want: Paths{"./index.js", "./other.js"},
		},
		{
			name: "empty array",
			json: `{"version":"1.0.0","main":[]}`,
			want: nil,
		},
		{
			name: "empty string",
			json: `{"version":"1.0.0","main":""}`,
			want: nil,
		},
		{
			name: "null is not an error",
			json: `{"version":"1.0.0","main":null}`,
			want: nil,
		},
		{
			name: "number is not an error",
			json: `{"version":"1.0.0","main":0}`,
			want: nil,
		},
		{
			name: "object is not an error",
			json: `{"version":"1.0.0","main":{"nope":true}}`,
			want: nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var v Version
			if err := json.Unmarshal([]byte(test.json), &v); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}

			v.Normalize("some-package")

			if len(v.Main) != len(test.want) {
				t.Fatalf("Main = %q, want %q", v.Main, test.want)
			}
			for i := range test.want {
				if v.Main[i] != test.want[i] {
					t.Fatalf("Main = %q, want %q", v.Main, test.want)
				}
			}
		})
	}
}

func TestVersionModuleFieldsOmittedWhenAbsent(t *testing.T) {
	var v Version
	if err := json.Unmarshal([]byte(`{"name":"x","version":"1.0.0"}`), &v); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	for _, field := range []string{`"type"`, `"main"`, `"module"`, `"exports"`} {
		if contains(string(out), field) {
			t.Errorf("expected %s to be omitted, got %s", field, out)
		}
	}
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		name        string
		json        string
		wantType    LooseString
		wantModule  LooseString
		wantExports string
	}{
		{
			name:     "absent type becomes the npm default",
			json:     `{"version":"1.0.0"}`,
			wantType: "commonjs",
		},
		{
			name:     "type is lower-cased",
			json:     `{"version":"1.0.0","type":"Module"}`,
			wantType: "module",
		},
		{
			name:     "unknown type is kept",
			json:     `{"version":"1.0.0","type":"esnext"}`,
			wantType: "esnext",
		},
		{
			name:     "bare paths gain a ./ prefix",
			json:     `{"version":"1.0.0","module":"dist/index.mjs"}`,
			wantType: "commonjs", wantModule: "./dist/index.mjs",
		},
		{
			name:     "backslashes become slashes",
			json:     `{"version":"1.0.0","module":"lib\\index.mjs"}`,
			wantType: "commonjs", wantModule: "./lib/index.mjs",
		},
		{
			name:     "already-relative paths are untouched",
			json:     `{"version":"1.0.0","module":"./index.mjs"}`,
			wantType: "commonjs", wantModule: "./index.mjs",
		},
		{
			name:        "string exports is lifted to the root subpath",
			json:        `{"version":"1.0.0","exports":"index.js"}`,
			wantType:    "commonjs",
			wantExports: `{".":"./index.js"}`,
		},
		{
			name:        "fallback array exports is lifted to the root subpath",
			json:        `{"version":"1.0.0","exports":["a.js","./b.js"]}`,
			wantType:    "commonjs",
			wantExports: `{".":["./a.js","./b.js"]}`,
		},
		{
			name:        "condition-only exports is lifted to the root subpath",
			json:        `{"version":"1.0.0","exports":{"require":"./i.cjs","import":"./i.mjs"}}`,
			wantType:    "commonjs",
			wantExports: `{".":{"import":"./i.mjs","require":"./i.cjs"}}`,
		},
		{
			name:        "subpath exports keeps its keys and sorts conditions",
			json:        `{"version":"1.0.0","exports":{"./sub":"sub.js",".":{"require":"i.cjs","import":"i.mjs"}}}`,
			wantType:    "commonjs",
			wantExports: `{".":{"import":"./i.mjs","require":"./i.cjs"},"./sub":"./sub.js"}`,
		},
		{
			name:        "blocked subpath survives",
			json:        `{"version":"1.0.0","exports":{".":"./i.js","./private":null}}`,
			wantType:    "commonjs",
			wantExports: `{".":"./i.js","./private":null}`,
		},
		{
			name:        "empty exports object is dropped",
			json:        `{"version":"1.0.0","exports":{}}`,
			wantType:    "commonjs",
			wantExports: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var v Version
			if err := json.Unmarshal([]byte(test.json), &v); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}

			v.Normalize("some-package")

			if v.Type != test.wantType {
				t.Errorf("Type = %q, want %q", v.Type, test.wantType)
			}
			if v.Module != test.wantModule {
				t.Errorf("Module = %q, want %q", v.Module, test.wantModule)
			}

			gotExports := ""
			if len(v.Exports) > 0 {
				out, err := json.Marshal(v.Exports)
				if err != nil {
					t.Fatalf("marshal exports failed: %v", err)
				}
				gotExports = string(out)
			}

			if gotExports != test.wantExports {
				t.Errorf("Exports = %s, want %s", gotExports, test.wantExports)
			}
		})
	}
}

func TestNormalizeIsStableAcrossSpellings(t *testing.T) {
	spellings := []string{
		`{"version":"1.0.0","type":"module","exports":{"import":"index.mjs","require":"./index.cjs"}}`,
		`{"version":"1.0.0","type":"Module","exports":{".":{"require":"index.cjs","import":"index.mjs"}}}`,
		`{"version":"1.0.0","type":"module","exports":{".":{"import":".\\index.mjs","require":".\\index.cjs"}}}`,
	}

	var want string
	for i, spelling := range spellings {
		var v Version
		if err := json.Unmarshal([]byte(spelling), &v); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}

		v.Normalize("some-package")

		out, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal failed: %v", err)
		}

		if i == 0 {
			want = string(out)
			continue
		}

		if string(out) != want {
			t.Errorf("spelling %d normalized to\n  %s\nwant\n  %s", i, out, want)
		}
	}
}

func TestNormalizeSkipsZeroVersion(t *testing.T) {
	// a package without a usable "latest" tag decodes to a zero version; it
	// must not come out claiming to be a commonjs package
	var v Version

	v.Normalize("some-package")

	if v.Type != "" {
		t.Errorf("Type = %q, want empty", v.Type)
	}

	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	if contains(string(out), `"type"`) {
		t.Errorf("expected type to be omitted, got %s", out)
	}
}
