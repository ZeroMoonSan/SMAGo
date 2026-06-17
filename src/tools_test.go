package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testRegistry(t *testing.T) (*ToolRegistry, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := &Config{DataDir: dir}
	r := NewToolRegistry(cfg)
	r.registerDefaults()
	return r, dir
}

func createTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	full := filepath.Join(dir, name)
	_ = os.MkdirAll(filepath.Dir(full), 0755)
	if err := os.WriteFile(full, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return name
}

func readFile(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// --- write_file: guard (no read_file first) ---

func TestWriteFile_NoReadFirst(t *testing.T) {
	r, dir := testRegistry(t)
	createTestFile(t, dir, "test.txt", "hello")

	_, err := r.writeFile(context.Background(), map[string]any{
		"path":    "test.txt",
		"content": "world",
	})
	if err == nil {
		t.Fatal("expected error when write_file called without read_file")
	}
	if !strings.Contains(err.Error(), "read_file must be called") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- write_file: after read_file ---

func TestWriteFile_AfterRead(t *testing.T) {
	r, dir := testRegistry(t)
	createTestFile(t, dir, "test.txt", "hello")

	_, err := r.readFile(context.Background(), map[string]any{"path": "test.txt"})
	if err != nil {
		t.Fatal(err)
	}

	out, err := r.writeFile(context.Background(), map[string]any{
		"path":    "test.txt",
		"content": "world",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ok: wrote 5 bytes") {
		t.Fatalf("unexpected output: %s", out)
	}
	if got := readFile(t, dir, "test.txt"); got != "world" {
		t.Fatalf("file content = %q, want %q", got, "world")
	}
}

// --- write_file: no mkdir (parent dir doesn't exist) ---

func TestWriteFile_NoMkdir(t *testing.T) {
	r, dir := testRegistry(t)
	// Create file in existing dir, read it, then try to write to a
	// sibling path whose parent doesn't exist.
	createTestFile(t, dir, "existing.txt", "data")
	_, _ = r.readFile(context.Background(), map[string]any{"path": "existing.txt"})

	// Now try to write to "sub/new.txt" — sub/ doesn't exist.
	// First we need to mark it as read... but it doesn't exist on disk.
	// So we use bash to create the file, read it, then delete the parent.
	// Simpler: just mark it as read manually for the test.
	r.MarkRead("sub/new.txt")

	_, err := r.writeFile(context.Background(), map[string]any{
		"path":    "sub/new.txt",
		"content": "new",
	})
	if err == nil {
		t.Fatal("expected error when writing to non-existent directory")
	}
	if !strings.Contains(err.Error(), "directory does not exist") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- edit_file: replace ---

func TestEditFile_Replace(t *testing.T) {
	r, dir := testRegistry(t)
	createTestFile(t, dir, "lines.txt", "line1\nline2\nline3\nline4\nline5")
	_, _ = r.readFile(context.Background(), map[string]any{"path": "lines.txt"})

	out, err := r.editFile(context.Background(), map[string]any{
		"path":    "lines.txt",
		"action":  "replace",
		"start":   2,
		"end":     3,
		"content": "NEW2\nNEW3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "replace") {
		t.Fatalf("unexpected output: %s", out)
	}
	got := readFile(t, dir, "lines.txt")
	expected := "line1\nNEW2\nNEW3\nline4\nline5"
	if got != expected {
		t.Fatalf("after replace:\ngot:  %q\nwant: %q", got, expected)
	}
}

// --- edit_file: delete ---

func TestEditFile_Delete(t *testing.T) {
	r, dir := testRegistry(t)
	createTestFile(t, dir, "lines.txt", "line1\nline2\nline3\nline4\nline5")
	_, _ = r.readFile(context.Background(), map[string]any{"path": "lines.txt"})

	out, err := r.editFile(context.Background(), map[string]any{
		"path":   "lines.txt",
		"action": "delete",
		"start":  2,
		"end":    4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "delete") {
		t.Fatalf("unexpected output: %s", out)
	}
	got := readFile(t, dir, "lines.txt")
	expected := "line1\nline5"
	if got != expected {
		t.Fatalf("after delete:\ngot:  %q\nwant: %q", got, expected)
	}
}

// --- edit_file: insert ---

func TestEditFile_Insert(t *testing.T) {
	r, dir := testRegistry(t)
	createTestFile(t, dir, "lines.txt", "line1\nline2\nline3")
	_, _ = r.readFile(context.Background(), map[string]any{"path": "lines.txt"})

	out, err := r.editFile(context.Background(), map[string]any{
		"path":    "lines.txt",
		"action":  "insert",
		"start":   2,
		"content": "AFTER2a\nAFTER2b",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "insert") {
		t.Fatalf("unexpected output: %s", out)
	}
	got := readFile(t, dir, "lines.txt")
	expected := "line1\nline2\nAFTER2a\nAFTER2b\nline3"
	if got != expected {
		t.Fatalf("after insert:\ngot:  %q\nwant: %q", got, expected)
	}
}

// --- edit_file: no read first ---

func TestEditFile_NoReadFirst(t *testing.T) {
	r, dir := testRegistry(t)
	createTestFile(t, dir, "test.txt", "hello")

	_, err := r.editFile(context.Background(), map[string]any{
		"path":   "test.txt",
		"action": "delete",
		"start":  1,
	})
	if err == nil {
		t.Fatal("expected error when edit_file called without read_file")
	}
	if !strings.Contains(err.Error(), "read_file must be called") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- edit_file: replace single line ---

func TestEditFile_ReplaceSingleLine(t *testing.T) {
	r, dir := testRegistry(t)
	createTestFile(t, dir, "lines.txt", "line1\nline2\nline3")
	_, _ = r.readFile(context.Background(), map[string]any{"path": "lines.txt"})

	_, err := r.editFile(context.Background(), map[string]any{
		"path":    "lines.txt",
		"action":  "replace",
		"start":   2,
		"content": "REPLACED",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := readFile(t, dir, "lines.txt")
	expected := "line1\nREPLACED\nline3"
	if got != expected {
		t.Fatalf("after replace single:\ngot:  %q\nwant: %q", got, expected)
	}
}

// --- edit_file: delete single line ---

func TestEditFile_DeleteSingleLine(t *testing.T) {
	r, dir := testRegistry(t)
	createTestFile(t, dir, "lines.txt", "line1\nline2\nline3")
	_, _ = r.readFile(context.Background(), map[string]any{"path": "lines.txt"})

	_, err := r.editFile(context.Background(), map[string]any{
		"path":   "lines.txt",
		"action": "delete",
		"start":  2,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := readFile(t, dir, "lines.txt")
	expected := "line1\nline3"
	if got != expected {
		t.Fatalf("after delete single:\ngot:  %q\nwant: %q", got, expected)
	}
}

// --- edit_file: bounds check ---

func TestEditFile_OutOfBounds(t *testing.T) {
	r, dir := testRegistry(t)
	createTestFile(t, dir, "lines.txt", "line1\nline2\nline3")
	_, _ = r.readFile(context.Background(), map[string]any{"path": "lines.txt"})

	_, err := r.editFile(context.Background(), map[string]any{
		"path":   "lines.txt",
		"action": "delete",
		"start":  99,
	})
	if err == nil {
		t.Fatal("expected out-of-bounds error")
	}
}

// --- edit_file: invalid action ---

func TestEditFile_InvalidAction(t *testing.T) {
	r, dir := testRegistry(t)
	createTestFile(t, dir, "lines.txt", "line1")
	_, _ = r.readFile(context.Background(), map[string]any{"path": "lines.txt"})

	_, err := r.editFile(context.Background(), map[string]any{
		"path":   "lines.txt",
		"action": "zap",
		"start":  1,
	})
	if err == nil {
		t.Fatal("expected error for invalid action")
	}
	if !strings.Contains(err.Error(), "unknown action") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- read_file: marks file as read ---

func TestReadFile_MarksAsRead(t *testing.T) {
	r, dir := testRegistry(t)
	createTestFile(t, dir, "test.txt", "content")

	if r.WasRead("test.txt") {
		t.Fatal("file should not be marked as read before calling read_file")
	}

	_, err := r.readFile(context.Background(), map[string]any{"path": "test.txt"})
	if err != nil {
		t.Fatal(err)
	}

	if !r.WasRead("test.txt") {
		t.Fatal("file should be marked as read after read_file")
	}
}

// --- edit_file: insert at beginning ---

func TestEditFile_InsertAtStart(t *testing.T) {
	r, dir := testRegistry(t)
	createTestFile(t, dir, "lines.txt", "line1\nline2")
	_, _ = r.readFile(context.Background(), map[string]any{"path": "lines.txt"})

	_, err := r.editFile(context.Background(), map[string]any{
		"path":    "lines.txt",
		"action":  "insert",
		"start":   0,
		"content": "FIRST",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := readFile(t, dir, "lines.txt")
	expected := "FIRST\nline1\nline2"
	if got != expected {
		t.Fatalf("after insert at start:\ngot:  %q\nwant: %q", got, expected)
	}
}

// --- edit_file: insert at end ---

func TestEditFile_InsertAtEnd(t *testing.T) {
	r, dir := testRegistry(t)
	createTestFile(t, dir, "lines.txt", "line1\nline2")
	_, _ = r.readFile(context.Background(), map[string]any{"path": "lines.txt"})

	_, err := r.editFile(context.Background(), map[string]any{
		"path":    "lines.txt",
		"action":  "insert",
		"start":   2,
		"content": "LAST",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := readFile(t, dir, "lines.txt")
	expected := "line1\nline2\nLAST"
	if got != expected {
		t.Fatalf("after insert at end:\ngot:  %q\nwant: %q", got, expected)
	}
}

// --- edit_file: delete all lines ---

func TestEditFile_DeleteAll(t *testing.T) {
	r, dir := testRegistry(t)
	createTestFile(t, dir, "lines.txt", "line1\nline2\nline3")
	_, _ = r.readFile(context.Background(), map[string]any{"path": "lines.txt"})

	_, err := r.editFile(context.Background(), map[string]any{
		"path":   "lines.txt",
		"action": "delete",
		"start":  1,
		"end":    3,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := readFile(t, dir, "lines.txt")
	if got != "" {
		t.Fatalf("after delete all:\ngot:  %q\nwant: empty", got)
	}
}

// --- write_file: overwrite same content ---

func TestWriteFile_OverwriteSameContent(t *testing.T) {
	r, dir := testRegistry(t)
	createTestFile(t, dir, "test.txt", "old")
	_, _ = r.readFile(context.Background(), map[string]any{"path": "test.txt"})

	_, err := r.writeFile(context.Background(), map[string]any{
		"path":    "test.txt",
		"content": "new",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, dir, "test.txt"); got != "new" {
		t.Fatalf("got %q, want %q", got, "new")
	}
}

// --- edit_file: multiple sequential edits ---

func TestEditFile_SequentialEdits(t *testing.T) {
	r, dir := testRegistry(t)
	createTestFile(t, dir, "lines.txt", "A\nB\nC")
	_, _ = r.readFile(context.Background(), map[string]any{"path": "lines.txt"})

	// Insert after line 1
	_, err := r.editFile(context.Background(), map[string]any{
		"path":    "lines.txt",
		"action":  "insert",
		"start":   1,
		"content": "X",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Now: A\nX\nB\nC

	// Replace lines 2-2
	_, err = r.editFile(context.Background(), map[string]any{
		"path":    "lines.txt",
		"action":  "replace",
		"start":   2,
		"end":     2,
		"content": "Y",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Now: A\nY\nB\nC

	got := readFile(t, dir, "lines.txt")
	expected := "A\nY\nB\nC"
	if got != expected {
		t.Fatalf("after sequential edits:\ngot:  %q\nwant: %q", got, expected)
	}
}
