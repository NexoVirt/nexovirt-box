package box

import (
	"testing"

	"github.com/NexoVirt/nexovirt-box/internal/catalog"
)

// The built-in catalog's stacks are signed, name only catalog apps and pass the rules.
func TestBuiltinStacks(t *testing.T) {
	cat, _, err := loadCatalog("")
	if err != nil {
		t.Fatal(err)
	}
	stacks := cat.Stacks()
	if len(stacks) == 0 {
		t.Fatal("no stacks in the built-in catalog")
	}
	if err := catalog.ValidateStacks(stacks, func(id string) bool { _, ok := cat.Entry(id); return ok }); err != nil {
		t.Fatal(err)
	}
	for _, s := range stacks {
		for _, a := range s.Apps {
			app, err := cat.App(a.ID)
			if err != nil {
				t.Fatalf("%s: %v", a.ID, err)
			}
			if app.DockerSocket {
				t.Errorf("stack %s installs %s, which needs the Docker socket (asks the admin)", s.ID, a.ID)
			}
		}
	}
}

func TestStackValidation(t *testing.T) {
	known := func(id string) bool { return id == "a" || id == "b" }
	ok := []catalog.Stack{{ID: "s", Name: "S", Apps: []catalog.StackApp{{ID: "a"}, {ID: "b", Answers: map[string]string{"USER": "admin"}}}}}
	if err := catalog.ValidateStacks(ok, known); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]catalog.Stack{
		{{ID: "s", Name: "S", Apps: []catalog.StackApp{{ID: "a"}}}},
		{{ID: "s", Name: "S", Apps: []catalog.StackApp{{ID: "a"}, {ID: "zz"}}}},
		{{ID: "s", Name: "S", Apps: []catalog.StackApp{{ID: "a"}, {ID: "b", Answers: map[string]string{"X": "it's"}}}}},
		{{ID: "S!", Name: "S", Apps: []catalog.StackApp{{ID: "a"}, {ID: "b"}}}},
	} {
		if catalog.ValidateStacks(bad, known) == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}
