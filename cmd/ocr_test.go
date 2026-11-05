package cmd

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/caitun/inferkit/services/imageinput"
	"github.com/caitun/inferkit/services/ocr"
)

func TestOCRCLIInputsAndJSONLines(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "sample.avif")
	if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	command := &cobra.Command{}
	command.SetContext(t.Context())
	command.SetOut(&output)
	check := func(ctx context.Context, src imageinput.Source) (ocr.Result, error) {
		if ctx.Err() != nil {
			return ocr.Result{}, errors.Join(ErrOCRCommand, ctx.Err())
		}
		if src.Reader == nil && src.URL == "" {
			return ocr.Result{}, imageinput.ErrInput
		}
		return ocr.Result{Text: "中文", Lines: []ocr.Line{}}, nil
	}
	if err := writeSourceResults(command, []string{path, "https://example.com/a.webp"}, ErrOCRCommand, check); err != nil {
		t.Fatal(err)
	}
	assertOCRLines(t, output.String())
	if ocrCmd.HasSubCommands() {
		t.Fatal("OCR must not define a serve subcommand")
	}
}

func assertOCRLines(t *testing.T, output string) {
	t.Helper()
	count := 0
	for line := range strings.SplitSeq(strings.TrimSpace(output), "\n") {
		var result ocr.Result
		if err := json.Unmarshal([]byte(line), &result); err != nil {
			t.Fatal(err)
		}
		if result.Text != "中文" {
			t.Fatalf("missing OCR text: %q", line)
		}
		count++
	}
	if count != 2 {
		t.Fatalf("JSON Lines count: %d", count)
	}
}
