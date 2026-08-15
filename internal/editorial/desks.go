// Package editorial defines the stable taxonomy and composition model used by
// the newspaper views. Desk IDs are persisted and used in URLs; their display
// labels may change without migrating subscriptions.
package editorial

import "strings"

// Desk identifies a stable newspaper section.
type Desk struct {
	ID    string
	Label string
}

const (
	DeskWorld           = "world"
	DeskPoliticsSociety = "politics-society"
	DeskBusiness        = "business"
	DeskCulture         = "culture"
	DeskScienceHealth   = "science-health"
	DeskClimate         = "climate"
	DeskFoodTravel      = "food-travel"
	DeskSport           = "sport"
	DeskTechnology      = "technology"
	DeskUnfiled         = "unfiled"
)

var stableDesks = []Desk{
	{ID: DeskWorld, Label: "World"},
	{ID: DeskPoliticsSociety, Label: "Politics & Society"},
	{ID: DeskBusiness, Label: "Business"},
	{ID: DeskCulture, Label: "Culture"},
	{ID: DeskScienceHealth, Label: "Science & Health"},
	{ID: DeskClimate, Label: "Climate"},
	{ID: DeskFoodTravel, Label: "Food & Travel"},
	{ID: DeskSport, Label: "Sport"},
	{ID: DeskTechnology, Label: "Technology"},
	{ID: DeskUnfiled, Label: "Unfiled"},
}

// Desks returns the stable desks in newspaper order. The returned slice is a
// copy so callers cannot mutate the process-wide taxonomy.
func Desks() []Desk {
	return append([]Desk(nil), stableDesks...)
}

// NormalizeDeskID maps an empty legacy value to Unfiled and reports whether a
// non-empty ID belongs to the stable taxonomy.
func NormalizeDeskID(id string) (string, bool) {
	id = strings.TrimSpace(strings.ToLower(id))
	if id == "" {
		return DeskUnfiled, true
	}
	for _, desk := range stableDesks {
		if id == desk.ID {
			return id, true
		}
	}
	return "", false
}

// DeskByID resolves id, treating an empty value from an older database as
// Unfiled.
func DeskByID(id string) (Desk, bool) {
	normalized, ok := NormalizeDeskID(id)
	if !ok {
		return Desk{}, false
	}
	for _, desk := range stableDesks {
		if desk.ID == normalized {
			return desk, true
		}
	}
	return Desk{}, false
}
