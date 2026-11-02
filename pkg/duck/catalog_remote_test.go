package duck

import (
	"os"
	"strings"
	"testing"
)

func TestRemoteSourceMetadata(t *testing.T) {
	endpoint := os.Getenv("JAR_METADATA_TEST_URL")
	if endpoint == "" {
		t.Skip("requires isolated metadata test server")
	}
	db, err := OpenRemote(endpoint, os.Getenv("JAR_DB_TOKEN"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, query := range []string{"DROP TABLE IF EXISTS item_sources", "DROP TABLE IF EXISTS items", "DROP TABLE IF EXISTS jar_index_metadata", "CREATE TABLE items (id INT PRIMARY KEY,text VARCHAR,embedding FLOAT[2],content_hash VARCHAR,sources VARCHAR)", "CREATE TABLE jar_index_metadata(fingerprint VARCHAR,embedding_model VARCHAR,dimensions INT,indexed_at TIMESTAMP)", "INSERT INTO jar_index_metadata VALUES('fixture','fixture-model',2,current_timestamp)"} {
		if err := remoteExec(db, query); err != nil {
			t.Fatal(err)
		}
	}
	rows := []embeddedPassage{
		{passage{ID: 1, Text: "same passage", Hash: "a", Sources: []string{"Conferencias/580508RN.DOC | section 1", "Books/La_Herencia_de_Sí_Mismo.pdf | page 2"}}, []float32{0.8, 0.2}},
		{passage{ID: 2, Text: "closer but wrong date", Hash: "b", Sources: []string{"Conferencias/590508RN.DOC | section 1"}}, []float32{1, 0}},
	}
	query, err := insertBatchSQL(rows)
	if err != nil {
		t.Fatal(err)
	}
	if err := remoteExec(db, query); err != nil {
		t.Fatal(err)
	}
	if err := BuildCatalog(db); err != nil {
		t.Fatal(err)
	}
	for _, filter := range []SourceFilter{{Date: "1958-05-08"}, {Book: "herencia de si mismo"}} {
		docs, err := Search(db, []float32{1, 0}, 1, filter)
		if err != nil {
			t.Fatal(err)
		}
		if len(docs) != 1 || docs[0].ID != 1 || len(docs[0].Origins) != 1 {
			t.Fatalf("filter not applied before ranking: %+v", docs)
		}
	}
	origins, count, err := ListSources(db, SourceFilter{Date: "1958-05"})
	if err != nil || count != 1 || len(origins) != 1 || origins[0].Date != "1958-05-08" {
		t.Fatalf("%+v %d %v", origins, count, err)
	}
	docs, err := Search(db, []float32{1, 0}, 2, SourceFilter{Book: "does not exist"})
	if err != nil || len(docs) != 0 {
		t.Fatal(docs, err)
	}
	books, err := BrowseSources(db, "books", "herencia de si")
	if err != nil || len(books) != 1 || !strings.Contains(books[0].Title, "Herencia") {
		t.Fatal(books, err)
	}
	conferences, err := BrowseSources(db, "conferences", "1958")
	if err != nil || len(conferences) != 1 || conferences[0].Date != "1958-05-08" {
		t.Fatal(conferences, err)
	}
	conferences, err = BrowseSources(db, "conferences", "")
	if err != nil || len(conferences) != 2 {
		t.Fatal(conferences, err)
	}
	text, err := RetrieveConferences(db, SourceFilter{Date: "1958-05-08"})
	if err != nil || !strings.Contains(text, "same passage") || strings.Contains(text, "wrong date") || strings.Contains(text, "Herencia") {
		t.Fatalf("retrieve: %s %v", text, err)
	}
	text, err = RetrieveConferences(db, SourceFilter{Date: "2000"})
	if err != nil || !strings.Contains(text, "No indexed conferences") {
		t.Fatal(text, err)
	}
	if err := BuildCatalog(db); err != nil {
		t.Fatal(err)
	}
	state, err := Status(db)
	if err != nil || state.Chunks != 2 || state.EmbeddingModel != "fixture-model" {
		t.Fatal(state, err)
	}
	continuation := []embeddedPassage{{passage{ID: 3, Text: "The continuation beyond the cited chunk.", Hash: "continuation", Sources: []string{"Conferencias/580508RN.DOC | section 1"}}, []float32{0.5, 0.5}}}
	extra, err := insertBatchSQL(continuation)
	if err != nil {
		t.Fatal(err)
	}
	if err := remoteExec(db, extra); err != nil {
		t.Fatal(err)
	}
	if err := BuildCatalog(db); err != nil {
		t.Fatal(err)
	}
	full, err := RetrieveSourceFiles(db, []string{"Conferencias/580508RN.DOC"})
	if err != nil || !strings.Contains(full, "same passage") || !strings.Contains(full, "continuation beyond") || strings.Contains(full, "closer but wrong date") || strings.Contains(full, "Herencia") {
		t.Fatal(full, err)
	}
	if _, err := RetrieveSourceFiles(db, []string{"580508RN"}); err == nil {
		t.Fatal("substring path accepted")
	}

	bookRows := []embeddedPassage{}
	for i, entry := range []struct{ path, label, text string }{
		{"Books/Chapters.pdf", "page 1", "CHAPTER I First chapter material."},
		{"Books/Chapters.pdf", "page 2", "First chapter continuation."},
		{"Books/Chapters.pdf", "page 3", "CHAPTER II Second chapter heading."},
		{"Books/Chapters.pdf", "page 4", "Cited second chapter passage."},
		{"Books/Chapters.pdf", "page 5", "CHAPTER III Third chapter material."},
		{"Books/NoHeadings.pdf", "page 1", "Earlier page to exclude."},
		{"Books/NoHeadings.pdf", "page 2", "Cited page without known chapter."},
		{"Books/NoHeadings.pdf", "page 3", "Later page to exclude."},
		{"Books/NoHeadings.pdf", "page 0", "Distant earlier material."},
		{"Books/NoHeadings.pdf", "page 4", "Distant later material."},
	} {
		bookRows = append(bookRows, embeddedPassage{passage{ID: 4 + i, Text: entry.text, Hash: entry.label + entry.path, Sources: []string{entry.path + " | " + entry.label}}, []float32{0.5, 0.5}})
	}
	query, err = insertBatchSQL(bookRows)
	if err != nil {
		t.Fatal(err)
	}
	if err := remoteExec(db, query); err != nil {
		t.Fatal(err)
	}
	if err := BuildCatalog(db); err != nil {
		t.Fatal(err)
	}
	chapter, err := RetrieveReferenceContext(db, []string{"Books/Chapters.pdf"}, 7)
	if err != nil || !strings.Contains(chapter, "Second chapter heading") || !strings.Contains(chapter, "Cited second chapter") || strings.Contains(chapter, "First chapter") || strings.Contains(chapter, "Third chapter") {
		t.Fatal(chapter, err)
	}
	page, err := RetrieveReferenceContext(db, []string{"Books/NoHeadings.pdf"}, 10)
	if err != nil || !strings.Contains(page, "Cited page without known chapter") || !strings.Contains(page, "chapter boundaries unavailable") || !strings.Contains(page, "Earlier page") || !strings.Contains(page, "Later page") || strings.Contains(page, "Distant") {
		t.Fatal(page, err)
	}
	conference, err := RetrieveReferenceContext(db, []string{"Conferencias/580508RN.DOC"}, 1)
	if err != nil || !strings.Contains(conference, "same passage") || !strings.Contains(conference, "continuation beyond") {
		t.Fatal(conference, err)
	}

	if err := remoteExec(db, `INSERT INTO item_sources SELECT chunk_id,?,path,title,kind,date_key,precision,raw_code,search_key FROM item_sources WHERE chunk_id=7 AND path=?`, "Books/Chapters.pdf | page 2", "Books/Chapters.pdf"); err != nil {
		t.Fatal(err)
	}
	ambiguous, err := RetrieveReferenceContext(db, []string{"Books/Chapters.pdf"}, 7)
	if err != nil || !strings.Contains(ambiguous, "unavailable or ambiguous") || strings.Contains(ambiguous, "full indexed source text") || !strings.Contains(ambiguous, "previous and next paragraph") {
		t.Fatal(ambiguous, err)
	}

}
