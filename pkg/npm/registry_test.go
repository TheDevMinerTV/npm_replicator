package npm

import (
	"encoding/json"
	"testing"
)

func TestBinUnmarshalRoundTrip(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantJSON string
		wantPath string // "" if none
		wantCmds map[string]string
	}{
		{
			name:     "string form",
			input:    `"cli.js"`,
			wantJSON: `"cli.js"`,
			wantPath: "cli.js",
		},
		{
			name:     "object form",
			input:    `{"tsc":"./bin/tsc","tsserver":"./bin/tsserver"}`,
			wantJSON: `{"tsc":"./bin/tsc","tsserver":"./bin/tsserver"}`,
			wantCmds: map[string]string{"tsc": "./bin/tsc", "tsserver": "./bin/tsserver"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b Bin
			if err := json.Unmarshal([]byte(tt.input), &b); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			if tt.wantPath != "" {
				if b.Path == nil || *b.Path != tt.wantPath {
					t.Errorf("Path = %v, want %q", b.Path, tt.wantPath)
				}
			} else if b.Path != nil {
				t.Errorf("Path = %q, want nil", *b.Path)
			}

			if len(tt.wantCmds) != len(b.Commands) {
				t.Errorf("Commands = %v, want %v", b.Commands, tt.wantCmds)
			}
			for k, v := range tt.wantCmds {
				if b.Commands[k] != v {
					t.Errorf("Commands[%q] = %q, want %q", k, b.Commands[k], v)
				}
			}

			out, err := json.Marshal(b)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(out) != tt.wantJSON {
				t.Errorf("round-trip = %s, want %s", out, tt.wantJSON)
			}
		})
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
	if containsBin := json.Valid(out) && contains(string(out), `"bin"`); containsBin {
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
