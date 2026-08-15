package editorial

import "testing"

func TestDesksHaveStableOrderAndIDs(t *testing.T) {
	got := Desks()
	want := []string{
		DeskWorld, DeskPoliticsSociety, DeskBusiness, DeskCulture,
		DeskScienceHealth, DeskClimate, DeskFoodTravel, DeskSport,
		DeskTechnology, DeskUnfiled,
	}
	if len(got) != len(want) {
		t.Fatalf("Desks() returned %d desks, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].ID != want[i] || got[i].Label == "" {
			t.Errorf("desk %d = %+v, want ID %q with a label", i, got[i], want[i])
		}
	}
	got[0].Label = "changed"
	if Desks()[0].Label == "changed" {
		t.Fatal("Desks exposed mutable package state")
	}
}

func TestNormalizeDeskID(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want string
		ok   bool
	}{
		{"", DeskUnfiled, true},
		{" Technology ", DeskTechnology, true},
		{"not-a-desk", "", false},
	} {
		got, ok := NormalizeDeskID(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("NormalizeDeskID(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}
