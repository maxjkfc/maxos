package usecase_test

import (
	"testing"

	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
)

// PreviewBasisOf is the one mapping from the names the domain gives to what a figure is made
// of to the words of the port: each of the three names is the word that is named so, and a name
// that is not one of them says that no draft changes the figure.
func TestPreviewBasisOfSaysEachNameOfTheDomainInTheWordOfThePort(t *testing.T) {
	for name, want := range map[string]usecase.PreviewBasis{
		"confirmed": usecase.PreviewBasisConfirmed,
		"both":      usecase.PreviewBasisBoth,
		"drafts":    usecase.PreviewBasisDrafts,
		"":          usecase.PreviewBasisConfirmed,
		"mixed":     usecase.PreviewBasisConfirmed,
		"Drafts":    usecase.PreviewBasisConfirmed,
	} {
		if got := usecase.PreviewBasisOf(name); got != want {
			t.Errorf("PreviewBasisOf(%q) = %q, want %q", name, got, want)
		}
	}
}
