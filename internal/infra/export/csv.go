package export

import (
	"context"
	"encoding/csv"
	"os"

	"github.com/example/auto-anki/internal/domain"
)

type CSVExporter struct{}

func (CSVExporter) ExportCSV(ctx context.Context, cards []domain.Card, filePath string) error {
	_ = ctx
	f, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	// Simple Anki CSV: Front,Back
	if err := w.Write([]string{"Front", "Back"}); err != nil {
		return err
	}

	for _, c := range cards {
		if err := w.Write([]string{c.Question, c.Answer}); err != nil {
			return err
		}
	}
	return w.Error()
}
