package model_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/catalog/domain/model"
)

// must returns v and fails the test setup loudly when err is set.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// entities lists what is named in the catalog, and how to build one and rename
// one, so that the rule for names is checked the same way for each. Both
// functions return the name the entity ends up with.
var entities = []struct {
	kind   string
	create func(name string) (string, error)
	rename func(name string) (string, error)
}{
	{
		kind: "area",
		create: func(name string) (string, error) {
			a, err := model.NewArea(uuid.Must(uuid.NewV7()), name)
			return a.Name, err
		},
		rename: func(name string) (string, error) {
			a := must(model.NewArea(uuid.Must(uuid.NewV7()), "old"))
			err := a.Rename(name)
			return a.Name, err
		},
	},
	{
		kind: "party",
		create: func(name string) (string, error) {
			p, err := model.NewParty(uuid.Must(uuid.NewV7()), name)
			return p.Name, err
		},
		rename: func(name string) (string, error) {
			p := must(model.NewParty(uuid.Must(uuid.NewV7()), "old"))
			err := p.Rename(name)
			return p.Name, err
		},
	},
	{
		kind: "tag",
		create: func(name string) (string, error) {
			tag, err := model.NewTag(uuid.Must(uuid.NewV7()), name)
			return tag.Name, err
		},
		rename: func(name string) (string, error) {
			tag := must(model.NewTag(uuid.Must(uuid.NewV7()), "old"))
			err := tag.Rename(name)
			return tag.Name, err
		},
	},
	{
		kind: "project",
		create: func(name string) (string, error) {
			p, err := model.NewProject(uuid.Must(uuid.NewV7()), name)
			return p.Name, err
		},
		rename: func(name string) (string, error) {
			p := must(model.NewProject(uuid.Must(uuid.NewV7()), "old"))
			err := p.Rename(name)
			return p.Name, err
		},
	},
}

func TestNamesAreTrimmed(t *testing.T) {
	for _, e := range entities {
		t.Run(e.kind, func(t *testing.T) {
			if got, err := e.create("  日本旅行 \n"); err != nil || got != "日本旅行" {
				t.Errorf("create = %q, %v, want the trimmed name", got, err)
			}
			if got, err := e.rename("\t玩樂  "); err != nil || got != "玩樂" {
				t.Errorf("rename = %q, %v, want the trimmed name", got, err)
			}
		})
	}
}

func TestNamesMayNotBeBlank(t *testing.T) {
	for _, e := range entities {
		t.Run(e.kind, func(t *testing.T) {
			for _, blank := range []string{"", "   ", " \t\n "} {
				if _, err := e.create(blank); !errors.Is(err, model.ErrNameEmpty) {
					t.Errorf("create(%q): err = %v, want ErrNameEmpty", blank, err)
				}
				got, err := e.rename(blank)
				if !errors.Is(err, model.ErrNameEmpty) {
					t.Errorf("rename(%q): err = %v, want ErrNameEmpty", blank, err)
				}
				if got != "old" {
					t.Errorf("rename(%q) left the name %q, want it unchanged", blank, got)
				}
			}
		})
	}
}
