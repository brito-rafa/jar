package duck

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ardanlabs/kronk/sdk/kronk/model"
)

type fixtureEmbedder struct{}

func (fixtureEmbedder) Unload(context.Context) error { return nil }
func (fixtureEmbedder) Embeddings(_ context.Context, d model.D) (model.EmbedReponse, error) {
	var result model.EmbedReponse
	for i := range d["input"].([]string) {
		result.Data = append(result.Data, model.EmbedData{Index: i, Embedding: []float32{1, 0}})
	}
	return result, nil
}

// Requires a disposable server: this test replaces its JAR tables.
func TestRemoteIndexReplacesCatalogTransactionally(t *testing.T) {
	endpoint := os.Getenv("JAR_INDEX_TEST_URL")
	if endpoint == "" {
		t.Skip("requires isolated indexing test server")
	}
	db, err := OpenRemote(endpoint, os.Getenv("JAR_DB_TOKEN"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	file := filepath.Join(t.TempDir(), "passages")
	if err = os.WriteFile(file, []byte("<CHUNK>Source: fixture.txt | section 1\nSynthetic passage.</CHUNK>"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, fingerprint := range []string{"first", "second"} {
		if err = Index(db, fixtureEmbedder{}, 2, file, fingerprint, "fixture-model"); err != nil {
			t.Fatal(err)
		}
		state, e := Status(db)
		if e != nil || !state.Ready || state.Chunks != 1 {
			t.Fatalf("status %+v, %v", state, e)
		}
	}
}
