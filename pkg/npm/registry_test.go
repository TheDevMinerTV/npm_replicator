package npm

import (
	"encoding/json"
	"testing"
)

func TestBinUnmarshalAndNormalize(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		pkgName  string
		want     map[string]string
		wantJSON string
	}{
		{
			name:     "bare string resolves to unscoped package name",
			input:    `"cli.js"`,
			pkgName:  "mytool",
			want:     map[string]string{"mytool": "cli.js"},
			wantJSON: `{"mytool":"cli.js"}`,
		},
		{
			name:     "bare string on scoped package strips scope",
			input:    `"./bin/run.js"`,
			pkgName:  "@acme/mytool",
			want:     map[string]string{"mytool": "./bin/run.js"},
			wantJSON: `{"mytool":"./bin/run.js"}`,
		},
		{
			name:     "object form is preserved and Normalize is a no-op",
			input:    `{"tsc":"./bin/tsc","tsserver":"./bin/tsserver"}`,
			pkgName:  "typescript",
			want:     map[string]string{"tsc": "./bin/tsc", "tsserver": "./bin/tsserver"},
			wantJSON: `{"tsc":"./bin/tsc","tsserver":"./bin/tsserver"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b Bin
			if err := json.Unmarshal([]byte(tt.input), &b); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			b.Normalize(tt.pkgName)

			if len(b) != len(tt.want) {
				t.Fatalf("Bin = %v, want %v", b, tt.want)
			}
			for k, v := range tt.want {
				if b[k] != v {
					t.Errorf("Bin[%q] = %q, want %q", k, b[k], v)
				}
			}

			out, err := json.Marshal(b)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(out) != tt.wantJSON {
				t.Errorf("marshaled = %s, want %s", out, tt.wantJSON)
			}
		})
	}
}

// A JSON null bin must decode to a nil map, not a placeholder entry.
func TestBinNull(t *testing.T) {
	var b Bin
	if err := json.Unmarshal([]byte(`null`), &b); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if b != nil {
		t.Fatalf("Bin = %v, want nil", b)
	}
}

// A missing bin must be omitted from the stored document, not serialized as null.
func TestVersionBinOmittedWhenAbsent(t *testing.T) {
	var v Version
	if err := json.Unmarshal([]byte(`{"name":"nobin","version":"1.0.0"}`), &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v.Bin != nil {
		t.Fatalf("Bin = %v, want nil", v.Bin)
	}

	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if contains(string(out), `"bin"`) {
		t.Errorf("marshaled version unexpectedly contains bin: %s", out)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
