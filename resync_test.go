package main

import (
	"slices"
	"testing"

	"github.com/thedevminertv/npm-replicator/pkg/npm"
)

func TestRevIsNewer(t *testing.T) {
	tests := []struct {
		upstream, local string
		want            bool
	}{
		{"3-abc", "3-abc", false},
		{"3-abc", "", true},
		{"4-abc", "3-def", true},
		{"3-abc", "4-def", false},
		{"3-abc", "3-def", true},
		{"10-abc", "9-def", true},
		{"garbage", "3-def", true},
	}

	for _, tt := range tests {
		if got := revIsNewer(tt.upstream, tt.local); got != tt.want {
			t.Errorf("revIsNewer(%q, %q) = %v, want %v", tt.upstream, tt.local, got, tt.want)
		}
	}
}

func TestPlanResyncRange(t *testing.T) {
	upstream := []npm.DocRow{
		{ID: "same", Rev: "2-a"},
		{ID: "behind", Rev: "3-b"},
		{ID: "ahead", Rev: "3-c"},
		{ID: "missing", Rev: "1-d"},
		{ID: "back", Rev: "5-e"},
	}
	local := map[string]resyncLocalRow{
		"same":         {UpstreamRev: "2-a"},
		"behind":       {UpstreamRev: "2-b"},
		"ahead":        {UpstreamRev: "4-c"},
		"back":         {UpstreamRev: "5-e", Dropped: true},
		"gone":         {UpstreamRev: "1-f"},
		"also-gone":    {UpstreamRev: "1-g"},
		"already-gone": {UpstreamRev: "1-h", Dropped: true},
	}

	plan := planResyncRange(upstream, local)

	var upserted []string
	for _, row := range plan.Upsert {
		upserted = append(upserted, row.ID)
	}
	if want := []string{"behind", "missing", "back"}; !slices.Equal(upserted, want) {
		t.Errorf("Upsert = %v, want %v", upserted, want)
	}
	if want := []string{"also-gone", "gone"}; !slices.Equal(plan.Candidates, want) {
		t.Errorf("Candidates = %v, want %v", plan.Candidates, want)
	}
}

func TestTooManyCandidates(t *testing.T) {
	tests := []struct {
		candidates, local int
		want              bool
	}{
		{0, 0, false},
		{100, 0, false},
		{101, 0, true},
		{900, 10000, false},
		{1000, 10000, false},
		{1001, 10000, true},
	}

	for _, tt := range tests {
		if got := tooManyCandidates(tt.candidates, tt.local); got != tt.want {
			t.Errorf("tooManyCandidates(%d, %d) = %v, want %v", tt.candidates, tt.local, got, tt.want)
		}
	}
}
