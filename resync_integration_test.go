package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-kivik/kivik/v4"
	"github.com/thedevminertv/npm-replicator/pkg/npm"
	"github.com/thedevminertv/npm-replicator/pkg/replicator"
)

// fakeUpstream stands in for replicate.npmjs.com and the registry. IDs listed
// in docs exist in both; registryOnly exist in the registry alone, like the
// packages npm leaves out of the feed while still serving them.
type fakeUpstream struct {
	docs         map[string]string
	registryOnly []string
	docCount     int
}

func (f *fakeUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/registry/":
		count := f.docCount
		if count == 0 {
			count = len(f.docs)
		}
		json.NewEncoder(w).Encode(map[string]int{"doc_count": count, "update_seq": 1})

	case r.URL.Path == "/registry/_all_docs":
		ids := make([]string, 0, len(f.docs))
		for id := range f.docs {
			ids = append(ids, id)
		}
		slices.Sort(ids)

		var start string
		if s := r.URL.Query().Get("startkey"); s != "" {
			json.Unmarshal([]byte(s), &start)
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

		type row struct {
			ID    string            `json:"id"`
			Value map[string]string `json:"value"`
		}
		rows := []row{}
		for _, id := range ids {
			if id >= start && len(rows) < limit {
				rows = append(rows, row{ID: id, Value: map[string]string{"rev": f.docs[id]}})
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"rows": rows})

	case strings.HasPrefix(r.URL.Path, "/packages/"):
		name := strings.TrimPrefix(r.URL.Path, "/packages/")
		if _, ok := f.docs[name]; ok || slices.Contains(f.registryOnly, name) {
			w.Write([]byte(`{}`))
			return
		}
		http.NotFound(w, r)

	default:
		http.NotFound(w, r)
	}
}

func newResyncTestDB(t *testing.T) *kivik.DB {
	t.Helper()

	couchURL := os.Getenv("COUCHDB_TEST_URL")
	if couchURL == "" {
		t.Skip("COUCHDB_TEST_URL not set")
	}

	client, err := kivik.New("couch", couchURL)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	name := fmt.Sprintf("resync-test-%d", time.Now().UnixNano())
	if err := client.CreateDB(ctx, name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.DestroyDB(context.Background(), name) })

	db := client.DB(name)
	if err := ensureDesignDocs(ctx, db); err != nil {
		t.Fatal(err)
	}

	return db
}

func newResyncTestTracker(t *testing.T) *Tracker {
	t.Helper()

	tracker, err := NewTracker(filepath.Join(t.TempDir(), "tracker.json"), TrackerData{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tracker.Close)

	return tracker
}

func putTestDoc(t *testing.T, db *kivik.DB, id string, pkg replicator.RegistryPackage) {
	t.Helper()

	if _, err := db.Put(context.Background(), id, pkg); err != nil {
		t.Fatalf("put %s: %v", id, err)
	}
}

func getTestDoc(t *testing.T, db *kivik.DB, id string) (replicator.RegistryPackage, string, bool) {
	t.Helper()

	var pkg replicator.RegistryPackage
	row := db.Get(context.Background(), id)
	if err := row.ScanDoc(&pkg); err != nil {
		if kivik.HTTPStatus(err) == http.StatusNotFound {
			return pkg, "", false
		}
		t.Fatalf("get %s: %v", id, err)
	}
	rev, _ := row.Rev()

	return pkg, rev, true
}

func TestResyncIntegration(t *testing.T) {
	db := newResyncTestDB(t)
	tracker := newResyncTestTracker(t)

	// several pages, so ranges meet in the middle of the listing
	oldPageSize := resyncPageSize
	resyncPageSize = 3
	t.Cleanup(func() { resyncPageSize = oldPageSize })

	upstream := &fakeUpstream{
		docs: map[string]string{
			"@scope/same": "2-a",
			"Zed":         "1-z", // sorts before lowercase raw, but not in ICU
			"abc-behind":  "3-b",
			"abc-missing": "1-c",
			"ahead":       "3-d",
			"back":        "5-e",
			"m-same":      "1-m",
			"n-same":      "1-n",
		},
		registryOnly: []string{"flaky"},
	}
	srv := httptest.NewServer(upstream)
	t.Cleanup(srv.Close)
	npmClient := npm.New(npm.WithBaseURLs(srv.URL+"/registry", srv.URL+"/packages"))

	earlier := time.Now().Add(-48 * time.Hour)
	downloads := &npm.DownloadCounts{LastWeek: 42}
	putTestDoc(t, db, "@scope/same", replicator.RegistryPackage{Replicator: replicator.ReplicatorMetadata{UpstreamRev: "2-a"}})
	putTestDoc(t, db, "Zed", replicator.RegistryPackage{Replicator: replicator.ReplicatorMetadata{UpstreamRev: "1-z"}})
	putTestDoc(t, db, "abc-behind", replicator.RegistryPackage{Downloads: downloads, Replicator: replicator.ReplicatorMetadata{UpstreamRev: "2-b"}})
	putTestDoc(t, db, "ahead", replicator.RegistryPackage{Replicator: replicator.ReplicatorMetadata{UpstreamRev: "4-d"}})
	putTestDoc(t, db, "back", replicator.RegistryPackage{Replicator: replicator.ReplicatorMetadata{UpstreamRev: "5-e", DroppedFromFeedAt: &earlier}})
	putTestDoc(t, db, "m-same", replicator.RegistryPackage{Replicator: replicator.ReplicatorMetadata{UpstreamRev: "1-m"}})
	putTestDoc(t, db, "n-same", replicator.RegistryPackage{Replicator: replicator.ReplicatorMetadata{UpstreamRev: "1-n"}})
	putTestDoc(t, db, "@ghost/private", replicator.RegistryPackage{Replicator: replicator.ReplicatorMetadata{UpstreamRev: "7-g"}})
	putTestDoc(t, db, "flaky", replicator.RegistryPackage{Replicator: replicator.ReplicatorMetadata{UpstreamRev: "1-f"}})
	putTestDoc(t, db, "zz-tail-ghost", replicator.RegistryPackage{Replicator: replicator.ReplicatorMetadata{UpstreamRev: "1-t"}})

	untouched := []string{"@scope/same", "Zed", "ahead", "m-same", "n-same", "flaky"}
	revsBefore := map[string]string{}
	for _, id := range untouched {
		_, rev, _ := getTestDoc(t, db, id)
		revsBefore[id] = rev
	}

	ctx := context.Background()

	// local ranges have to line up with upstream's raw order, or IDs drift
	// into another page's range and only get sorted out by rechecks
	local, err := localResyncRange(ctx, db, "@scope/same", "abc-behind")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := local["Zed"]; !ok || len(local) != 2 {
		t.Errorf("local range (@scope/same, abc-behind] = %v, want Zed and abc-behind", local)
	}

	if err := runResync(ctx, npmClient, db, tracker); err != nil {
		t.Fatalf("runResync: %v", err)
	}

	for _, id := range untouched {
		pkg, rev, _ := getTestDoc(t, db, id)
		if rev != revsBefore[id] {
			t.Errorf("%s was rewritten (%s -> %s)", id, revsBefore[id], rev)
		}
		if pkg.Replicator.DroppedFromFeedAt != nil {
			t.Errorf("%s was marked as dropped", id)
		}
	}

	if pkg, _, _ := getTestDoc(t, db, "abc-behind"); pkg.Replicator.UpstreamRev != "3-b" || pkg.Downloads == nil || pkg.Downloads.LastWeek != 42 {
		t.Errorf("abc-behind = %+v, want upstream rev 3-b with downloads kept", pkg.Replicator)
	}
	if pkg, _, ok := getTestDoc(t, db, "abc-missing"); !ok || pkg.Replicator.UpstreamRev != "1-c" {
		t.Errorf("abc-missing exists=%v rev=%q, want created with 1-c", ok, pkg.Replicator.UpstreamRev)
	}
	if pkg, _, _ := getTestDoc(t, db, "back"); pkg.Replicator.DroppedFromFeedAt != nil {
		t.Errorf("back is still marked as dropped")
	}
	for _, id := range []string{"@ghost/private", "zz-tail-ghost"} {
		if pkg, _, _ := getTestDoc(t, db, id); pkg.Replicator.DroppedFromFeedAt == nil {
			t.Errorf("%s was not marked as dropped", id)
		}
	}

	cursor, seen, completedAt := tracker.ResyncState()
	if cursor != "" || seen != 0 || completedAt == nil {
		t.Errorf("tracker after resync = (%q, %d, %v), want finished", cursor, seen, completedAt)
	}

	// a second run has nothing left to do
	revsAfter := map[string]string{}
	var all []string
	for id := range upstream.docs {
		all = append(all, id)
	}
	all = append(all, "@ghost/private", "flaky", "zz-tail-ghost")
	for _, id := range all {
		_, rev, _ := getTestDoc(t, db, id)
		revsAfter[id] = rev
	}
	if err := runResync(ctx, npmClient, db, tracker); err != nil {
		t.Fatalf("second runResync: %v", err)
	}
	for _, id := range all {
		if _, rev, _ := getTestDoc(t, db, id); rev != revsAfter[id] {
			t.Errorf("second run rewrote %s", id)
		}
	}

	// unchanged design documents aren't rewritten, which would reindex them
	_, ddocRev, _ := getTestDoc(t, db, "_design/resync")
	if err := ensureDesignDocs(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, rev, _ := getTestDoc(t, db, "_design/resync"); rev != ddocRev {
		t.Errorf("design document rewritten although unchanged")
	}

	// the stats view sees both marks
	var dropped int
	view := db.Query(ctx, "_design/resync", "dropped")
	if view.Next() {
		view.ScanValue(&dropped)
	}
	view.Close()
	if dropped != 2 {
		t.Errorf("dropped view = %d, want 2", dropped)
	}
}

func TestResyncTruncatedListing(t *testing.T) {
	db := newResyncTestDB(t)
	tracker := newResyncTestTracker(t)

	// upstream claims far more documents than it lists
	upstream := &fakeUpstream{docs: map[string]string{"a": "1-a"}, docCount: 1000}
	srv := httptest.NewServer(upstream)
	t.Cleanup(srv.Close)
	npmClient := npm.New(npm.WithBaseURLs(srv.URL+"/registry", srv.URL+"/packages"))

	putTestDoc(t, db, "a", replicator.RegistryPackage{Replicator: replicator.ReplicatorMetadata{UpstreamRev: "1-a"}})
	putTestDoc(t, db, "b", replicator.RegistryPackage{Replicator: replicator.ReplicatorMetadata{UpstreamRev: "1-b"}})

	if err := runResync(context.Background(), npmClient, db, tracker); err == nil {
		t.Fatal("runResync succeeded on a truncated listing")
	}
	if pkg, _, _ := getTestDoc(t, db, "b"); pkg.Replicator.DroppedFromFeedAt != nil {
		t.Error("b was marked as dropped from a truncated listing")
	}
	if _, _, completedAt := tracker.ResyncState(); completedAt != nil {
		t.Error("truncated resync recorded as completed")
	}
}
