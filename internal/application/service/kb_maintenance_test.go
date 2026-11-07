package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// TestEstimateEmbeddingCalls verifies the request-count estimate used by the
// plan endpoint: one request per full batch, plus one for the remainder.
func TestEstimateEmbeddingCalls(t *testing.T) {
	cases := []struct {
		name         string
		targetChunks int
		batchSize    int
		want         int
	}{
		{"empty", 0, 200, 0},
		{"single partial batch", 150, 200, 1},
		{"exactly one batch", 200, 200, 1},
		{"one full plus a remainder", 201, 200, 2},
		{"ten full batches", 2000, 200, 10},
		{"zero batch size falls back to no calls", 100, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := estimateEmbeddingCalls(tc.targetChunks, tc.batchSize); got != tc.want {
				t.Fatalf("estimateEmbeddingCalls(%d, %d) = %d, want %d",
					tc.targetChunks, tc.batchSize, got, tc.want)
			}
		})
	}
}

// TestEstimateMinutes verifies the wall-clock estimate never promises less
// than the work implies.
func TestEstimateMinutes(t *testing.T) {
	// 794 chunks at 200/batch = 4 requests; at 3s each = 12s → rounds to 1
	// minute with the deliberate +1.
	if got := estimateMinutes(794, 200, 3); got != 1 {
		t.Fatalf("estimateMinutes(794, 200, 3) = %d, want 1", got)
	}
	// 12000 chunks at 200/batch = 60 requests; at 3s each = 180s = 3 min.
	if got := estimateMinutes(12000, 200, 3); got != 4 {
		t.Fatalf("estimateMinutes(12000, 200, 3) = %d, want 4", got)
	}
	// No chunks → no time.
	if got := estimateMinutes(0, 200, 3); got != 0 {
		t.Fatalf("estimateMinutes(0, ...) = %d, want 0", got)
	}
}

// TestBuildKnowledgeIndexContentIsUsedForBackfill pins the indexed-text
// contract: the document title, a newline, then the chunk's embedding
// content. A backfill that drops the title silently degrades semantic
// alignment between question-style queries and statement-style chunks, so
// this is asserted rather than assumed.
func TestBuildKnowledgeIndexContentIsUsedForBackfill(t *testing.T) {
	knowledge := &types.Knowledge{Title: "语文大师"}
	chunk := &types.Chunk{Content: "  正文内容  ", ContextHeader: "第一章"}

	got := buildKnowledgeIndexContent(knowledge, chunk.EmbeddingContent())

	// EmbeddingContent() prepends the context header and trims the body.
	want := "语文大师\n第一章\n\n正文内容"
	if got != want {
		t.Fatalf("indexed content = %q, want %q", got, want)
	}
}

// TestBuildKnowledgeIndexContentWithoutTitle pins the fallback: a document
// without a title indexes its content unchanged.
func TestBuildKnowledgeIndexContentWithoutTitle(t *testing.T) {
	knowledge := &types.Knowledge{Title: "   "}
	got := buildKnowledgeIndexContent(knowledge, "正文")
	if got != "正文" {
		t.Fatalf("indexed content = %q, want %q", got, "正文")
	}
}
