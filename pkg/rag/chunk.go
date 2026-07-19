// Package rag builds and queries a semantic retrieval index over a project's corpus and knowledge base
// (ADR-0039): text is chunked, embedded, and stored as vectors; retrieval ranks chunks by cosine similarity.
package rag

import "strings"

const (
	chunkSize    = 1000 // target characters per chunk
	chunkOverlap = 150  // characters repeated between adjacent chunks for context continuity
)

// Chunk splits text into overlapping, paragraph-aware chunks for embedding. It packs whole paragraphs up to
// chunkSize, carries chunkOverlap characters into the next chunk, and hard-splits any single oversized
// paragraph. Empty/whitespace input yields no chunks.
func Chunk(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	paras := splitParagraphs(text)
	var chunks []string
	var cur strings.Builder
	flush := func() {
		s := strings.TrimSpace(cur.String())
		if s != "" {
			chunks = append(chunks, s)
		}
		cur.Reset()
	}
	for _, p := range paras {
		// A single paragraph larger than a chunk is hard-split.
		for len(p) > chunkSize {
			if cur.Len() > 0 {
				flush()
			}
			chunks = append(chunks, strings.TrimSpace(p[:chunkSize]))
			p = p[chunkSize-chunkOverlap:]
		}
		if cur.Len()+len(p)+2 > chunkSize && cur.Len() > 0 {
			prev := cur.String()
			flush()
			if tail := overlapTail(prev); tail != "" {
				cur.WriteString(tail)
				cur.WriteString("\n\n")
			}
		}
		cur.WriteString(p)
		cur.WriteString("\n\n")
	}
	flush()
	return chunks
}

func splitParagraphs(text string) []string {
	raw := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n")
	out := make([]string, 0, len(raw))
	for _, p := range raw {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// overlapTail returns the last chunkOverlap characters of s (on a word boundary when possible), to prefix
// the next chunk with a little preceding context.
func overlapTail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= chunkOverlap {
		return s
	}
	tail := s[len(s)-chunkOverlap:]
	if i := strings.IndexByte(tail, ' '); i >= 0 && i < len(tail)-1 {
		tail = tail[i+1:]
	}
	return strings.TrimSpace(tail)
}
