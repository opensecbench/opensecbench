package rag

import (
	"strings"
	"testing"
)

func TestChunkEmpty(t *testing.T) {
	if c := Chunk("   \n\n  "); c != nil {
		t.Fatalf("blank input should yield no chunks, got %v", c)
	}
}

func TestChunkParagraphs(t *testing.T) {
	// Short multi-paragraph text fits in one chunk.
	c := Chunk("Keycloak admin.\n\nDisable the console.\n\nUse TLS.")
	if len(c) != 1 || !strings.Contains(c[0], "Keycloak") || !strings.Contains(c[0], "TLS") {
		t.Fatalf("short text should be one chunk with all paragraphs: %v", c)
	}
}

func TestChunkLongSplitsWithOverlap(t *testing.T) {
	// A long single paragraph is hard-split into multiple chunks.
	long := strings.Repeat("alpha beta gamma delta ", 300) // ~6900 chars
	c := Chunk(long)
	if len(c) < 2 {
		t.Fatalf("long text should split into multiple chunks, got %d", len(c))
	}
	for _, ch := range c {
		if len(ch) > chunkSize+chunkOverlap {
			t.Fatalf("chunk exceeds size bound: %d", len(ch))
		}
	}
}
