package moze_test

import (
	"context"
	"errors"
	"testing"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/usecase/moze"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/out/filefailure"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/out/mapping"
)

// errDisk is the reason a port gives for a file it cannot read or write, in its own
// words: the system's, which the command line shows as they are.
var errDisk = errors.New("the disk is gone")

// brokenSource is an export that cannot be read.
type brokenSource struct{ err error }

func (s brokenSource) Rows(context.Context, string) ([]model.Row, error) { return nil, s.err }

// brokenMapping is a mapping file that cannot be read, or cannot be written.
type brokenMapping struct{ load, add error }

func (m brokenMapping) Load(context.Context, string) (mapping.Loaded, error) {
	return mapping.Loaded{}, m.load
}

func (m brokenMapping) Append(context.Context, string, model.Mapping) error { return m.add }

// brokenRates is a rates file that cannot be read.
type brokenRates struct{ err error }

func (r brokenRates) Load(context.Context, string) (model.Rates, bool, error) {
	return model.Rates{}, false, r.err
}

// inputsWith is working inputs, the export of the fixture and a mapping that lacks
// some of what it needs, which changes the one that is given.
func inputsWith(change func(*moze.Inputs)) moze.Inputs {
	in := moze.Inputs{
		Source:  fakeSource{rows: fixture()},
		Mapping: &fakeMapping{loaded: mapping.Loaded{Mapping: fixtureMapping(), Exists: true}},
		Rates:   fakeRates{fixtureRates()},
	}
	change(&in)
	return in
}

func planOf(in moze.Inputs) error {
	files := usecase.Files{Export: "export.csv", Mapping: "mapping.yaml", Rates: "rates.csv"}
	_, err := moze.NewPlanner(in).Plan(context.Background(), usecase.PlanInput{Files: files})
	return err
}

// A port that fails with an error of its own, and not with a failure, comes to the
// failure of the file that was being read or written, with its reason as it was
// given: this layer has no sentence for it, and the command line has the words.
func TestAFileAPortCannotReadOrWriteIsAFailureOfThatFile(t *testing.T) {
	tests := []struct {
		name     string
		change   func(*moze.Inputs)
		wantCode filefailure.Code
		wantFile string
	}{
		{"the export", func(in *moze.Inputs) { in.Source = brokenSource{errDisk} }, filefailure.Unreadable, "export.csv"},
		{"the mapping", func(in *moze.Inputs) { in.Mapping = brokenMapping{load: errDisk} }, filefailure.Unreadable, "mapping.yaml"},
		{"the rates", func(in *moze.Inputs) { in.Rates = brokenRates{errDisk} }, filefailure.Unreadable, "rates.csv"},
		{"the mapping, to add the guesses to", func(in *moze.Inputs) { in.Mapping = brokenMapping{add: errDisk} }, filefailure.NotWritten, "mapping.yaml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := planOf(inputsWith(tt.change))
			var f *filefailure.Failure
			if !errors.As(err, &f) {
				t.Fatalf("Plan = %v, want a *filefailure.Failure", err)
			}
			if f.Code != tt.wantCode || f.File != tt.wantFile || f.Line != 0 || len(f.Args) != 0 {
				t.Errorf("failure = %s about %q at line %d with %q, want %s about %q", f.Code, f.File, f.Line, f.Args, tt.wantCode, tt.wantFile)
			}
			if !errors.Is(err, errDisk) {
				t.Errorf("Plan = %v, want the reason of the port to be in it as it was given", err)
			}
		})
	}
}

// What an adapter says of its file is said already, and is returned as it is: it
// names the file and the line.
func TestAFailureOfAPortIsReturnedAsItIs(t *testing.T) {
	given := &filefailure.Failure{Code: filefailure.NotDay, File: "my.csv", Line: 4, Args: []string{"日期", "x", "2026/03/10"}}
	tests := map[string]func(*moze.Inputs){
		"the export":  func(in *moze.Inputs) { in.Source = brokenSource{given} },
		"the mapping": func(in *moze.Inputs) { in.Mapping = brokenMapping{load: given} },
		"the rates":   func(in *moze.Inputs) { in.Rates = brokenRates{given} },
		"adding":      func(in *moze.Inputs) { in.Mapping = brokenMapping{add: given} },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			err := planOf(inputsWith(change))
			var f *filefailure.Failure
			if !errors.As(err, &f) || f != given {
				t.Errorf("Plan = %v, want the failure the port gave, as it is", err)
			}
		})
	}
}

// The end of the context is not the fault of a file: it is returned as it is, and not
// told as a file that cannot be read.
func TestTheEndOfTheContextIsNotAFailureOfAFile(t *testing.T) {
	for _, reason := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(reason.Error(), func(t *testing.T) {
			err := planOf(inputsWith(func(in *moze.Inputs) { in.Source = brokenSource{reason} }))
			var f *filefailure.Failure
			if !errors.Is(err, reason) || errors.As(err, &f) {
				t.Errorf("Plan = %v, want %v and no failure", err, reason)
			}
		})
	}
}
