package knowledge

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/ArminDashti/chatbot-api/internal/store"
)

const SupportRole = `You are a support guide for a distribution ERP (integrated wholesale / pakhsh software). Help users find screens, explain what a module is for, and walk through documented workflows.
Reply in the same language as the user (Persian or English).
Use only the retrieved documentation below. If the docs do not mention a screen or step, say you do not have that in the documentation. Do not invent menu paths, field names, or business rules.`

func NormalizeGuidePaths(raw []string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		item = strings.TrimSpace(strings.Trim(item, `"'`))
		if item == "" {
			continue
		}
		item = filepath.Clean(item)
		key := strings.ToLower(item)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func IndexGuidePaths(ctx context.Context, db *sql.DB, paths []string, fallbackDir string) (int, error) {
	roots := NormalizeGuidePaths(paths)
	if len(roots) == 0 {
		roots = NormalizeGuidePaths([]string{fallbackDir})
	}
	chunks := make([]store.KnowledgeChunk, 0)
	for _, root := range roots {
		part, err := collectMarkdownPath(root)
		if err != nil {
			return 0, err
		}
		chunks = append(chunks, part...)
	}
	return store.ReplaceKnowledgeChunks(ctx, db, chunks)
}

func IndexMarkdownDir(ctx context.Context, db *sql.DB, root string) (int, error) {
	return IndexGuidePaths(ctx, db, nil, root)
}

func collectMarkdownPath(root string) ([]store.KnowledgeChunk, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, nil
	}
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return readMarkdownFile(root, filepath.ToSlash(root))
	}

	chunks := make([]store.KnowledgeChunk, 0)
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			name := strings.ToLower(d.Name())
			if name == ".git" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		base := strings.ToLower(d.Name())
		if base == "web.config" || strings.Contains(base, "secret") {
			return nil
		}
		if !strings.HasSuffix(base, ".md") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		source := filepath.ToSlash(filepath.Join(filepath.Base(root), rel))
		part, err := readMarkdownFile(path, source)
		if err != nil {
			return err
		}
		chunks = append(chunks, part...)
		return nil
	})
	return chunks, err
}

func readMarkdownFile(path, source string) ([]store.KnowledgeChunk, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(raw) {
		return nil, nil
	}
	return splitMarkdownChunks(source, string(raw)), nil
}

func splitMarkdownChunks(relPath, markdown string) []store.KnowledgeChunk {
	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	heading := ""
	var body strings.Builder
	out := make([]store.KnowledgeChunk, 0)
	flush := func() {
		text := strings.TrimSpace(body.String())
		if text == "" && heading == "" {
			return
		}
		if text == "" {
			text = heading
		}
		out = append(out, store.KnowledgeChunk{RelPath: relPath, Heading: heading, Body: clipRunes(text, 8000)})
		body.Reset()
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "#") {
			flush()
			heading = strings.TrimSpace(strings.TrimLeft(line, "#"))
			continue
		}
		body.WriteString(line)
		body.WriteByte('\n')
	}
	flush()
	return out
}

func clipRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max]) + "…"
}

func BuildSystemPrompt(baseRules, retrieved string) string {
	var b strings.Builder
	b.WriteString(SupportRole)
	if strings.TrimSpace(baseRules) != "" {
		b.WriteString("\n\nAdditional company rules:\n")
		b.WriteString(strings.TrimSpace(baseRules))
	}
	if strings.TrimSpace(retrieved) != "" {
		b.WriteString("\n\nRetrieved documentation:\n")
		b.WriteString(strings.TrimSpace(retrieved))
	}
	return b.String()
}
