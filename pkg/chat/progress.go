package chat

import "github.com/brito-rafa/jar/pkg/duck"

type retrievedPreview struct {
	Reference  int     `json:"reference"`
	ChunkID    int     `json:"chunkId"`
	Similarity float64 `json:"similarity"`
	Source     string  `json:"source"`
	Text       string  `json:"text"`
}
type chatProgress struct {
	Part     int                `json:"part,omitempty"`
	Parts    int                `json:"parts,omitempty"`
	Stage    string             `json:"stage"`
	Language string             `json:"language,omitempty"`
	Results  []retrievedPreview `json:"results,omitempty"`
}

func retrievalProgress(docs []duck.Document, language string) chatProgress {
	p := chatProgress{Stage: "retrieved", Language: language, Results: []retrievedPreview{}}
	sources := contextSources(docs)
	for i, s := range sources {
		p.Results = append(p.Results, retrievedPreview{Reference: s.Number, ChunkID: docs[i].ID, Similarity: float64(docs[i].Similarity), Source: s.Label, Text: preview(s.Text)})
	}
	return p
}
